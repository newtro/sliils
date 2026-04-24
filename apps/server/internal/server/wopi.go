package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/wopi"
)

// WOPI + Collabora integration (M10-P2).
//
// Surface:
//   POST /files/:id/edit-session     — resolve which Collabora URL to open
//                                      and mint a short-lived access_token
//   GET  /wopi/files/:id             — CheckFileInfo  (called BY Collabora)
//   GET  /wopi/files/:id/contents    — GetFile        (called BY Collabora)
//   POST /wopi/files/:id/contents    — PutFile        (called BY Collabora)
//
// The /wopi/* endpoints use token-based auth (no session cookies) because
// Collabora is a different origin and will not share cookies. They must
// therefore live OUTSIDE the usual requireAuth chain.

// ---- DTOs ---------------------------------------------------------------

type EditSessionResponse struct {
	EditURL        string    `json:"edit_url"`
	WOPISrc        string    `json:"wopi_src"`
	AccessToken    string    `json:"access_token"`
	AccessTokenTTL int64     `json:"access_token_ttl"` // ms since epoch — the Collabora convention
	ExpiresAt      time.Time `json:"expires_at"`
	CanWrite       bool      `json:"can_write"`
}

type checkFileInfoResponse struct {
	BaseFileName            string `json:"BaseFileName"`
	OwnerID                 string `json:"OwnerId"`
	Size                    int64  `json:"Size"`
	UserID                  string `json:"UserId"`
	UserFriendlyName        string `json:"UserFriendlyName,omitempty"`
	Version                 string `json:"Version"`
	UserCanWrite            bool   `json:"UserCanWrite"`
	UserCanNotWriteRelative bool   `json:"UserCanNotWriteRelative"`
	SupportsUpdate          bool   `json:"SupportsUpdate"`
	SupportsLocks           bool   `json:"SupportsLocks"`
	DisablePrint            bool   `json:"DisablePrint"`
	DisableExport           bool   `json:"DisableExport"`
	DisableCopy             bool   `json:"DisableCopy"`
	LastModifiedTime        string `json:"LastModifiedTime"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountWOPI(api *echo.Group) {
	// Authenticated session endpoint — needs the normal bearer token.
	authed := api.Group("")
	authed.Use(s.requireAuth())
	authed.POST("/files/:id/edit-session", s.createEditSession)

	// Unauthenticated WOPI endpoints. Access is gated by the signed
	// access_token query parameter, which Collabora carries on every
	// call. requireAuth() would refuse these.
	api.GET("/wopi/files/:id", s.wopiCheckFileInfo)
	api.GET("/wopi/files/:id/contents", s.wopiGetFile)
	api.POST("/wopi/files/:id/contents", s.wopiPutFile)
}

// ---- edit-session (UI → server) ----------------------------------------

func (s *Server) createEditSession(c echo.Context) error {
	if s.collabora == nil {
		return problem.ServiceUnavailable("collabora is not configured on this server")
	}
	user := userFromContext(c)
	fileID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}

	// Locate the file under the user's RLS scope. Same walk-the-memberships
	// pattern as downloadFile, because files are RLS-scoped per workspace.
	row, workspaceID, err := s.resolveFileForUser(c.Request().Context(), user.ID, fileID)
	if err != nil {
		return err
	}

	// Choose an action. MIME is preferred; filename extension is the
	// fallback for older Collabora builds.
	disc, err := s.collabora.Get(c.Request().Context())
	if err != nil {
		return problem.Internal("collabora discovery: " + err.Error())
	}
	preferredAction := "edit"
	if c.QueryParam("mode") == "view" {
		preferredAction = "view"
	}
	action := disc.ActionForMime(row.Mime, row.Filename, preferredAction)
	if action == nil {
		return problem.BadRequest("this file type is not supported by Collabora")
	}

	canWrite := preferredAction == "edit" && row.ScanStatus != "infected"

	token, exp, err := s.wopiTokens.Issue(user.ID, workspaceID, fileID, canWrite)
	if err != nil {
		return problem.Internal("mint wopi token: " + err.Error())
	}

	// WOPISrc is the URL Collabora calls back to. It must be reachable
	// from Collabora (which, in typical deployments, is a different
	// process on another host). Use the server's PublicBaseURL.
	wopiSrc := fmt.Sprintf("%s/api/v1/wopi/files/%d", strings.TrimRight(s.cfg.PublicBaseURL, "/"), fileID)

	editURL := buildCollaboraEditURL(action.URLSrc, wopiSrc, user.DisplayName, userLocale(c))

	return c.JSON(http.StatusOK, EditSessionResponse{
		EditURL:        editURL,
		WOPISrc:        wopiSrc,
		AccessToken:    token,
		AccessTokenTTL: exp.UnixMilli(),
		ExpiresAt:      exp,
		CanWrite:       canWrite,
	})
}

// ---- Collabora → WOPI endpoints ----------------------------------------

func (s *Server) wopiCheckFileInfo(c echo.Context) error {
	if s.wopiTokens == nil {
		return problem.ServiceUnavailable("wopi not configured")
	}
	claims, err := s.parseWOPIRequest(c)
	if err != nil {
		return err
	}
	file, err := s.loadWOPIFile(c.Request().Context(), claims)
	if err != nil {
		return err
	}
	resp := checkFileInfoResponse{
		BaseFileName:     file.Filename,
		OwnerID:          strconv.FormatInt(derefInt64(file.UploaderUserID), 10),
		Size:             file.SizeBytes,
		UserID:           strconv.FormatInt(claims.UserID, 10),
		UserFriendlyName: s.userDisplayName(c.Request().Context(), claims.UserID),
		Version:          file.Sha256,
		UserCanWrite:     claims.CanWrite && file.ScanStatus != "infected",
		// We currently do not support Office's "save as copy" flow. Marking
		// UserCanNotWriteRelative=true stops Collabora from offering it.
		UserCanNotWriteRelative: true,
		SupportsUpdate:          true,
		SupportsLocks:           false,
		LastModifiedTime:        file.CreatedAt.Time.UTC().Format(time.RFC3339),
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *Server) wopiGetFile(c echo.Context) error {
	if s.wopiTokens == nil || s.storage == nil {
		return problem.ServiceUnavailable("wopi not configured")
	}
	claims, err := s.parseWOPIRequest(c)
	if err != nil {
		return err
	}
	file, err := s.loadWOPIFile(c.Request().Context(), claims)
	if err != nil {
		return err
	}
	if file.ScanStatus == "infected" {
		return problem.Forbidden("file quarantined by antivirus")
	}
	r, err := s.storage.Get(c.Request().Context(), file.StorageKey)
	if err != nil {
		return problem.Internal("read storage: " + err.Error())
	}
	defer r.Close()
	c.Response().Header().Set(echo.HeaderContentType, file.Mime)
	c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(file.SizeBytes, 10))
	c.Response().WriteHeader(http.StatusOK)
	_, _ = io.Copy(c.Response().Writer, r)
	return nil
}

func (s *Server) wopiPutFile(c echo.Context) error {
	if s.wopiTokens == nil || s.storage == nil {
		return problem.ServiceUnavailable("wopi not configured")
	}
	claims, err := s.parseWOPIRequest(c)
	if err != nil {
		return err
	}
	if !claims.CanWrite {
		return problem.Forbidden("token does not grant write")
	}
	file, err := s.loadWOPIFile(c.Request().Context(), claims)
	if err != nil {
		return err
	}

	// Stream the new version into a buffer so we can hash + get size
	// without a second trip. Files are capped at the storage layer.
	var buf bytes.Buffer
	n, err := io.Copy(&buf, c.Request().Body)
	if err != nil {
		return problem.BadRequest("read body: " + err.Error())
	}
	sum := sha256.Sum256(buf.Bytes())
	sha := hex.EncodeToString(sum[:])

	// Replace bytes in storage. We keep the same storage_key so file.id
	// stays stable and any attachments pointing at it remain valid.
	if err := s.storage.Put(c.Request().Context(), file.StorageKey, bytes.NewReader(buf.Bytes()), n, file.Mime); err != nil {
		return problem.Internal("write storage: " + err.Error())
	}

	// Update the row: new size, new sha256, reset the scan status so the
	// AV worker re-scans once M5.1 lands. We use the owner pool because
	// the WOPI endpoint has no user-scoped session from Collabora's side
	// — the claims give us the workspace id to apply RLS manually.
	if err := db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: claims.UserID, WorkspaceID: claims.WorkspaceID},
		func(scope db.TxScope) error {
			return scope.Queries.UpdateFileBytes(c.Request().Context(), sqlcgen.UpdateFileBytesParams{
				ID:        claims.FileID,
				Sha256:    sha,
				SizeBytes: n,
				Mime:      file.Mime,
			})
		}); err != nil {
		return problem.Internal("update file metadata: " + err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"LastModifiedTime": time.Now().UTC().Format(time.RFC3339)})
}

// ---- helpers -----------------------------------------------------------

// parseWOPIRequest pulls `access_token` off the query string, validates
// the signature, and confirms the path param matches the token's fileid.
func (s *Server) parseWOPIRequest(c echo.Context) (*wopi.Claims, error) {
	raw := c.QueryParam("access_token")
	if raw == "" {
		return nil, problem.Unauthorized("missing access_token")
	}
	claims, err := s.wopiTokens.Parse(raw)
	if err != nil {
		return nil, problem.Unauthorized("invalid access_token")
	}
	// Cross-check URL param vs claim to stop token reuse across files.
	paramID, err := parseInt64Param(c, "id")
	if err != nil {
		return nil, err
	}
	if paramID != claims.FileID {
		return nil, problem.Forbidden("token does not apply to this file")
	}
	return claims, nil
}

func (s *Server) loadWOPIFile(ctx context.Context, claims *wopi.Claims) (*sqlcgen.File, error) {
	var file sqlcgen.File
	err := db.WithTx(ctx, s.pool.Pool,
		db.TxOptions{UserID: claims.UserID, WorkspaceID: claims.WorkspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			f, err := scope.Queries.GetFileByID(ctx, claims.FileID)
			if err != nil {
				return err
			}
			file = f
			return nil
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, problem.NotFound("file not found")
		}
		return nil, problem.Internal("load file: " + err.Error())
	}
	return &file, nil
}

func (s *Server) resolveFileForUser(ctx context.Context, userID, fileID int64) (*sqlcgen.File, int64, error) {
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return nil, 0, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		var row sqlcgen.File
		var found bool
		err := db.WithTx(ctx, s.pool.Pool,
			db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true},
			func(scope db.TxScope) error {
				f, err := scope.Queries.GetFileByID(ctx, fileID)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return nil
					}
					return err
				}
				row = f
				found = true
				return nil
			})
		if err != nil {
			return nil, 0, problem.Internal("load file: " + err.Error())
		}
		if found {
			return &row, wsID, nil
		}
	}
	return nil, 0, problem.NotFound("file not found")
}

func (s *Server) userDisplayName(ctx context.Context, userID int64) string {
	if userID == 0 || s.ownerPool == nil {
		return ""
	}
	var name string
	_ = s.ownerPool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, userID).Scan(&name)
	return name
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// buildCollaboraEditURL takes the urlsrc template Collabora returned in
// its discovery XML and fills in the WOPI source + optional UI params.
// The urlsrc usually ends in `?` with some trailing tokens like
// `<ui=UI_LLCC&>` — we strip those placeholder tokens and then append
// our WOPISrc parameter.
func buildCollaboraEditURL(urlSrc, wopiSrc, displayName, locale string) string {
	// Strip the angle-bracket placeholders Collabora includes in its
	// discovery output (e.g. `<ui=UI_LLCC&>`). Since we fill none of
	// them in, they're not useful to us and they confuse URL parsers.
	cleaned := stripPlaceholders(urlSrc)

	sep := "?"
	if strings.Contains(cleaned, "?") {
		sep = "&"
	}
	q := url.Values{}
	q.Set("WOPISrc", wopiSrc)
	if locale != "" {
		q.Set("lang", locale)
	}
	if displayName != "" {
		q.Set("username", displayName)
	}
	return cleaned + sep + q.Encode()
}

func stripPlaceholders(s string) string {
	// Remove every <...> run. Collabora discovery only uses them to
	// signal optional host-injected params; a naive scanner suffices.
	out := make([]byte, 0, len(s))
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out = append(out, s[i])
			}
		}
	}
	return strings.TrimRight(string(out), "&?")
}

// userLocale pulls a sensible IETF tag from Accept-Language. We currently
// only use it to seed Collabora's UI language; nothing else hinges on it.
func userLocale(c echo.Context) string {
	al := c.Request().Header.Get("Accept-Language")
	if al == "" {
		return ""
	}
	// Grab the first tag before the comma/semicolon.
	if i := strings.IndexAny(al, ",;"); i >= 0 {
		return strings.TrimSpace(al[:i])
	}
	return strings.TrimSpace(al)
}
