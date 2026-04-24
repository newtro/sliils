package server

import (
	"errors"
	"net/http"
	"net/mail"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/auth"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Slug rules shared by first-run bootstrap and any future self-service
// workspace-creation flow. 2-40 chars, lowercase alphanumerics + hyphens,
// must start with a letter so it's a valid URL path component.
var firstRunSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

// First-run wizard (M12-polish).
//
// On a fresh install the DB has zero users. The web client detects
// this via /install/status (server returns setup_completed: false +
// users_count: 0) and routes the browser to /first-run, where a
// single-page wizard walks the operator through:
//
//   1. Admin account — email + password + display name. The created
//      user is flagged is_super_admin=true.
//   2. Install email — Resend API key, from address, from name.
//      Optional; install can launch without email and fill in later
//      via Admin → Integrations, but invites + magic-link won't work
//      until it's set.
//   3. Signup policy — "open" or "invite_only". Invite-only is the
//      default to match "self-hosted for my team".
//   4. First workspace — name + slug.
//
// Server surface:
//
//   GET  /api/v1/first-run/state   → {completed, users_count}
//   POST /api/v1/first-run/bootstrap
//
// The bootstrap endpoint runs ONCE. It refuses (409) if any user
// already exists, so a misconfigured setup can't silently hand out
// super-admin to a second visitor.

type firstRunStateDTO struct {
	Completed  bool  `json:"completed"`
	UsersCount int64 `json:"users_count"`
	// SignupMode is the current setting so the wizard can pre-select
	// the radio button matching whatever was already seeded from env.
	SignupMode string `json:"signup_mode"`
}

type bootstrapRequest struct {
	Admin struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	} `json:"admin"`
	Email struct {
		Provider     string `json:"provider,omitempty"` // always "resend" at v1
		ResendAPIKey string `json:"resend_api_key,omitempty"`
		FromAddress  string `json:"from_address,omitempty"`
		FromName     string `json:"from_name,omitempty"`
	} `json:"email"`
	SignupMode string `json:"signup_mode"` // "open" | "invite_only"
	Workspace  struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description,omitempty"`
	} `json:"workspace"`
}

type bootstrapResponse struct {
	AccessToken    string `json:"access_token"`
	TokenType      string `json:"token_type"`
	ExpiresAt      string `json:"expires_at"`
	WorkspaceSlug  string `json:"workspace_slug"`
	UserID         int64  `json:"user_id"`
}

func (s *Server) mountFirstRun(api *echo.Group) {
	// Both endpoints are intentionally unauthenticated. `state` is
	// read-only public; `bootstrap` self-gates on users-count == 0.
	api.GET("/first-run/state", s.firstRunState)
	api.POST("/first-run/bootstrap", s.firstRunBootstrap)
}

func (s *Server) firstRunState(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("first-run state requires the owner pool")
	}
	q := sqlcgen.New(s.ownerPool)
	n, err := q.CountActiveUsers(c.Request().Context())
	if err != nil {
		return problem.Internal("count users: " + err.Error())
	}
	mode := install.SignupInviteOnly
	if s.installSvc != nil {
		mode = s.installSvc.SignupMode(c.Request().Context())
	}
	return c.JSON(http.StatusOK, firstRunStateDTO{
		Completed:  n > 0,
		UsersCount: n,
		SignupMode: mode,
	})
}

func (s *Server) firstRunBootstrap(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("bootstrap requires the owner pool")
	}
	if s.installSvc == nil {
		return problem.Internal("bootstrap requires the install service")
	}

	var req bootstrapRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	// 1. Self-gate: only runs when zero users exist.
	ownerQ := sqlcgen.New(s.ownerPool)
	n, err := ownerQ.CountActiveUsers(c.Request().Context())
	if err != nil {
		return problem.Internal("count users: " + err.Error())
	}
	if n > 0 {
		return problem.Conflict("install is already initialised — sign in with an existing account")
	}

	// 2. Validate admin account.
	email := strings.ToLower(strings.TrimSpace(req.Admin.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return problem.BadRequest("invalid admin email")
	}
	if err := validatePassword(req.Admin.Password); err != nil {
		return problem.BadRequest("admin password: " + err.Error())
	}
	displayName := strings.TrimSpace(req.Admin.DisplayName)
	if displayName == "" {
		displayName = email
	}

	// 3. Validate signup mode.
	switch req.SignupMode {
	case install.SignupOpen, install.SignupInviteOnly:
	case "":
		req.SignupMode = install.SignupInviteOnly
	default:
		return problem.BadRequest("signup_mode must be 'open' or 'invite_only'")
	}

	// 4. Validate workspace.
	wsName := strings.TrimSpace(req.Workspace.Name)
	wsSlug := strings.ToLower(strings.TrimSpace(req.Workspace.Slug))
	if wsName == "" {
		return problem.BadRequest("workspace name is required")
	}
	if !firstRunSlugPattern.MatchString(wsSlug) {
		return problem.BadRequest("workspace slug must be 2-40 lowercase letters, digits, or hyphens (must start with a letter)")
	}

	// 5. Create the admin user + promote to super-admin.
	hash, err := s.hasher.Hash(req.Admin.Password)
	if err != nil {
		return problem.Internal("hash password: " + err.Error())
	}
	created, err := ownerQ.CreateUser(c.Request().Context(), sqlcgen.CreateUserParams{
		Email:        email,
		PasswordHash: &hash,
		DisplayName:  displayName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Internal("create user returned no row")
		}
		return problem.Internal("create admin user: " + err.Error())
	}
	if err := ownerQ.MarkEmailVerified(c.Request().Context(), created.ID); err != nil {
		s.logger.Warn("bootstrap: mark admin verified", "error", err.Error())
	}
	if err := ownerQ.PromoteToSuperAdmin(c.Request().Context(), created.ID); err != nil {
		return problem.Internal("promote super admin: " + err.Error())
	}

	// 6. Persist email config (optional — wizard allows skipping).
	if strings.TrimSpace(req.Email.ResendAPIKey) != "" {
		_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallEmailProvider, firstNonEmptyStr(req.Email.Provider, "resend"), false, &created.ID)
		_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallResendAPIKey, strings.TrimSpace(req.Email.ResendAPIKey), true, &created.ID)
		_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallEmailFrom, strings.TrimSpace(req.Email.FromAddress), false, &created.ID)
		_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallEmailFromName, strings.TrimSpace(req.Email.FromName), false, &created.ID)
	}
	_ = s.installSvc.Set(c.Request().Context(), install.KeySignupMode, req.SignupMode, false, &created.ID)
	_ = s.installSvc.Set(c.Request().Context(), install.KeyInstallSetupCompleted, "true", false, &created.ID)

	// 7. Create the first workspace + owner membership. Workspace
	// creation under the owner pool so we don't need app.user_id set.
	var wsID int64
	err = db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: created.ID}, func(scope db.TxScope) error {
		ws, err := scope.Queries.CreateWorkspace(c.Request().Context(), sqlcgen.CreateWorkspaceParams{
			Slug:        wsSlug,
			Name:        wsName,
			Description: req.Workspace.Description,
			CreatedBy:   created.ID,
		})
		if err != nil {
			return err
		}
		wsID = ws.ID
		if _, err := scope.Queries.CreateMembership(c.Request().Context(), sqlcgen.CreateMembershipParams{
			WorkspaceID: ws.ID,
			UserID:      created.ID,
			Role:        "owner",
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return problem.Internal("create workspace: " + err.Error())
	}

	// 8. Issue a session + refresh cookie so the wizard can land the
	// user in their workspace without a separate login step.
	refresh, err := auth.RandomToken(32)
	if err != nil {
		return problem.Internal("mint refresh: " + err.Error())
	}
	refreshExp := time.Now().Add(s.cfg.RefreshTokenTTL)
	var ipAddr *netip.Addr
	if ip := c.RealIP(); ip != "" {
		if a, err := netip.ParseAddr(ip); err == nil {
			ipAddr = &a
		}
	}
	session, err := ownerQ.CreateSession(c.Request().Context(), sqlcgen.CreateSessionParams{
		UserID:           created.ID,
		RefreshTokenHash: auth.HashToken(refresh),
		UserAgent:        c.Request().UserAgent(),
		Ip:               ipAddr,
		ExpiresAt:        pgtype.Timestamptz{Time: refreshExp, Valid: true},
	})
	if err != nil {
		return problem.Internal("create session: " + err.Error())
	}
	setRefreshCookie(c, s.cfg, refresh, refreshExp)
	token, exp, err := s.tokens.Issue(created.ID, session.ID, 0)
	if err != nil {
		return problem.Internal("issue token: " + err.Error())
	}

	if s.auditor != nil {
		s.auditor.Record(c.Request().Context(), audit.Event{
			ActorUserID: &created.ID,
			ActorIP:     c.RealIP(),
			Action:      "install.bootstrap",
			TargetKind:  "install",
			Metadata: map[string]any{
				"workspace_slug": wsSlug,
				"workspace_id":   wsID,
				"signup_mode":    req.SignupMode,
			},
		})
	}

	return c.JSON(http.StatusCreated, bootstrapResponse{
		AccessToken:   token,
		TokenType:     "Bearer",
		ExpiresAt:     exp.Format("2006-01-02T15:04:05.999999999Z07:00"),
		WorkspaceSlug: wsSlug,
		UserID:        created.ID,
	})
}
