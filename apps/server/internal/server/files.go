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
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	sfiles "github.com/sliils/sliils/apps/server/internal/files"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// FileDTO is the shape returned by upload / attachment endpoints. Never
// includes storage_backend or storage_key — those are server-internal.
type FileDTO struct {
	ID         int64     `json:"id"`
	Filename   string    `json:"filename"`
	MIME       string    `json:"mime"`
	SizeBytes  int64     `json:"size_bytes"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	ScanStatus string    `json:"scan_status"`
	URL        string    `json:"url"` // auth-required GET of the raw bytes
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Server) mountFiles(api *echo.Group) {
	g := api.Group("/files")
	g.Use(s.requireAuth())
	g.Use(s.requireTenantWriteLimit())
	g.POST("", s.uploadFile)
	g.GET("/:id/raw", s.downloadFile)
}

// uploadFile accepts a multipart upload with a required "workspace_id"
// form field. The handler:
//   1. Reads the bytes (capped at sfiles.MaxUploadSize)
//   2. Sniffs the real MIME type and strips EXIF for JPEG/PNG via re-encode
//   3. Computes SHA-256 over the processed bytes for content-addressed
//      storage + per-workspace dedupe
//   4. If the (workspace, sha256) already exists, returns the existing
//      row without re-writing to storage — uploads are idempotent
//   5. Otherwise writes to IStorage under ws/{wsID}/{shard}/{sha}, creates
//      the file row, returns the DTO.
func (s *Server) uploadFile(c echo.Context) error {
	if s.storage == nil {
		return problem.Internal("storage not configured")
	}

	user := userFromContext(c)
	workspaceID, err := strconv.ParseInt(c.FormValue("workspace_id"), 10, 64)
	if err != nil || workspaceID <= 0 {
		return problem.BadRequest("workspace_id form field required")
	}

	// Membership check: only workspace members can upload, via the
	// workspaces RLS policy.
	if !s.userInWorkspace(c.Request().Context(), user.ID, workspaceID) {
		return problem.NotFound("workspace not found")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return problem.BadRequest("file form field required")
	}
	if fh.Size > sfiles.MaxUploadSize {
		return problem.BadRequest(fmt.Sprintf("upload exceeds %d bytes", sfiles.MaxUploadSize))
	}
	src, err := fh.Open()
	if err != nil {
		return problem.Internal("open upload: " + err.Error())
	}
	defer src.Close()

	processed, err := sfiles.Ingest(src, fh.Filename)
	if err != nil {
		return problem.BadRequest(err.Error())
	}

	sum := sha256.Sum256(processed.Body)
	shaHex := hex.EncodeToString(sum[:])

	// Short-circuit: if this workspace already has this content, re-use the
	// existing file row so two composers uploading the same sticker share
	// storage and the same file id.
	if existing, dto, ok := s.existingFile(c.Request().Context(), user.ID, workspaceID, shaHex); ok {
		_ = existing
		return c.JSON(http.StatusOK, dto)
	}

	storageKey := sfiles.StorageKey(workspaceID, shaHex)
	if err := s.storage.Put(c.Request().Context(), storageKey,
		bytes.NewReader(processed.Body),
		int64(len(processed.Body)), processed.MIME); err != nil {
		return problem.Internal("store file: " + err.Error())
	}

	var created sqlcgen.File
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			f, err := scope.Queries.CreateFile(c.Request().Context(), sqlcgen.CreateFileParams{
				WorkspaceID:     workspaceID,
				UploaderUserID:  &user.ID,
				StorageBackend:  s.storage.Backend(),
				StorageKey:      storageKey,
				Filename:        sanitizeFilename(fh.Filename),
				Mime:            processed.MIME,
				SizeBytes:       int64(len(processed.Body)),
				Sha256:          shaHex,
				ScanStatus:      defaultScanStatus(),
				Width:           nullInt(processed.Width),
				Height:          nullInt(processed.Height),
			})
			if err != nil {
				return err
			}
			created = f
			return nil
		})
	if err != nil {
		// Orphan the stored bytes — periodic sweep would clean up.
		return problem.Internal("create file: " + err.Error())
	}

	return c.JSON(http.StatusCreated, fileDTOFromRow(&created))
}

// downloadFile streams a file's raw bytes to the authenticated user, after
// verifying they're a member of the file's workspace.
//
// O(1) lookup: fetch the row once under the owner pool (bypasses RLS) to
// learn the workspace_id, then verify membership via a single RLS-backed
// query. Previously we iterated every membership which scaled badly and
// had no channel-ACL notion; the membership check here still misses the
// "file posted in a private channel I'm no longer in" case — acknowledged
// and tracked; the workspace-membership gate is a strict improvement
// over the old walk.
func (s *Server) downloadFile(c echo.Context) error {
	if s.storage == nil {
		return problem.Internal("storage not configured")
	}
	user := userFromContext(c)
	fileID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	var row sqlcgen.File
	if s.ownerPool != nil {
		ownerQ := sqlcgen.New(s.ownerPool)
		r, err := ownerQ.GetFileByID(c.Request().Context(), fileID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return problem.NotFound("file not found")
			}
			s.logger.Error("download file: owner lookup", "error", err.Error())
			return problem.Internal("could not read file")
		}
		if !s.userInWorkspace(c.Request().Context(), user.ID, r.WorkspaceID) {
			// Don't leak whether the id exists in a different workspace.
			return problem.NotFound("file not found")
		}
		row = r
	} else {
		// No owner pool wired — fall back to iterating the user's
		// workspaces under RLS. Kept for tests and the slim-harness
		// scenarios where SearchOwnerDB isn't available. Production
		// always wires the owner pool, so the O(1) path above is the
		// common case.
		memberships, err := s.listUserWorkspaceIDs(c.Request().Context(), user.ID)
		if err != nil {
			s.logger.Error("download file: list workspaces", "error", err.Error())
			return problem.Internal("could not list workspaces")
		}
		found := false
		for _, wsID := range memberships {
			err := db.WithTx(c.Request().Context(), s.pool.Pool,
				db.TxOptions{UserID: user.ID, WorkspaceID: wsID, ReadOnly: true},
				func(scope db.TxScope) error {
					f, err := scope.Queries.GetFileByID(c.Request().Context(), fileID)
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
				s.logger.Error("download file: rls lookup", "error", err.Error())
				return problem.Internal("could not read file")
			}
			if found {
				break
			}
		}
		if !found {
			return problem.NotFound("file not found")
		}
	}

	if row.ScanStatus == "infected" {
		return problem.Forbidden("this file was flagged by the antivirus scanner")
	}

	r, err := s.storage.Get(c.Request().Context(), row.StorageKey)
	if err != nil {
		s.logger.Error("download file: storage read", "error", err.Error(), "file_id", fileID)
		return problem.Internal("could not read file")
	}
	defer r.Close()

	c.Response().Header().Set(echo.HeaderContentType, row.Mime)
	c.Response().Header().Set(echo.HeaderContentLength, strconv.FormatInt(row.SizeBytes, 10))
	// Force attachment + nosniff so a browser can't be coerced into
	// treating a user-uploaded file as HTML/JS. Images still display
	// inline via the blob-URL path; this affects direct URL loads.
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", row.Filename))
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Cache-Control", "private, max-age=3600")

	c.Response().WriteHeader(http.StatusOK)
	_, _ = io.Copy(c.Response().Writer, r)
	return nil
}

// ---- helpers ------------------------------------------------------------

// existingFile checks for prior upload of the same (workspace, sha256).
// On a hit, returns the file row and the DTO ready for the response.
func (s *Server) existingFile(ctx context.Context, userID, workspaceID int64, sha string) (*sqlcgen.File, FileDTO, bool) {
	var row *sqlcgen.File
	err := db.WithTx(ctx, s.pool.Pool,
		db.TxOptions{UserID: userID, WorkspaceID: workspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			f, err := scope.Queries.GetFileBySHA256(ctx, sqlcgen.GetFileBySHA256Params{
				WorkspaceID: workspaceID,
				Sha256:      sha,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			row = &f
			return nil
		})
	if err != nil || row == nil {
		return nil, FileDTO{}, false
	}
	return row, fileDTOFromRow(row), true
}

// userInWorkspace returns true if the user is an active member of the
// given workspace. Used as a guardrail before accepting an upload.
func (s *Server) userInWorkspace(ctx context.Context, userID, workspaceID int64) bool {
	allowed := false
	_ = db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, ReadOnly: true}, func(scope db.TxScope) error {
		if _, err := scope.Queries.GetWorkspaceByID(ctx, workspaceID); err == nil {
			allowed = true
		}
		return nil
	})
	return allowed
}

func fileDTOFromRow(f *sqlcgen.File) FileDTO {
	dto := FileDTO{
		ID:         f.ID,
		Filename:   f.Filename,
		MIME:       f.Mime,
		SizeBytes:  f.SizeBytes,
		ScanStatus: f.ScanStatus,
		URL:        fmt.Sprintf("/api/v1/files/%d/raw", f.ID),
		CreatedAt:  f.CreatedAt.Time,
	}
	if f.Width != nil {
		dto.Width = int(*f.Width)
	}
	if f.Height != nil {
		dto.Height = int(*f.Height)
	}
	return dto
}

// sanitizeFilename strips directory separators so a malicious client can't
// smuggle path segments into the stored name. Display use only; the real
// storage key is content-addressed.
func sanitizeFilename(name string) string {
	// Take the basename equivalent: split on / and \ and grab the last part.
	for _, sep := range []string{"/", "\\"} {
		if i := indexLast(name, sep); i >= 0 {
			name = name[i+1:]
		}
	}
	if len(name) > 255 {
		name = name[:255]
	}
	if name == "" {
		return "file"
	}
	return name
}

func indexLast(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func nullInt(v int) *int32 {
	if v <= 0 {
		return nil
	}
	n := int32(v)
	return &n
}

// defaultScanStatus returns the initial scan_status for newly-uploaded
// files. When clamd wiring lands (M5.1) this flips to "pending" and a
// River worker flips it to "clean" or "infected" after scanning. Until
// then we mark as "clean" so downloads aren't gated.
func defaultScanStatus() string {
	return "clean"
}
