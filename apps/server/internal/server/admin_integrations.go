package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/push"
)

// Admin Integrations tab (M12-polish).
//
// Workspace owners/admins configure their own outbound-email provider
// here so invites from this workspace go out as `no-reply@theirdomain`
// instead of the install operator's address. The install_settings
// operator-level config is the fallback when a workspace doesn't set
// its own.
//
// Surface:
//
//   GET    /workspaces/:slug/admin/integrations/email
//          Returns the current workspace email config (api key omitted
//          — API keys are write-only).
//
//   PATCH  /workspaces/:slug/admin/integrations/email
//          Upserts the workspace email config. Pass an empty
//          "resend_api_key" to keep the existing secret while changing
//          from_address / from_name.
//
//   POST   /workspaces/:slug/admin/integrations/email/test
//          Attempts a test send to the caller's own email using the
//          currently-stored workspace config. Returns the delivery
//          outcome so the admin can verify before sending real invites.

type workspaceEmailDTO struct {
	Provider    string `json:"provider"`
	FromAddress string `json:"from_address,omitempty"`
	FromName    string `json:"from_name,omitempty"`
	APIKeyIsSet bool   `json:"api_key_is_set"`
}

type patchWorkspaceEmailRequest struct {
	Provider     string `json:"provider,omitempty"`       // "resend" — only one at v1
	ResendAPIKey string `json:"resend_api_key,omitempty"` // empty = keep existing
	FromAddress  string `json:"from_address,omitempty"`
	FromName     string `json:"from_name,omitempty"`
}

type emailTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) mountAdminIntegrations(api *echo.Group) {
	g := api.Group("/workspaces/:slug/admin/integrations")
	g.Use(s.requireAuth())
	g.GET("/email", s.getWorkspaceEmailIntegration)
	g.PATCH("/email", s.patchWorkspaceEmailIntegration)
	g.POST("/email/test", s.testWorkspaceEmailIntegration)

	// /install/status is PUBLIC — the signup page consults it to decide
	// whether to show the registration form. Reveals only policy flags,
	// no secrets.
	api.GET("/install/status", s.getInstallStatus)

	// Everything else under /install is super-admin-only. A workspace
	// owner on some tenant cannot change install-level policy for
	// tenants they don't control.
	inst := api.Group("/install")
	inst.Use(s.requireAuth())
	inst.Use(s.requireSuperAdmin())
	inst.GET("/signup-mode", s.getInstallSignupMode)
	inst.PATCH("/signup-mode", s.patchInstallSignupMode)
	inst.GET("/email", s.getInstallEmail)
	inst.PATCH("/email", s.patchInstallEmail)
	inst.GET("/infrastructure", s.getInstallInfrastructure)
	inst.PATCH("/infrastructure", s.patchInstallInfrastructure)
	inst.POST("/vapid/generate", s.generateVAPIDKeys)
	// Super-admin management: list current super-admins so an operator
	// can promote a backup before demoting themselves. The demote path
	// refuses to remove the last active super-admin so the install
	// cannot be locked out of /install/* by a single mis-click.
	inst.GET("/super-admins", s.listSuperAdmins)
	inst.POST("/super-admins/:uid/promote", s.promoteSuperAdmin)
	inst.POST("/super-admins/:uid/demote", s.demoteSuperAdmin)

	// Restart flow: the infra PATCH handler flags install_settings with
	// a restart_required_at timestamp; the UI polls /restart-status to
	// show a banner, and /restart fires the main-loop shutdown hook so
	// systemd/docker can bring the process back with the new config.
	inst.GET("/restart-status", s.getRestartStatus)
	inst.POST("/restart", s.postRestart)
}

// ---- install-level -----------------------------------------------------

type installStatusDTO struct {
	SetupCompleted bool   `json:"setup_completed"`
	SignupMode     string `json:"signup_mode"` // "open" | "invite_only"
}

// getInstallStatus is unauthenticated — the signup/signin pages need
// it to decide whether to show the "Create account" button. It reveals
// only policy flags; no secrets.
func (s *Server) getInstallStatus(c echo.Context) error {
	if s.installSvc == nil {
		return c.JSON(http.StatusOK, installStatusDTO{
			SetupCompleted: true,
			SignupMode:     install.SignupInviteOnly,
		})
	}
	return c.JSON(http.StatusOK, installStatusDTO{
		SetupCompleted: s.installSvc.SetupCompleted(c.Request().Context()),
		SignupMode:     s.installSvc.SignupMode(c.Request().Context()),
	})
}

type signupModeDTO struct {
	SignupMode string `json:"signup_mode"`
}

func (s *Server) getInstallSignupMode(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	return c.JSON(http.StatusOK, signupModeDTO{
		SignupMode: s.installSvc.SignupMode(c.Request().Context()),
	})
}

type patchSignupModeRequest struct {
	SignupMode string `json:"signup_mode"`
}

func (s *Server) patchInstallSignupMode(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	var req patchSignupModeRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	switch req.SignupMode {
	case install.SignupOpen, install.SignupInviteOnly:
	default:
		return problem.BadRequest("signup_mode must be 'open' or 'invite_only'")
	}
	if err := s.installSvc.Set(c.Request().Context(), install.KeySignupMode, req.SignupMode, false, &actor.ID); err != nil {
		return problem.Internal("persist signup mode: " + err.Error())
	}
	// Flag first-run wizard as done once an admin has made the call.
	_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallSetupCompleted, "true", false, &actor.ID)
	s.recordAudit(c, 0, actor.ID, "install.signup_mode_updated", "install", nil, map[string]any{
		"signup_mode": req.SignupMode,
	})
	return c.JSON(http.StatusOK, signupModeDTO{SignupMode: req.SignupMode})
}

func (s *Server) getWorkspaceEmailIntegration(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	cfg, err := s.installSvc.GetWorkspaceEmail(c.Request().Context(), ws.ID)
	if err != nil {
		return problem.Internal("get workspace email: " + err.Error())
	}
	return c.JSON(http.StatusOK, workspaceEmailDTO{
		Provider:    firstNonEmptyStr(cfg.Provider, "resend"),
		FromAddress: cfg.FromAddress,
		FromName:    cfg.FromName,
		APIKeyIsSet: cfg.APIKeyIsSet,
	})
}

func (s *Server) patchWorkspaceEmailIntegration(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	var req patchWorkspaceEmailRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = "resend"
	}
	if provider != "resend" {
		return problem.BadRequest("only the resend provider is supported at v1")
	}
	if req.FromAddress != "" {
		if !strings.Contains(req.FromAddress, "@") {
			return problem.BadRequest("from_address must be an email address")
		}
	}

	cfg, err := s.installSvc.SetWorkspaceEmail(
		c.Request().Context(),
		ws.ID,
		provider,
		strings.TrimSpace(req.ResendAPIKey),
		strings.TrimSpace(req.FromAddress),
		strings.TrimSpace(req.FromName),
		&actor.ID,
	)
	if err != nil {
		return problem.BadRequest(err.Error())
	}
	s.recordAudit(c, ws.ID, actor.ID, "workspace.email_updated", "workspace", &ws.ID, map[string]any{
		"provider":     provider,
		"from_address": req.FromAddress,
		"api_key_set":  req.ResendAPIKey != "",
	})
	return c.JSON(http.StatusOK, workspaceEmailDTO{
		Provider:    cfg.Provider,
		FromAddress: cfg.FromAddress,
		FromName:    cfg.FromName,
		APIKeyIsSet: cfg.APIKeyIsSet,
	})
}

func (s *Server) testWorkspaceEmailIntegration(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	sender, err := s.resolveEmailSenderForWorkspace(c.Request().Context(), ws.ID)
	if err != nil {
		return c.JSON(http.StatusOK, emailTestResponse{OK: false, Error: err.Error()})
	}
	if sender == nil {
		return c.JSON(http.StatusOK, emailTestResponse{
			OK:    false,
			Error: "no email provider is configured — add a Resend API key + from address",
		})
	}
	msg := email.Message{
		To:       []string{actor.Email},
		Subject:  "SliilS email test",
		HTMLBody: "<p>This is a test email from your workspace's email configuration.</p><p>If you received this, outbound email is working correctly.</p>",
		TextBody: "This is a test email from your workspace's email configuration. If you received this, outbound email is working correctly.",
	}
	if err := sender.Send(c.Request().Context(), msg); err != nil {
		return c.JSON(http.StatusOK, emailTestResponse{OK: false, Error: err.Error()})
	}
	return c.JSON(http.StatusOK, emailTestResponse{OK: true})
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- install-level email (auth flows) ---------------------------------

type installEmailDTO struct {
	Provider    string `json:"provider"`
	FromAddress string `json:"from_address,omitempty"`
	FromName    string `json:"from_name,omitempty"`
	APIKeyIsSet bool   `json:"api_key_is_set"`
}

type patchInstallEmailRequest struct {
	Provider     string `json:"provider,omitempty"`
	ResendAPIKey string `json:"resend_api_key,omitempty"` // empty = keep
	FromAddress  string `json:"from_address,omitempty"`
	FromName     string `json:"from_name,omitempty"`
}

func (s *Server) getInstallEmail(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	ctx := c.Request().Context()
	provider, _ := s.installSvc.Get(ctx, install.KeyInstallEmailProvider)
	apiKey, _ := s.installSvc.Get(ctx, install.KeyInstallResendAPIKey)
	fromAddr, _ := s.installSvc.Get(ctx, install.KeyInstallEmailFrom)
	fromName, _ := s.installSvc.Get(ctx, install.KeyInstallEmailFromName)
	return c.JSON(http.StatusOK, installEmailDTO{
		Provider:    firstNonEmptyStr(provider, "resend"),
		FromAddress: fromAddr,
		FromName:    fromName,
		APIKeyIsSet: apiKey != "",
	})
}

func (s *Server) patchInstallEmail(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	var req patchInstallEmailRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = "resend"
	}
	ctx := c.Request().Context()
	if err := s.installSvc.Set(ctx, install.KeyInstallEmailProvider, provider, false, &actor.ID); err != nil {
		return problem.Internal(err.Error())
	}
	if strings.TrimSpace(req.ResendAPIKey) != "" {
		if err := s.installSvc.Set(ctx, install.KeyInstallResendAPIKey, strings.TrimSpace(req.ResendAPIKey), true, &actor.ID); err != nil {
			return problem.BadRequest(err.Error())
		}
	}
	if err := s.installSvc.Set(ctx, install.KeyInstallEmailFrom, strings.TrimSpace(req.FromAddress), false, &actor.ID); err != nil {
		return problem.Internal(err.Error())
	}
	if err := s.installSvc.Set(ctx, install.KeyInstallEmailFromName, strings.TrimSpace(req.FromName), false, &actor.ID); err != nil {
		return problem.Internal(err.Error())
	}
	s.recordAudit(c, 0, actor.ID, "install.email_updated", "install", nil, map[string]any{
		"from_address": req.FromAddress,
		"api_key_set":  req.ResendAPIKey != "",
	})
	return s.getInstallEmail(c)
}

// ---- install-level infrastructure -------------------------------------

// infraDTO bundles every infrastructure endpoint the admin can edit.
// Secret fields (VAPID private key, LiveKit secret, Y-Sweet server
// token) are never returned — the UI only sees an is_set flag.
type infraDTO struct {
	VAPIDPublicKey     string `json:"vapid_public_key,omitempty"`
	VAPIDPrivateKeySet bool   `json:"vapid_private_key_set"`
	VAPIDSubject       string `json:"vapid_subject,omitempty"`
	CollaboraURL       string `json:"collabora_url,omitempty"`
	YSweetURL          string `json:"ysweet_url,omitempty"`
	YSweetTokenSet     bool   `json:"ysweet_server_token_set"`
	LiveKitURL         string `json:"livekit_url,omitempty"`
	LiveKitWSURL       string `json:"livekit_ws_url,omitempty"`
	LiveKitAPIKey      string `json:"livekit_api_key,omitempty"`
	LiveKitSecretSet   bool   `json:"livekit_api_secret_set"`
}

type patchInfraRequest struct {
	VAPIDPublicKey  *string `json:"vapid_public_key,omitempty"`
	VAPIDPrivateKey *string `json:"vapid_private_key,omitempty"` // empty = keep
	VAPIDSubject    *string `json:"vapid_subject,omitempty"`
	CollaboraURL    *string `json:"collabora_url,omitempty"`
	YSweetURL       *string `json:"ysweet_url,omitempty"`
	YSweetToken     *string `json:"ysweet_server_token,omitempty"` // empty = keep
	LiveKitURL      *string `json:"livekit_url,omitempty"`
	LiveKitWSURL    *string `json:"livekit_ws_url,omitempty"`
	LiveKitAPIKey   *string `json:"livekit_api_key,omitempty"`
	LiveKitSecret   *string `json:"livekit_api_secret,omitempty"` // empty = keep
}

func (s *Server) getInstallInfrastructure(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	ctx := c.Request().Context()
	dto := infraDTO{}
	dto.VAPIDPublicKey, _ = s.installSvc.Get(ctx, install.KeyVAPIDPublicKey)
	dto.VAPIDSubject, _ = s.installSvc.Get(ctx, install.KeyVAPIDSubject)
	priv, _ := s.installSvc.Get(ctx, install.KeyVAPIDPrivateKey)
	dto.VAPIDPrivateKeySet = priv != ""
	dto.CollaboraURL, _ = s.installSvc.Get(ctx, install.KeyCollaboraURL)
	dto.YSweetURL, _ = s.installSvc.Get(ctx, install.KeyYSweetURL)
	ysweetTok, _ := s.installSvc.Get(ctx, install.KeyYSweetServerToken)
	dto.YSweetTokenSet = ysweetTok != ""
	dto.LiveKitURL, _ = s.installSvc.Get(ctx, install.KeyLiveKitURL)
	dto.LiveKitWSURL, _ = s.installSvc.Get(ctx, install.KeyLiveKitWSURL)
	dto.LiveKitAPIKey, _ = s.installSvc.Get(ctx, install.KeyLiveKitAPIKey)
	lkSecret, _ := s.installSvc.Get(ctx, install.KeyLiveKitAPISecret)
	dto.LiveKitSecretSet = lkSecret != ""

	// Fall back to runtime config values for display when DB is empty
	// — the admin sees what they'd be replacing.
	if dto.VAPIDPublicKey == "" {
		dto.VAPIDPublicKey = s.cfg.VAPIDPublicKey
	}
	if dto.VAPIDSubject == "" {
		dto.VAPIDSubject = s.cfg.VAPIDSubject
	}
	if dto.CollaboraURL == "" {
		dto.CollaboraURL = s.cfg.CollaboraURL
	}
	if dto.YSweetURL == "" {
		dto.YSweetURL = s.cfg.YSweetURL
	}
	if dto.LiveKitURL == "" {
		dto.LiveKitURL = s.cfg.LiveKitURL
	}
	if dto.LiveKitWSURL == "" {
		dto.LiveKitWSURL = s.cfg.LiveKitWSURL
	}
	if dto.LiveKitAPIKey == "" {
		dto.LiveKitAPIKey = s.cfg.LiveKitAPIKey
	}
	return c.JSON(http.StatusOK, dto)
}

func (s *Server) patchInstallInfrastructure(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	actor := userFromContext(c)
	var req patchInfraRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	ctx := c.Request().Context()
	set := func(key string, ptr *string, encrypted bool) error {
		if ptr == nil {
			return nil
		}
		val := strings.TrimSpace(*ptr)
		if encrypted && val == "" {
			// Empty encrypted field = preserve existing secret.
			return nil
		}
		return s.installSvc.Set(ctx, key, val, encrypted, &actor.ID)
	}
	changed := false
	for _, p := range []struct {
		key string
		v   *string
		enc bool
	}{
		{install.KeyVAPIDPublicKey, req.VAPIDPublicKey, false},
		{install.KeyVAPIDPrivateKey, req.VAPIDPrivateKey, true},
		{install.KeyVAPIDSubject, req.VAPIDSubject, false},
		{install.KeyCollaboraURL, req.CollaboraURL, false},
		{install.KeyYSweetURL, req.YSweetURL, false},
		{install.KeyYSweetServerToken, req.YSweetToken, true},
		{install.KeyLiveKitURL, req.LiveKitURL, false},
		{install.KeyLiveKitWSURL, req.LiveKitWSURL, false},
		{install.KeyLiveKitAPIKey, req.LiveKitAPIKey, false},
		{install.KeyLiveKitAPISecret, req.LiveKitSecret, true},
	} {
		if p.v == nil {
			continue
		}
		if err := set(p.key, p.v, p.enc); err != nil {
			return problem.BadRequest(err.Error())
		}
		changed = true
	}
	if changed {
		// Services that read these (push, livekit, y-sweet, collabora)
		// are constructed at boot. Flag the install so the UI can
		// surface a restart banner. Cleared by main.go on startup.
		if err := s.installSvc.Set(ctx, install.KeyRestartRequiredAt,
			time.Now().UTC().Format(time.RFC3339), false, &actor.ID); err != nil {
			s.logger.Error("flag restart required", "error", err.Error())
		}
	}
	s.recordAudit(c, 0, actor.ID, "install.infrastructure_updated", "install", nil, nil)
	return s.getInstallInfrastructure(c)
}

// generateVAPIDKeys mints a fresh P-256 VAPID keypair and returns it
// to the caller — it does NOT auto-save. The admin reviews + saves
// through the normal PATCH, so they can also copy the key to any
// dependent clients (Tauri native push in the future, etc.) before
// committing.
func (s *Server) generateVAPIDKeys(c echo.Context) error {
	priv, pub, err := push.GenerateVAPIDKeys()
	if err != nil {
		s.logger.Error("generate vapid keypair", "error", err.Error())
		return problem.Internal("could not generate keypair")
	}
	actor := userFromContext(c)
	if s.auditor != nil {
		s.auditor.Record(c.Request().Context(), audit.Event{
			ActorUserID: &actor.ID,
			ActorIP:     clientIP(c),
			Action:      "install.vapid_generated",
			TargetKind:  "install",
		})
	}
	return c.JSON(http.StatusOK, map[string]string{
		"public_key":  pub,
		"private_key": priv,
	})
}

// ---- super-admin management -------------------------------------------

type superAdminDTO struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) listSuperAdmins(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("super-admin listing requires the owner pool")
	}
	q := sqlcgen.New(s.ownerPool)
	rows, err := q.ListActiveSuperAdmins(c.Request().Context())
	if err != nil {
		s.logger.Error("list super admins", "error", err.Error())
		return problem.Internal("could not list super-admins")
	}
	out := make([]superAdminDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, superAdminDTO{
			ID:          r.ID,
			Email:       r.Email,
			DisplayName: r.DisplayName,
			CreatedAt:   r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) promoteSuperAdmin(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("super-admin promotion requires the owner pool")
	}
	target, err := parseInt64Param(c, "uid")
	if err != nil {
		return err
	}
	actor := userFromContext(c)
	q := sqlcgen.New(s.ownerPool)
	if err := q.PromoteToSuperAdmin(c.Request().Context(), target); err != nil {
		s.logger.Error("promote super admin", "error", err.Error(), "target", target)
		return problem.Internal("could not promote user")
	}
	s.recordAudit(c, 0, actor.ID, "install.super_admin_promoted", "user", &target, nil)
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) demoteSuperAdmin(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("super-admin demotion requires the owner pool")
	}
	target, err := parseInt64Param(c, "uid")
	if err != nil {
		return err
	}
	actor := userFromContext(c)

	// Re-check within a transaction so two concurrent demotes can't both
	// see count==2 and both succeed, leaving zero. pg_advisory_xact_lock
	// serialises them; the count-then-update is safe under SERIALIZABLE.
	ctx := c.Request().Context()
	tx, err := s.ownerPool.Begin(ctx)
	if err != nil {
		s.logger.Error("demote super admin: begin tx", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", firstRunBootstrapLockID); err != nil {
		s.logger.Error("demote super admin: lock", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	txQ := sqlcgen.New(tx)
	n, err := txQ.CountActiveSuperAdmins(ctx)
	if err != nil {
		s.logger.Error("demote super admin: count", "error", err.Error())
		return problem.Internal("could not read super-admin count")
	}
	if n <= 1 {
		return problem.Conflict("cannot demote the last super-admin — promote another user first")
	}
	if err := txQ.DemoteFromSuperAdmin(ctx, target); err != nil {
		s.logger.Error("demote super admin: update", "error", err.Error(), "target", target)
		return problem.Internal("could not demote user")
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("demote super admin: commit", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	committed = true

	s.recordAudit(c, 0, actor.ID, "install.super_admin_demoted", "user", &target, nil)
	return c.NoContent(http.StatusNoContent)
}

// ---- restart flow ------------------------------------------------------

type restartStatusDTO struct {
	RestartRequired bool   `json:"restart_required"`
	Since           string `json:"since,omitempty"`
	// Supervised indicates whether the in-app restart button is wired
	// (i.e. main.go set a restart requester). When false, the UI hides
	// the button and shows a shell-command hint instead — a bare
	// `./sliils-app` run has no supervisor to bring it back.
	Supervised bool `json:"supervised"`
}

func (s *Server) getRestartStatus(c echo.Context) error {
	if s.installSvc == nil {
		return problem.ServiceUnavailable("install settings service is not wired")
	}
	since, _ := s.installSvc.Get(c.Request().Context(), install.KeyRestartRequiredAt)
	return c.JSON(http.StatusOK, restartStatusDTO{
		RestartRequired: since != "",
		Since:           since,
		Supervised:      s.requestRestart != nil,
	})
}

func (s *Server) postRestart(c echo.Context) error {
	if s.requestRestart == nil {
		// No supervisor hooked up — refuse rather than kill the process
		// with no way to come back. The UI surfaces this as a
		// "restart via your init system" message.
		return problem.ServiceUnavailable(
			"restart-on-demand is not available; this install runs without a process supervisor — restart via systemd / docker compose",
		)
	}
	actor := userFromContext(c)
	s.recordAudit(c, 0, actor.ID, "install.server_restart", "install", nil, nil)
	s.logger.Warn("super-admin requested server restart",
		"actor_id", actor.ID,
		"actor_email", actor.Email,
	)
	// Fire the request asynchronously so the HTTP response can flush
	// before the signal loop tears the server down. A small delay lets
	// the JSON round-trip; by the time the timer expires main.go is
	// already on the shutdown path and returning 202 is the best we can
	// offer (the next request will race the shutdown).
	go func() {
		time.Sleep(250 * time.Millisecond)
		s.requestRestart()
	}()
	return c.JSON(http.StatusAccepted, map[string]any{
		"status":  "restarting",
		"message": "the server is shutting down; your process supervisor will bring it back in a moment",
	})
}
