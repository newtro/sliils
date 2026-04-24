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
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
)

// Slug rules shared by first-run bootstrap and any future self-service
// workspace-creation flow. 2-40 chars, lowercase alphanumerics + hyphens,
// must start with a letter so it's a valid URL path component.
var firstRunSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

// firstRunBootstrapLockID is a constant passed to pg_advisory_xact_lock
// inside firstRunBootstrap. Any value fits in a bigint; pick one unlikely
// to collide with other uses of advisory locks (there are none at v1).
const firstRunBootstrapLockID int64 = 0x5a117f1857f17

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
// The bootstrap endpoint runs ONCE. A pg_advisory_xact_lock held for
// the life of the transaction serialises concurrent callers, and the
// CountActiveUsers check is re-executed INSIDE that transaction so the
// loser sees the first admin's row and gets 409.

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
	AccessToken   string `json:"access_token"`
	TokenType     string `json:"token_type"`
	ExpiresAt     string `json:"expires_at"`
	WorkspaceSlug string `json:"workspace_slug"`
	UserID        int64  `json:"user_id"`
}

func (s *Server) mountFirstRun(api *echo.Group) {
	// Both endpoints are intentionally unauthenticated. `state` is
	// read-only public; `bootstrap` self-gates on users-count == 0.
	// Both are rate-limited to blunt a bored scanner.
	api.GET("/first-run/state", s.firstRunState)
	api.POST("/first-run/bootstrap", s.firstRunBootstrap)
}

func (s *Server) firstRunState(c echo.Context) error {
	if s.ownerPool == nil {
		return problem.Internal("first-run state requires the owner pool")
	}
	ip := clientIP(c)
	if !s.limiter.Allow("first-run-state:"+ip, ratelimit.RuleFirstRun) {
		return problem.TooManyRequests("too many requests")
	}
	q := sqlcgen.New(s.ownerPool)
	n, err := q.CountActiveUsers(c.Request().Context())
	if err != nil {
		s.logger.Error("first-run state: count users", "error", err.Error())
		return problem.Internal("could not read install state")
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

	ip := clientIP(c)
	if !s.limiter.Allow("first-run-bootstrap:"+ip, ratelimit.RuleFirstRun) {
		return problem.TooManyRequests("too many requests")
	}

	var req bootstrapRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	// 1. Validate admin account.
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
	if err := validateDisplayName(displayName); err != nil {
		return problem.BadRequest("display name: " + err.Error())
	}

	// 2. Validate signup mode.
	switch req.SignupMode {
	case install.SignupOpen, install.SignupInviteOnly:
	case "":
		req.SignupMode = install.SignupInviteOnly
	default:
		return problem.BadRequest("signup_mode must be 'open' or 'invite_only'")
	}

	// 3. Validate workspace.
	wsName := strings.TrimSpace(req.Workspace.Name)
	wsSlug := strings.ToLower(strings.TrimSpace(req.Workspace.Slug))
	if err := validateWorkspaceName(wsName); err != nil {
		return problem.BadRequest(err.Error())
	}
	if !firstRunSlugPattern.MatchString(wsSlug) {
		return problem.BadRequest("workspace slug must be 2-40 lowercase letters, digits, or hyphens (must start with a letter)")
	}

	// 4. Hash password outside the transaction — argon2id is deliberately
	//    slow and holding an advisory lock across it would serialise all
	//    concurrent bootstraps for no benefit.
	hash, err := s.hasher.Hash(req.Admin.Password)
	if err != nil {
		s.logger.Error("first-run: hash password", "error", err.Error())
		return problem.Internal("could not hash password")
	}

	// 5. Everything else runs inside ONE owner-pool transaction behind a
	//    pg_advisory_xact_lock. The lock serialises concurrent bootstrap
	//    attempts; the in-tx users-count re-check is what proves to the
	//    loser that the install is no longer empty.
	ctx := c.Request().Context()
	tx, err := s.ownerPool.Begin(ctx)
	if err != nil {
		s.logger.Error("first-run: begin tx", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", firstRunBootstrapLockID); err != nil {
		s.logger.Error("first-run: acquire advisory lock", "error", err.Error())
		return problem.Internal("database unavailable")
	}

	txQ := sqlcgen.New(tx)

	n, err := txQ.CountActiveUsers(ctx)
	if err != nil {
		s.logger.Error("first-run: count users", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	if n > 0 {
		return problem.Conflict("install is already initialised — sign in with an existing account")
	}

	// Also set app.user_id=0 for the duration of this tx. Workspace insert
	// runs under the owner role so RLS is bypassed, but being explicit here
	// keeps the contract clear for future readers.
	// (Owner pool connects without SET ROLE sliils_app, so RLS doesn't apply.)

	created, err := txQ.CreateUser(ctx, sqlcgen.CreateUserParams{
		Email:        email,
		PasswordHash: &hash,
		DisplayName:  displayName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("first-run: create user returned no row")
			return problem.Internal("could not create admin user")
		}
		s.logger.Error("first-run: create user", "error", err.Error())
		return problem.Internal("could not create admin user")
	}
	if err := txQ.MarkEmailVerified(ctx, created.ID); err != nil {
		s.logger.Error("first-run: mark admin verified", "error", err.Error())
		return problem.Internal("could not verify admin email")
	}
	if err := txQ.PromoteToSuperAdmin(ctx, created.ID); err != nil {
		s.logger.Error("first-run: promote super admin", "error", err.Error())
		return problem.Internal("could not promote admin")
	}

	// Workspace + owner membership inside the same tx. Owner role bypasses
	// RLS so we don't need app.workspace_id set.
	ws, err := txQ.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{
		Slug:        wsSlug,
		Name:        wsName,
		Description: req.Workspace.Description,
		CreatedBy:   created.ID,
	})
	if err != nil {
		s.logger.Error("first-run: create workspace", "error", err.Error(), "slug", wsSlug)
		return problem.Internal("could not create workspace")
	}
	if _, err := txQ.CreateMembership(ctx, sqlcgen.CreateMembershipParams{
		WorkspaceID: ws.ID,
		UserID:      created.ID,
		Role:        "owner",
	}); err != nil {
		s.logger.Error("first-run: create membership", "error", err.Error())
		return problem.Internal("could not create workspace membership")
	}

	// Persist install settings inside the tx so a partial failure rolls
	// everything back together. The install service uses its own pool
	// reference; since we need these writes to participate in this tx
	// we inline the upserts directly against the tx handle.
	persistSetting := func(key, value string, encrypted bool) error {
		if !encrypted {
			return txQ.UpsertInstallSetting(ctx, sqlcgen.UpsertInstallSettingParams{
				Key:       key,
				Value:     value,
				Encrypted: false,
				UpdatedBy: &created.ID,
			})
		}
		sealed, err := s.installSvc.EncryptForTx(value)
		if err != nil {
			return err
		}
		return txQ.UpsertInstallSetting(ctx, sqlcgen.UpsertInstallSettingParams{
			Key:       key,
			Value:     sealed,
			Encrypted: true,
			UpdatedBy: &created.ID,
		})
	}

	if strings.TrimSpace(req.Email.ResendAPIKey) != "" {
		if err := persistSetting(install.KeyInstallEmailProvider, firstNonEmptyStr(req.Email.Provider, "resend"), false); err != nil {
			s.logger.Error("first-run: persist email provider", "error", err.Error())
			return problem.Internal("could not save email provider")
		}
		if err := persistSetting(install.KeyInstallResendAPIKey, strings.TrimSpace(req.Email.ResendAPIKey), true); err != nil {
			s.logger.Error("first-run: persist resend key", "error", err.Error())
			return problem.BadRequest(err.Error())
		}
		if err := persistSetting(install.KeyInstallEmailFrom, strings.TrimSpace(req.Email.FromAddress), false); err != nil {
			s.logger.Error("first-run: persist from address", "error", err.Error())
			return problem.Internal("could not save from address")
		}
		if err := persistSetting(install.KeyInstallEmailFromName, strings.TrimSpace(req.Email.FromName), false); err != nil {
			s.logger.Error("first-run: persist from name", "error", err.Error())
			return problem.Internal("could not save from name")
		}
	}
	if err := persistSetting(install.KeySignupMode, req.SignupMode, false); err != nil {
		s.logger.Error("first-run: persist signup mode", "error", err.Error())
		return problem.Internal("could not save signup mode")
	}
	if err := persistSetting(install.KeyInstallSetupCompleted, "true", false); err != nil {
		s.logger.Error("first-run: persist setup completed", "error", err.Error())
		return problem.Internal("could not save setup flag")
	}

	// Session + refresh cookie also live inside the tx so a commit-time
	// failure (statement_timeout, replica lag) rolls back the session
	// row instead of leaving a ghost row that points at the (rolled-back)
	// user.
	refresh, err := auth.RandomToken(32)
	if err != nil {
		s.logger.Error("first-run: mint refresh", "error", err.Error())
		return problem.Internal("could not mint refresh token")
	}
	refreshExp := time.Now().Add(s.cfg.RefreshTokenTTL)
	var ipAddr *netip.Addr
	if ip := c.RealIP(); ip != "" {
		if a, err := netip.ParseAddr(ip); err == nil {
			ipAddr = &a
		}
	}
	session, err := txQ.CreateSession(ctx, sqlcgen.CreateSessionParams{
		UserID:           created.ID,
		RefreshTokenHash: auth.HashToken(refresh),
		UserAgent:        c.Request().UserAgent(),
		Ip:               ipAddr,
		ExpiresAt:        pgtype.Timestamptz{Time: refreshExp, Valid: true},
	})
	if err != nil {
		s.logger.Error("first-run: create session", "error", err.Error())
		return problem.Internal("could not create session")
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("first-run: commit", "error", err.Error())
		return problem.Internal("database unavailable")
	}
	committed = true

	setRefreshCookie(c, s.cfg, refresh, refreshExp)
	token, exp, err := s.tokens.Issue(created.ID, session.ID, 0)
	if err != nil {
		// Tx is committed; the user exists. Surface the failure so the
		// operator retries login rather than believing bootstrap failed.
		s.logger.Error("first-run: issue access token", "error", err.Error())
		return problem.Internal("bootstrap completed but access token could not be issued; sign in to continue")
	}

	if s.auditor != nil {
		s.auditor.Record(c.Request().Context(), audit.Event{
			ActorUserID: &created.ID,
			ActorIP:     c.RealIP(),
			Action:      "install.bootstrap",
			TargetKind:  "install",
			Metadata: map[string]any{
				"workspace_slug": wsSlug,
				"workspace_id":   ws.ID,
				"signup_mode":    req.SignupMode,
			},
		})
	}

	return c.JSON(http.StatusCreated, bootstrapResponse{
		AccessToken:   token,
		TokenType:     "Bearer",
		ExpiresAt:     exp.UTC().Format(time.RFC3339Nano),
		WorkspaceSlug: wsSlug,
		UserID:        created.ID,
	})
}

// validateDisplayName caps length and rejects control / bidi characters
// that could impersonate other users in member lists or notifications.
// Empty is allowed — the caller falls back to the email address (at
// signup) or keeps the previous value (at update).
func validateDisplayName(name string) error {
	if len(name) > 64 {
		return errors.New("must be 64 characters or fewer")
	}
	if name == "" {
		return nil
	}
	return containsBidiOrControl(name)
}

// validateWorkspaceName caps length and rejects bidi/control chars that
// could impersonate other workspaces in the picker.
func validateWorkspaceName(name string) error {
	if name == "" {
		return errors.New("workspace name is required")
	}
	if len(name) > 80 {
		return errors.New("workspace name must be 80 characters or fewer")
	}
	return containsBidiOrControl(name)
}

// containsBidiOrControl refuses strings that include Unicode right-to-left
// override / embedding / isolate characters and ASCII/C0 controls. These
// are classic impersonation + log-injection vectors.
func containsBidiOrControl(s string) error {
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return errors.New("must not contain line breaks or tabs")
		case r < 0x20:
			return errors.New("must not contain control characters")
		case r == 0x200E || r == 0x200F, // LRM / RLM
			r == 0x202A || r == 0x202B || r == 0x202C || r == 0x202D || r == 0x202E, // LRE/RLE/PDF/LRO/RLO
			r == 0x2066 || r == 0x2067 || r == 0x2068 || r == 0x2069: // LRI/RLI/FSI/PDI
			return errors.New("must not contain bidirectional override characters")
		}
	}
	return nil
}

