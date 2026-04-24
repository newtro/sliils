package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/auth"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
)

// mountAuth attaches the auth route group to the given /api/v1 group.
// Endpoints match tech-spec §2.2.
func (s *Server) mountAuth(api *echo.Group) {
	g := api.Group("/auth")
	g.POST("/signup", s.authSignup)
	g.POST("/login", s.authLogin)
	g.POST("/logout", s.authLogout)
	g.POST("/refresh", s.authRefresh)
	g.POST("/verify-email/request", s.authVerifyEmailRequest)
	g.POST("/verify-email/consume", s.authVerifyEmailConsume)
	g.POST("/magic-link/request", s.authMagicLinkRequest)
	g.POST("/magic-link/consume", s.authMagicLinkConsume)
	g.POST("/password-reset/request", s.authPasswordResetRequest)
	g.POST("/password-reset/confirm", s.authPasswordResetConfirm)
}

// ---- request / response DTOs ---------------------------------------------

type signupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type magicLinkRequestBody struct {
	Email string `json:"email"`
}

type consumeTokenRequest struct {
	Token string `json:"token"`
}

type passwordResetConfirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type sessionResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	User        UserDTO   `json:"user"`
}

type messageResponse struct {
	Message string `json:"message"`
}

// ---- signup ---------------------------------------------------------------

func (s *Server) authSignup(c echo.Context) error {
	ip := clientIP(c)
	if !s.limiter.Allow("signup:"+ip, ratelimit.RuleSignup) {
		return problem.TooManyRequests("signup rate limit exceeded")
	}

	var req signupRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return problem.BadRequest("invalid email address")
	}
	if err := validatePassword(req.Password); err != nil {
		return problem.BadRequest(err.Error())
	}
	if len(req.DisplayName) > 64 {
		return problem.BadRequest("display_name must be 64 characters or fewer")
	}

	// Does the email already exist?
	if existing, err := s.queries.GetUserByEmail(c.Request().Context(), req.Email); err == nil {
		// Use 409 to signal collision; don't leak whether email was previously used.
		_ = existing
		return problem.Conflict("an account with that email already exists")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return problem.Internal("lookup user: " + err.Error())
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return problem.Internal("hash password: " + err.Error())
	}

	created, err := s.queries.CreateUser(c.Request().Context(), sqlcgen.CreateUserParams{
		Email:        req.Email,
		PasswordHash: &hash,
		DisplayName:  req.DisplayName,
	})
	if err != nil {
		return problem.Internal("create user: " + err.Error())
	}

	// Issue + email the verification token. Delivery failure shouldn't 500 the
	// signup — the user can re-request verification later.
	if err := s.sendVerifyEmail(c.Request().Context(), created.ID, created.Email, ip); err != nil {
		s.logger.Warn("send verify email failed", "user_id", created.ID, "error", err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &created.ID,
		ActorIP:     ip,
		Action:      audit.ActionSignup,
		TargetKind:  "user",
		TargetID:    fmt.Sprint(created.ID),
	})

	// Issue first session immediately so the user lands signed-in. Unverified
	// email is allowed to use the app; feature gates that require verification
	// are applied at the relevant handlers (not relevant at M1).
	return s.issueSession(c, &created, ip)
}

// ---- login ---------------------------------------------------------------

func (s *Server) authLogin(c echo.Context) error {
	ip := clientIP(c)
	if !s.limiter.Allow("login:ip:"+ip, ratelimit.RuleLogin) {
		return problem.TooManyRequests("too many login attempts from this IP")
	}

	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	user, err := s.queries.GetUserByEmail(c.Request().Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Unauthorized("invalid email or password")
		}
		return problem.Internal("lookup user: " + err.Error())
	}

	if !s.limiter.Allow(fmt.Sprintf("login:user:%d", user.ID), ratelimit.RuleLoginPerUser) {
		return problem.TooManyRequests("too many login attempts for this account")
	}

	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		return problem.TooManyRequests("account temporarily locked due to failed login attempts")
	}

	if user.PasswordHash == nil || *user.PasswordHash == "" {
		return problem.Unauthorized("this account has no password — sign in with a magic link instead")
	}

	ok, err := s.hasher.Verify(req.Password, *user.PasswordHash)
	if err != nil {
		return problem.Internal("verify password: " + err.Error())
	}
	if !ok {
		lockoutMin := fmt.Sprint(s.cfg.LoginLockoutMinutes)
		row, incErr := s.queries.IncrementFailedLogins(c.Request().Context(), sqlcgen.IncrementFailedLoginsParams{
			ID:               user.ID,
			FailedLoginCount: int32(s.cfg.MaxFailedLogins),
			Column3:          &lockoutMin,
		})
		if incErr == nil && row.LockedUntil.Valid {
			s.auditor.Record(c.Request().Context(), audit.Event{
				ActorUserID: &user.ID,
				ActorIP:     ip,
				Action:      audit.ActionLoginLocked,
			})
		}
		s.auditor.Record(c.Request().Context(), audit.Event{
			ActorUserID: &user.ID,
			ActorIP:     ip,
			Action:      audit.ActionLoginFailure,
		})
		return problem.Unauthorized("invalid email or password")
	}

	// Success: clear failure counters and reset per-IP limiter so a legit
	// user isn't punished for someone else's earlier failures on this IP.
	_ = s.queries.ResetFailedLogins(c.Request().Context(), user.ID)
	s.limiter.Reset("login:ip:" + ip)

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		ActorIP:     ip,
		Action:      audit.ActionLoginSuccess,
	})

	return s.issueSession(c, &user, ip)
}

// ---- logout --------------------------------------------------------------

func (s *Server) authLogout(c echo.Context) error {
	cookie, err := c.Cookie(RefreshCookieName)
	if err == nil && cookie.Value != "" {
		hash := auth.HashToken(cookie.Value)
		if sess, err := s.queries.GetSessionByRefreshHash(c.Request().Context(), hash); err == nil {
			_ = s.queries.RevokeSession(c.Request().Context(), sess.ID)
			s.auditor.Record(c.Request().Context(), audit.Event{
				ActorUserID: &sess.UserID,
				ActorIP:     clientIP(c),
				Action:      audit.ActionLogout,
				TargetKind:  "session",
				TargetID:    fmt.Sprint(sess.ID),
			})
		}
	}
	clearRefreshCookie(c, s.cfg)
	return c.JSON(http.StatusOK, messageResponse{Message: "logged out"})
}

// ---- refresh -------------------------------------------------------------

func (s *Server) authRefresh(c echo.Context) error {
	cookie, err := c.Cookie(RefreshCookieName)
	if err != nil || cookie.Value == "" {
		return problem.Unauthorized("no refresh cookie")
	}

	hash := auth.HashToken(cookie.Value)
	sess, err := s.queries.GetSessionByRefreshHash(c.Request().Context(), hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Unauthorized("invalid or expired session")
		}
		return problem.Internal("load session: " + err.Error())
	}

	user, err := s.queries.GetUserByID(c.Request().Context(), sess.UserID)
	if err != nil {
		return problem.Unauthorized("user not found")
	}

	// Rotate: mint a new refresh token, update the session row, replace cookie.
	newRefresh, err := auth.RandomToken(32)
	if err != nil {
		return problem.Internal("mint refresh: " + err.Error())
	}
	newExpiry := time.Now().Add(s.cfg.RefreshTokenTTL)
	if err := s.queries.RotateSessionRefresh(c.Request().Context(), sqlcgen.RotateSessionRefreshParams{
		ID:               sess.ID,
		RefreshTokenHash: auth.HashToken(newRefresh),
		ExpiresAt:        pgtype.Timestamptz{Time: newExpiry, Valid: true},
	}); err != nil {
		return problem.Internal("rotate session: " + err.Error())
	}
	setRefreshCookie(c, s.cfg, newRefresh, newExpiry)

	access, accessExp, err := s.tokens.Issue(user.ID, sess.ID, 0)
	if err != nil {
		return problem.Internal("issue access: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		ActorIP:     clientIP(c),
		Action:      audit.ActionTokenRefresh,
	})

	return c.JSON(http.StatusOK, sessionResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresAt:   accessExp,
		User:        userFromRow(&user),
	})
}

// ---- email verification --------------------------------------------------

func (s *Server) authVerifyEmailRequest(c echo.Context) error {
	// Must be signed in to request a resend — prevents anonymous enumeration.
	// Wrap a lightweight auth check inline; mounting requireAuth here would
	// lose the ability to handle a missing token with a cleaner message.
	return s.requireAuth()(func(c echo.Context) error {
		user := userFromContext(c)
		if user.EmailVerifiedAt.Valid {
			return c.JSON(http.StatusOK, messageResponse{Message: "already verified"})
		}
		ip := clientIP(c)
		if err := s.sendVerifyEmail(c.Request().Context(), user.ID, user.Email, ip); err != nil {
			return problem.Internal("send verify email: " + err.Error())
		}
		return c.JSON(http.StatusAccepted, messageResponse{Message: "verification email sent"})
	})(c)
}

func (s *Server) authVerifyEmailConsume(c echo.Context) error {
	var req consumeTokenRequest
	if err := c.Bind(&req); err != nil || req.Token == "" {
		return problem.BadRequest("token required")
	}

	tok, err := s.queries.GetAuthTokenByHash(c.Request().Context(), sqlcgen.GetAuthTokenByHashParams{
		TokenHash: auth.HashToken(req.Token),
		Purpose:   "email_verify",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.BadRequest("invalid or expired verification link")
		}
		return problem.Internal("load token: " + err.Error())
	}

	if err := s.queries.ConsumeAuthToken(c.Request().Context(), tok.ID); err != nil {
		return problem.Internal("consume token: " + err.Error())
	}
	if err := s.queries.MarkEmailVerified(c.Request().Context(), tok.UserID); err != nil {
		return problem.Internal("mark verified: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &tok.UserID,
		ActorIP:     clientIP(c),
		Action:      audit.ActionEmailVerifyComplete,
	})

	return c.JSON(http.StatusOK, messageResponse{Message: "email verified"})
}

// ---- magic link ----------------------------------------------------------

func (s *Server) authMagicLinkRequest(c echo.Context) error {
	ip := clientIP(c)
	if !s.limiter.Allow("magic:"+ip, ratelimit.RuleMagicLinkIssue) {
		return problem.TooManyRequests("too many magic-link requests")
	}

	var req magicLinkRequestBody
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return problem.BadRequest("invalid email")
	}

	user, err := s.queries.GetUserByEmail(c.Request().Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Don't leak whether email exists. Always 202.
			return c.JSON(http.StatusAccepted, messageResponse{Message: "if that email is on file, a sign-in link has been sent"})
		}
		return problem.Internal("lookup user: " + err.Error())
	}

	if err := s.issueAndSendAuthToken(c.Request().Context(), user.ID, user.Email, "magic_link", ip, s.cfg.MagicLinkTTL); err != nil {
		s.logger.Warn("send magic link failed", "user_id", user.ID, "error", err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		ActorIP:     ip,
		Action:      audit.ActionMagicLinkRequest,
	})

	return c.JSON(http.StatusAccepted, messageResponse{Message: "if that email is on file, a sign-in link has been sent"})
}

func (s *Server) authMagicLinkConsume(c echo.Context) error {
	var req consumeTokenRequest
	if err := c.Bind(&req); err != nil || req.Token == "" {
		return problem.BadRequest("token required")
	}

	tok, err := s.queries.GetAuthTokenByHash(c.Request().Context(), sqlcgen.GetAuthTokenByHashParams{
		TokenHash: auth.HashToken(req.Token),
		Purpose:   "magic_link",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.BadRequest("invalid or expired sign-in link")
		}
		return problem.Internal("load token: " + err.Error())
	}

	if err := s.queries.ConsumeAuthToken(c.Request().Context(), tok.ID); err != nil {
		return problem.Internal("consume token: " + err.Error())
	}

	// Magic-link sign-in implicitly verifies the email (the user proved they
	// control the inbox).
	_ = s.queries.MarkEmailVerified(c.Request().Context(), tok.UserID)

	user, err := s.queries.GetUserByID(c.Request().Context(), tok.UserID)
	if err != nil {
		return problem.Internal("load user: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		ActorIP:     clientIP(c),
		Action:      audit.ActionMagicLinkConsume,
	})

	return s.issueSession(c, &user, clientIP(c))
}

// ---- password reset ------------------------------------------------------

func (s *Server) authPasswordResetRequest(c echo.Context) error {
	ip := clientIP(c)
	if !s.limiter.Allow("pwreset:"+ip, ratelimit.RulePasswordReset) {
		return problem.TooManyRequests("too many password-reset requests")
	}

	var req magicLinkRequestBody
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil {
		return problem.BadRequest("invalid email")
	}

	user, err := s.queries.GetUserByEmail(c.Request().Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusAccepted, messageResponse{Message: "if that email is on file, a reset link has been sent"})
		}
		return problem.Internal("lookup user: " + err.Error())
	}

	if err := s.issueAndSendAuthToken(c.Request().Context(), user.ID, user.Email, "password_reset", ip, s.cfg.PasswordResetTTL); err != nil {
		s.logger.Warn("send password reset failed", "user_id", user.ID, "error", err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		ActorIP:     ip,
		Action:      audit.ActionPasswordResetRequest,
	})

	return c.JSON(http.StatusAccepted, messageResponse{Message: "if that email is on file, a reset link has been sent"})
}

func (s *Server) authPasswordResetConfirm(c echo.Context) error {
	var req passwordResetConfirmRequest
	if err := c.Bind(&req); err != nil || req.Token == "" {
		return problem.BadRequest("token required")
	}
	if err := validatePassword(req.NewPassword); err != nil {
		return problem.BadRequest(err.Error())
	}

	tok, err := s.queries.GetAuthTokenByHash(c.Request().Context(), sqlcgen.GetAuthTokenByHashParams{
		TokenHash: auth.HashToken(req.Token),
		Purpose:   "password_reset",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.BadRequest("invalid or expired reset link")
		}
		return problem.Internal("load token: " + err.Error())
	}

	hash, err := s.hasher.Hash(req.NewPassword)
	if err != nil {
		return problem.Internal("hash password: " + err.Error())
	}

	if err := s.queries.UpdateUserPassword(c.Request().Context(), sqlcgen.UpdateUserPasswordParams{
		ID:           tok.UserID,
		PasswordHash: &hash,
	}); err != nil {
		return problem.Internal("update password: " + err.Error())
	}
	_ = s.queries.ConsumeAuthToken(c.Request().Context(), tok.ID)
	_ = s.queries.RevokeAllUserSessions(c.Request().Context(), tok.UserID)

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &tok.UserID,
		ActorIP:     clientIP(c),
		Action:      audit.ActionPasswordResetComplete,
	})

	return c.JSON(http.StatusOK, messageResponse{Message: "password updated; please sign in"})
}

// ---- shared helpers ------------------------------------------------------

// issueSession creates a new session row, sets the refresh cookie, and
// returns an access token in the JSON body.
func (s *Server) issueSession(c echo.Context, user *sqlcgen.User, ip string) error {
	refresh, err := auth.RandomToken(32)
	if err != nil {
		return problem.Internal("mint refresh token: " + err.Error())
	}
	expiresAt := time.Now().Add(s.cfg.RefreshTokenTTL)

	var ipAddr *netip.Addr
	if ip != "" {
		if a, err := netip.ParseAddr(ip); err == nil {
			ipAddr = &a
		}
	}

	sess, err := s.queries.CreateSession(c.Request().Context(), sqlcgen.CreateSessionParams{
		UserID:           user.ID,
		RefreshTokenHash: auth.HashToken(refresh),
		UserAgent:        c.Request().UserAgent(),
		Ip:               ipAddr,
		ExpiresAt:        pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return problem.Internal("create session: " + err.Error())
	}

	setRefreshCookie(c, s.cfg, refresh, expiresAt)

	access, accessExp, err := s.tokens.Issue(user.ID, sess.ID, 0)
	if err != nil {
		return problem.Internal("issue access token: " + err.Error())
	}

	return c.JSON(http.StatusOK, sessionResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresAt:   accessExp,
		User:        userFromRow(user),
	})
}

// issueAndSendAuthToken generates a single-use token, stores its hash, and
// emails the user the link. Used by email verification, magic link, and
// password reset flows.
func (s *Server) issueAndSendAuthToken(ctx context.Context, userID int64, emailAddr, purpose, ip string, ttl time.Duration) error {
	// Invalidate any previously-active token for the same purpose so old
	// magic-link emails can't be used after a new one is requested.
	_ = s.queries.InvalidateActiveAuthTokens(ctx, sqlcgen.InvalidateActiveAuthTokensParams{
		UserID:  userID,
		Purpose: purpose,
	})

	raw, err := auth.RandomToken(32)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	var ipAddr *netip.Addr
	if ip != "" {
		if a, err := netip.ParseAddr(ip); err == nil {
			ipAddr = &a
		}
	}

	if _, err := s.queries.CreateAuthToken(ctx, sqlcgen.CreateAuthTokenParams{
		UserID:    userID,
		Purpose:   purpose,
		TokenHash: auth.HashToken(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		Ip:        ipAddr,
	}); err != nil {
		return fmt.Errorf("store token: %w", err)
	}

	if s.email == nil {
		return errors.New("email sender not configured")
	}

	msg := buildEmailForPurpose(purpose, emailAddr, s.cfg.PublicBaseURL, raw)
	return s.email.Send(ctx, msg)
}

func (s *Server) sendVerifyEmail(ctx context.Context, userID int64, emailAddr, ip string) error {
	return s.issueAndSendAuthToken(ctx, userID, emailAddr, "email_verify", ip, s.cfg.EmailVerifyTTL)
}

func buildEmailForPurpose(purpose, recipient, publicBase, token string) email.Message {
	base := strings.TrimRight(publicBase, "/")
	switch purpose {
	case "email_verify":
		return email.VerifyEmail(recipient, fmt.Sprintf("%s/auth/verify-email?token=%s", base, token))
	case "magic_link":
		return email.MagicLink(recipient, fmt.Sprintf("%s/auth/magic-link?token=%s", base, token))
	case "password_reset":
		return email.PasswordReset(recipient, fmt.Sprintf("%s/auth/reset-password?token=%s", base, token))
	default:
		return email.Message{}
	}
}

// validatePassword enforces minimum strength. 10+ chars is the NIST floor;
// we don't enforce character classes because length is what matters.
func validatePassword(pw string) error {
	if len(pw) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(pw) > 256 {
		return errors.New("password must be 256 characters or fewer")
	}
	return nil
}
