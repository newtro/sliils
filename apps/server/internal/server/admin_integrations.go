package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/problem"
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

	// Install-wide endpoints. At v1 any workspace admin can read/edit
	// them — a self-hosted install is typically one operator with all
	// admins trusted. A dedicated super-admin role is a v1.1 concern.
	// Unauthenticated readers get the /install/status endpoint (below)
	// which reveals only whether the first-run wizard is complete.
	api.GET("/install/status", s.getInstallStatus)

	inst := api.Group("/install")
	inst.Use(s.requireAuth())
	inst.GET("/signup-mode", s.getInstallSignupMode)
	inst.PATCH("/signup-mode", s.patchInstallSignupMode)
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
