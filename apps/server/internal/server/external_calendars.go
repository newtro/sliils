package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/calsync"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/secretbox"
)

// External-calendar OAuth endpoints (M9-P3).
//
//   GET  /me/external-calendars                      — list connected
//   POST /me/external-calendars/:provider/start      — kick off OAuth
//   GET  /auth/external-calendars/:provider/callback — OAuth redirect target
//   DELETE /me/external-calendars/:provider          — disconnect
//
// The callback is intentionally NOT under /me — Google/Microsoft redirect
// unauthenticated browsers to it. We infer the user via the `state` param,
// which carries an HMAC-signed user_id + nonce.

// ---- DTOs ---------------------------------------------------------------

type ExternalCalendarDTO struct {
	Provider     string    `json:"provider"`
	AccountEmail string    `json:"account_email"`
	ConnectedAt  time.Time `json:"connected_at"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountExternalCalendars(api *echo.Group) {
	if s.calSync == nil {
		// Feature is off. Return 503 on the user-facing endpoints so the
		// client can show a "configure OAuth first" banner.
		api.GET("/me/external-calendars", s.externalCalendarsDisabled)
		api.POST("/me/external-calendars/:provider/start", s.externalCalendarsDisabled)
		api.DELETE("/me/external-calendars/:provider", s.externalCalendarsDisabled)
		return
	}

	authed := api.Group("")
	authed.Use(s.requireAuth())
	authed.GET("/me/external-calendars", s.listMyExternalCalendars)
	authed.POST("/me/external-calendars/:provider/start", s.startExternalCalendarOAuth)
	authed.DELETE("/me/external-calendars/:provider", s.disconnectExternalCalendar)

	// Callback: token-signed, not session-authed.
	api.GET("/auth/external-calendars/:provider/callback", s.externalCalendarOAuthCallback)
}

func (s *Server) externalCalendarsDisabled(c echo.Context) error {
	return problem.ServiceUnavailable("external calendar sync is not configured on this install (set SLIILS_EXTERNAL_CALENDARS_ENABLED + provider credentials)")
}

// ---- handlers: list / disconnect ----------------------------------------

func (s *Server) listMyExternalCalendars(c echo.Context) error {
	user := userFromContext(c)
	var rows []sqlcgen.ExternalCalendar
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		r, err := scope.Queries.ListExternalCalendarsForUser(c.Request().Context(), user.ID)
		if err != nil {
			return err
		}
		rows = r
		return nil
	})
	if err != nil {
		return problem.Internal("list: " + err.Error())
	}
	out := make([]ExternalCalendarDTO, 0, len(rows))
	for _, r := range rows {
		dto := ExternalCalendarDTO{
			Provider:     r.Provider,
			AccountEmail: r.ExternalAccountEmail,
			ConnectedAt:  r.ConnectedAt.Time,
		}
		if r.LastSyncedAt.Valid {
			t := r.LastSyncedAt.Time
			dto.LastSyncedAt = &t
		}
		out = append(out, dto)
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) disconnectExternalCalendar(c echo.Context) error {
	user := userFromContext(c)
	provider := c.Param("provider")
	if !validProvider(provider) {
		return problem.BadRequest("unknown provider")
	}
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID}, func(scope db.TxScope) error {
		return scope.Queries.DisconnectExternalCalendar(c.Request().Context(), sqlcgen.DisconnectExternalCalendarParams{
			UserID:   user.ID,
			Provider: provider,
		})
	})
	if err != nil {
		return problem.Internal("disconnect: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- handlers: OAuth start / callback -----------------------------------

func (s *Server) startExternalCalendarOAuth(c echo.Context) error {
	user := userFromContext(c)
	providerKey := c.Param("provider")
	provider, ok := s.calSync.Provider(providerKey)
	if !ok {
		return problem.BadRequest("provider not configured")
	}
	state, err := s.calSync.SignState(user.ID)
	if err != nil {
		return problem.Internal("sign state: " + err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{
		"redirect_url": provider.AuthCodeURL(state),
	})
}

// externalCalendarOAuthCallback: provider redirects the user's browser
// here with ?code=... &state=... We verify state, exchange the code,
// encrypt the refresh token, upsert the row, and redirect back to the
// web app's calendar page.
func (s *Server) externalCalendarOAuthCallback(c echo.Context) error {
	providerKey := c.Param("provider")
	provider, ok := s.calSync.Provider(providerKey)
	if !ok {
		return problem.BadRequest("provider not configured")
	}
	code := c.QueryParam("code")
	state := c.QueryParam("state")
	if code == "" || state == "" {
		return problem.BadRequest("missing code/state")
	}
	userID, err := s.calSync.VerifyState(state)
	if err != nil {
		return problem.Unauthorized("state verify failed: " + err.Error())
	}

	ctx := c.Request().Context()
	tok, err := provider.Exchange(ctx, code)
	if err != nil {
		return problem.BadRequest("oauth exchange failed: " + err.Error())
	}
	email, err := provider.AccountEmail(ctx, tok.RefreshToken)
	if err != nil && !errors.Is(err, calsync.ErrNeedsReauth) {
		return problem.BadRequest("account probe failed: " + err.Error())
	}

	enc, err := s.calSync.Encrypt([]byte(tok.RefreshToken))
	if err != nil {
		return problem.Internal("encrypt refresh token: " + err.Error())
	}

	// Upsert under the user's own scope. The wsm_self RLS policy on
	// external_calendars matches on user_id, so no workspace GUC needed.
	err = db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID}, func(scope db.TxScope) error {
		_, err := scope.Queries.UpsertExternalCalendar(ctx, sqlcgen.UpsertExternalCalendarParams{
			UserID:               userID,
			Provider:             provider.Name(),
			ExternalAccountEmail: email,
			OauthRefreshToken:    enc,
		})
		return err
	})
	if err != nil {
		return problem.Internal("persist connection: " + err.Error())
	}

	// Bounce back to the web app's calendar page. Build the redirect URL
	// from PublicBaseURL so we land the browser on the right origin.
	redirect := strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/calendar/settings?connected=" + provider.Name()
	return c.Redirect(http.StatusFound, redirect)
}

func validProvider(p string) bool {
	switch p {
	case "google", "microsoft", "caldav":
		return true
	}
	return false
}

// ---- state signing for the OAuth round-trip (unexported helpers) --------

// The "state" parameter in OAuth is an opaque CSRF token. We use it to
// also carry the user id so the callback (which arrives on an
// unauthenticated redirect) knows WHO just consented. Bound with HMAC
// so a client can't forge a state for another user.

// The implementation lives in the CalSyncService struct (see
// internal/server/deps.go) — this file just wires the HTTP handlers.

// ---- small helpers ------------------------------------------------------

// newOAuthNonce generates a URL-safe random string — used internally by
// the CalSyncService to build the state parameter.
func newOAuthNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// keep unused imports honest
var _ = context.Background
var _ secretbox.Box
var _ = fmt.Sprintf
var _ = pgx.ErrNoRows
