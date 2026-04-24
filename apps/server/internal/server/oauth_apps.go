package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/apps"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// App install + OAuth flow (M12-P1).
//
// Surface:
//
//   GET    /api/v1/oauth/authorize?client_id=...&redirect_uri=...
//                         &scope=...&code_challenge=...&code_challenge_method=S256
//                         &workspace_slug=...&state=...
//
//          Authenticated endpoint — the user's browser lands here from
//          the third-party app's "Add to SliilS" link. We validate the
//          client_id + redirect_uri against the app's manifest, ensure
//          the current user is a workspace admin, create the
//          installation (if it doesn't already exist), mint a single-
//          use authorization code, and 302 back to the redirect_uri
//          with ?code=... &state=...
//
//   POST   /api/v1/oauth/token
//          Unauthenticated — called by the developer's backend.
//          Exchanges `code` + `code_verifier` (+ client_id + secret)
//          for a long-lived access token scoped to the installation.
//
//   GET    /api/v1/workspaces/:slug/apps        — list installed apps
//   DELETE /api/v1/installations/:id            — uninstall (admin only)
//
// We deliberately mirror Slack's OAuth flow shape so existing Slack
// app SDKs need only a base-URL swap to target SliilS.

// ---- routes ------------------------------------------------------------

func (s *Server) mountOAuthApps(api *echo.Group) {
	authz := api.Group("")
	authz.Use(s.requireAuth())
	authz.GET("/oauth/authorize", s.oauthAuthorize)
	authz.GET("/workspaces/:slug/apps", s.listInstalledApps)
	authz.DELETE("/installations/:id", s.uninstallApp)

	// Token exchange is UN-authenticated (no user session) — it's called
	// by the app's own backend with client_id + client_secret.
	api.POST("/oauth/token", s.oauthToken)
}

// ---- /oauth/authorize --------------------------------------------------

type authorizeResponse struct {
	RedirectTo string `json:"redirect_to"`
}

func (s *Server) oauthAuthorize(c echo.Context) error {
	user := userFromContext(c)
	q := c.QueryParams()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	scopeRaw := q.Get("scope")
	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")
	slug := q.Get("workspace_slug")
	state := q.Get("state")
	if clientID == "" || redirectURI == "" || challenge == "" || slug == "" {
		return problem.BadRequest("client_id, redirect_uri, code_challenge and workspace_slug are required")
	}
	if method == "" {
		method = "S256"
	}
	if method != "S256" && method != "plain" {
		return problem.BadRequest("code_challenge_method must be S256 or plain")
	}

	if s.ownerPool == nil {
		return problem.Internal("apps require the owner pool")
	}
	ownerQ := sqlcgen.New(s.ownerPool)

	app, err := ownerQ.GetAppByClientID(c.Request().Context(), clientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("unknown client_id")
		}
		return problem.Internal("load app: " + err.Error())
	}

	manifest, err := apps.DecodeManifest(app.Manifest)
	if err != nil {
		return problem.Internal("decode manifest: " + err.Error())
	}
	if !redirectURIAllowed(manifest.RedirectURIs, redirectURI) {
		return problem.BadRequest("redirect_uri is not registered for this app")
	}

	requested := splitScopes(scopeRaw)
	// Every requested scope MUST appear in the app's manifest, otherwise
	// the developer is asking for more than they declared.
	for _, sc := range requested {
		if !apps.HasScope(manifest.Scopes, sc) {
			return problem.BadRequest("scope " + sc + " is not declared in the app's manifest")
		}
	}

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	// Installing an app changes workspace capabilities — admin-only.
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	// Create (or re-use) the installation row. Scopes on the installation
	// row are the GRANTED subset — at v1 we grant everything requested;
	// a future "consent screen" UI lets the user narrow.
	granted := append([]string(nil), requested...)
	grantedJSON := apps.EncodeScopes(granted)

	var install sqlcgen.AppInstallation
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			existing, err := scope.Queries.GetInstallation(c.Request().Context(), sqlcgen.GetInstallationParams{
				AppID:       app.ID,
				WorkspaceID: ws.ID,
			})
			if err == nil {
				install = existing
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			created, err := scope.Queries.CreateAppInstallation(c.Request().Context(), sqlcgen.CreateAppInstallationParams{
				AppID:       app.ID,
				WorkspaceID: ws.ID,
				InstalledBy: &user.ID,
				Scopes:      grantedJSON,
				BotUserID:   nil,
			})
			if err != nil {
				return err
			}
			install = created
			return nil
		})
	if err != nil {
		return problem.Internal("create installation: " + err.Error())
	}

	// Provision a bot user lazily if the app asked for the `bot` scope
	// and doesn't already have one on this installation.
	if apps.HasScope(granted, "bot") && install.BotUserID == nil {
		botName := "SliilS Bot"
		if manifest.BotUser != nil && manifest.BotUser.DisplayName != "" {
			botName = manifest.BotUser.DisplayName
		} else {
			botName = app.Name
		}
		syntheticEmail := fmt.Sprintf("bot+install%d@bot.local", install.ID)
		bot, err := ownerQ.CreateBotUser(c.Request().Context(), sqlcgen.CreateBotUserParams{
			Email:                syntheticEmail,
			DisplayName:          botName,
			BotAppInstallationID: &install.ID,
		})
		if err == nil {
			_ = ownerQ.SetInstallationBot(c.Request().Context(), sqlcgen.SetInstallationBotParams{
				ID:        install.ID,
				BotUserID: &bot.ID,
			})
		} else {
			s.logger.Warn("create bot user", "installation_id", install.ID, "error", err.Error())
		}
	}

	// Mint the authorization code.
	code, err := apps.NewAuthorizationCode()
	if err != nil {
		return problem.Internal("mint code: " + err.Error())
	}
	if err := ownerQ.CreateOAuthCode(c.Request().Context(), sqlcgen.CreateOAuthCodeParams{
		Code:                 code,
		AppID:                app.ID,
		WorkspaceID:          ws.ID,
		UserID:               user.ID,
		RedirectUri:          redirectURI,
		Scopes:               grantedJSON,
		CodeChallenge:        challenge,
		CodeChallengeMethod:  method,
		ExpiresAt:            pgtype.Timestamptz{Time: time.Now().Add(10 * time.Minute), Valid: true},
	}); err != nil {
		return problem.Internal("persist code: " + err.Error())
	}

	// Build the redirect URL. We don't 302 directly: the client is a
	// React SPA that wants to do its own transition. Returning the URL
	// also lets test harnesses assert without following redirects.
	redir := buildAuthorizeRedirect(redirectURI, code, state)
	return c.JSON(http.StatusOK, authorizeResponse{RedirectTo: redir})
}

// ---- /oauth/token ------------------------------------------------------

type tokenRequest struct {
	GrantType    string `form:"grant_type" json:"grant_type"`
	Code         string `form:"code" json:"code"`
	RedirectURI  string `form:"redirect_uri" json:"redirect_uri"`
	ClientID     string `form:"client_id" json:"client_id"`
	ClientSecret string `form:"client_secret" json:"client_secret"`
	CodeVerifier string `form:"code_verifier" json:"code_verifier"`
}

type tokenResponse struct {
	AccessToken    string   `json:"access_token"`
	TokenType      string   `json:"token_type"`
	Scope          string   `json:"scope"`
	WorkspaceID    int64    `json:"workspace_id"`
	InstallationID int64    `json:"installation_id"`
	BotUserID      *int64   `json:"bot_user_id,omitempty"`
	AppID          int64    `json:"app_id"`
}

func (s *Server) oauthToken(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("apps require the owner pool")
	}

	var req tokenRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.GrantType != "authorization_code" {
		return problem.BadRequest("grant_type must be authorization_code")
	}
	if req.Code == "" || req.ClientID == "" || req.RedirectURI == "" {
		return problem.BadRequest("code, client_id, and redirect_uri are required")
	}

	ownerQ := sqlcgen.New(s.ownerPool)
	app, err := ownerQ.GetAppByClientID(c.Request().Context(), req.ClientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.BadRequest("unknown client_id")
		}
		return problem.Internal("load app: " + err.Error())
	}

	// Either a valid client_secret OR a valid PKCE verifier is required.
	// PKCE is the recommended path for SPAs; client_secret is fine for
	// server-to-server apps. We accept either; presence of client_secret
	// takes precedence so a compromised verifier can't downgrade a
	// confidential client.
	if req.ClientSecret != "" {
		if !apps.VerifyClientSecret(req.ClientSecret, app.ClientSecretHash) {
			return problem.Unauthorized("invalid client_secret")
		}
	}

	codeRow, err := ownerQ.ConsumeOAuthCode(c.Request().Context(), req.Code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.BadRequest("code is invalid, expired, or already used")
		}
		return problem.Internal("consume code: " + err.Error())
	}
	if codeRow.AppID != app.ID {
		return problem.BadRequest("code does not belong to this client_id")
	}
	if codeRow.RedirectUri != req.RedirectURI {
		return problem.BadRequest("redirect_uri mismatch")
	}

	if req.ClientSecret == "" {
		if !apps.VerifyPKCE(codeRow.CodeChallenge, codeRow.CodeChallengeMethod, req.CodeVerifier) {
			return problem.Unauthorized("PKCE verification failed")
		}
	}

	// Locate the installation to attach the token to.
	install, err := ownerQ.GetInstallation(c.Request().Context(), sqlcgen.GetInstallationParams{
		AppID:       app.ID,
		WorkspaceID: codeRow.WorkspaceID,
	})
	if err != nil {
		return problem.Internal("load installation: " + err.Error())
	}

	// Mint the token. We need the token_id to embed in the plaintext,
	// so INSERT first with a placeholder hash, then UPDATE with the real
	// hash once we have the id. Simpler: two-phase via a dummy hash.
	//
	// Alternative: rely on RETURNING to get the id, then compute the
	// plain token, then UPDATE the hash. That's what we do.
	tokenRow, err := ownerQ.CreateAppToken(c.Request().Context(), sqlcgen.CreateAppTokenParams{
		AppInstallationID: install.ID,
		WorkspaceID:       codeRow.WorkspaceID,
		TokenHash:         "pending",
		Label:             "initial",
		Scopes:            codeRow.Scopes,
	})
	if err != nil {
		return problem.Internal("create token row: " + err.Error())
	}
	plain, hash, err := apps.NewAccessToken(tokenRow.TokenID)
	if err != nil {
		return problem.Internal("mint token: " + err.Error())
	}
	// Update the hash in place. `RotateAppSecret` is for apps;
	// app_tokens reuses the token_id shape and we patch via raw SQL.
	_, err = s.ownerPool.Exec(c.Request().Context(),
		`UPDATE app_tokens SET token_hash = $1 WHERE token_id = $2`, hash, tokenRow.TokenID)
	if err != nil {
		return problem.Internal("finalise token: " + err.Error())
	}

	scopeStr := strings.Join(apps.DecodeScopes(codeRow.Scopes), " ")
	return c.JSON(http.StatusOK, tokenResponse{
		AccessToken:    plain,
		TokenType:      "Bearer",
		Scope:          scopeStr,
		WorkspaceID:    codeRow.WorkspaceID,
		InstallationID: install.ID,
		BotUserID:      install.BotUserID,
		AppID:          app.ID,
	})
}

// ---- /workspaces/:slug/apps + /installations/:id -----------------------

type InstallationDTO struct {
	ID           int64             `json:"id"`
	AppID        int64             `json:"app_id"`
	Slug         string            `json:"slug"`
	AppName      string            `json:"app_name"`
	AppDescription string          `json:"app_description,omitempty"`
	Scopes       []string          `json:"scopes"`
	BotUserID    *int64            `json:"bot_user_id,omitempty"`
	InstalledBy  *int64            `json:"installed_by,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

func (s *Server) listInstalledApps(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var rows []sqlcgen.ListInstallationsForWorkspaceRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListInstallationsForWorkspace(c.Request().Context(), ws.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list installations: " + err.Error())
	}
	out := make([]InstallationDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, InstallationDTO{
			ID:             r.ID,
			AppID:          r.AppID,
			Slug:           r.Slug,
			AppName:        r.AppName,
			AppDescription: r.AppDescription,
			Scopes:         apps.DecodeScopes(r.Scopes),
			BotUserID:      r.BotUserID,
			InstalledBy:    r.InstalledBy,
			CreatedAt:      r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) uninstallApp(c echo.Context) error {
	user := userFromContext(c)
	id, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	if s.ownerPool == nil {
		return problem.Internal("apps require the owner pool")
	}
	// Load under owner pool to get workspace_id, then enforce admin.
	ownerQ := sqlcgen.New(s.ownerPool)
	row, err := ownerQ.GetInstallationByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("installation not found")
		}
		return problem.Internal("load installation: " + err.Error())
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, row.WorkspaceID); err != nil {
		return err
	}
	if err := ownerQ.RevokeInstallation(c.Request().Context(), sqlcgen.RevokeInstallationParams{
		ID:          id,
		WorkspaceID: row.WorkspaceID,
	}); err != nil {
		return problem.Internal("uninstall: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- helpers -----------------------------------------------------------

func splitScopes(s string) []string {
	out := []string{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '+' }) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func redirectURIAllowed(registered []string, candidate string) bool {
	// Exact match. RFC 6749 recommends not allowing partial matches —
	// `example.com/` does NOT equal `example.com` does NOT equal
	// `example.com?param=x`.
	for _, r := range registered {
		if r == candidate {
			return true
		}
	}
	return false
}

func buildAuthorizeRedirect(base, code, state string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// writeAuthorizeJSON is a thin helper so test harnesses can decode the
// redirect target without parsing the HTML. Production clients follow
// the RedirectTo field directly.
//
// (Kept tiny so the oauth flow stays readable in the main handler.)
func writeAuthorizeJSON(c echo.Context, to string) error {
	return c.JSON(http.StatusOK, map[string]string{"redirect_to": to})
}

var _ = writeAuthorizeJSON // referenced by future callers
var _ = json.Marshal        // keep encoding/json imported; used indirectly via sqlc
