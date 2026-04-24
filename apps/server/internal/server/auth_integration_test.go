//go:build integration

package server_test

// Integration tests for the M1 auth surface.
//
// Run with:
//   SLIILS_TEST_DATABASE_URL=postgres://... go test -tags=integration ./internal/server/...
//
// Requires a reachable Postgres. CI uses a throwaway container via the Go
// integration job; developers can point at local Postgres.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

// testHarness bundles server + captured outgoing emails for a fresh
// test install. Each top-level test gets an isolated schema.
type testHarness struct {
	t      *testing.T
	srv    *server.Server
	pool   *db.Pool
	dsn    string
	emails chan email.Message
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	dsn := os.Getenv("SLIILS_TEST_DATABASE_URL")
	if dsn == "" {
		// Default to the local-dev database the kickoff doc calls out. Developers
		// who want isolation set SLIILS_TEST_DATABASE_URL to a dedicated DB.
		dsn = "postgres://postgres:Fl1pFl0p@localhost:5432/sliils_test?sslmode=disable"
	}

	// Fresh schema per test: reset, migrate, then open the runtime pool.
	// Using a raw pgxpool to reset because db.Open would try to SET ROLE
	// sliils_app before the role exists.
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	resetSchema(t, dsn)
	require.NoError(t, db.RunMigrations(ctx, dsn, migrations.FS, ".", logger))

	pool, err := db.Open(ctx, dsn, logger)
	require.NoError(t, err, "open test database (set SLIILS_TEST_DATABASE_URL)")

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")

	cfg, err := config.Load()
	require.NoError(t, err)
	// Skip Validate: we're using the noop email sender.
	cfg.PublicBaseURL = "http://testhost"

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender: email.NoopSender{Sent: emails},
		Limiter:     ratelimit.New(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}
}

// adminExec runs SQL under the DSN's original role (typically postgres) —
// not the sliils_app runtime role — so tests can set up state that RLS
// would otherwise block. Use ONLY for test scaffolding, never from prod code.
func (h *testHarness) adminExec(sql string, args ...any) error {
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(h.dsn)
	if err != nil {
		return err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql, args...)
	return err
}

func mustExec(t *testing.T, p *db.Pool, sql string) {
	t.Helper()
	_, err := p.Pool.Exec(context.Background(), sql)
	require.NoError(t, err, "exec %q", sql)
}

// resetSchema drops and recreates the public schema under the DSN's original
// role. Bypasses the runtime pool so no SET ROLE happens before the role
// exists. Each integration test gets a fully-clean schema.
func resetSchema(t *testing.T, dsn string) {
	t.Helper()
	connCfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	conn, err := pgx.ConnectConfig(context.Background(), connCfg)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	_, err = conn.Exec(context.Background(),
		`DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public; CREATE EXTENSION IF NOT EXISTS citext;`)
	require.NoError(t, err, "reset schema")
}

func (h *testHarness) post(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// postAuth is like post but also attaches a Bearer token.
func (h *testHarness) postAuth(path, body, bearer string) *httptest.ResponseRecorder {
	return h.authJSON(http.MethodPost, path, body, bearer)
}

func (h *testHarness) patchAuth(path, body, bearer string) *httptest.ResponseRecorder {
	return h.authJSON(http.MethodPatch, path, body, bearer)
}

func (h *testHarness) deleteAuth(path, body, bearer string) *httptest.ResponseRecorder {
	return h.authJSON(http.MethodDelete, path, body, bearer)
}

func (h *testHarness) authJSON(method, path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *testHarness) get(path string, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

type sessionResponse struct {
	AccessToken string                 `json:"access_token"`
	TokenType   string                 `json:"token_type"`
	User        map[string]interface{} `json:"user"`
}

func signup(t *testing.T, h *testHarness, email, password string) (*sessionResponse, *http.Cookie) {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `","display_name":"Test User"}`
	rec := h.post("/api/v1/auth/signup", body, nil)
	require.Equal(t, http.StatusOK, rec.Code, "signup body: %s", rec.Body.String())

	var resp sessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.AccessToken)

	var refreshCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == server.RefreshCookieName {
			refreshCookie = c
		}
	}
	require.NotNil(t, refreshCookie, "signup should set refresh cookie")
	return &resp, refreshCookie
}

func TestAuthHappyPath(t *testing.T) {
	h := newHarness(t)
	resp, refresh := signup(t, h, "alice@example.com", "correct-horse-battery-staple")
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, "alice@example.com", resp.User["email"])

	// /me with the access token returns the same identity.
	rec := h.get("/api/v1/me", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var me map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.Equal(t, "alice@example.com", me["email"])
	assert.Equal(t, "Test User", me["display_name"])

	// Verify email was queued.
	select {
	case msg := <-h.emails:
		assert.Equal(t, []string{"alice@example.com"}, msg.To)
		assert.Contains(t, msg.Subject, "Verify")
	case <-time.After(2 * time.Second):
		t.Fatal("no verify email sent")
	}

	// Logout clears the cookie and revokes the session.
	rec = h.post("/api/v1/auth/logout", "", refresh)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Subsequent refresh with the now-revoked cookie fails.
	rec = h.post("/api/v1/auth/refresh", "", refresh)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLoginWrongPassword(t *testing.T) {
	h := newHarness(t)
	signup(t, h, "bob@example.com", "correct-horse-battery-staple")
	drainEmails(h) // drop the signup verify email

	body := `{"email":"bob@example.com","password":"wrong-password"}`
	rec := h.post("/api/v1/auth/login", body, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "problem+json")
}

func TestLoginAfterSignup(t *testing.T) {
	h := newHarness(t)
	signup(t, h, "carol@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	body := `{"email":"carol@example.com","password":"correct-horse-battery-staple"}`
	rec := h.post("/api/v1/auth/login", body, nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp sessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
}

func TestSignupDuplicateEmail(t *testing.T) {
	h := newHarness(t)
	signup(t, h, "dave@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	body := `{"email":"dave@example.com","password":"correct-horse-battery-staple","display_name":""}`
	rec := h.post("/api/v1/auth/signup", body, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestMagicLinkFlow(t *testing.T) {
	h := newHarness(t)
	signup(t, h, "erin@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	// Request magic link
	body := `{"email":"erin@example.com"}`
	rec := h.post("/api/v1/auth/magic-link/request", body, nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var msg email.Message
	select {
	case msg = <-h.emails:
	case <-time.After(2 * time.Second):
		t.Fatal("magic-link email not sent")
	}
	token := extractTokenFromEmail(t, msg, "/auth/magic-link?token=")

	// Consume
	consume, _ := json.Marshal(map[string]string{"token": token})
	rec = h.post("/api/v1/auth/magic-link/consume", string(consume), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Second consume of same token should fail.
	rec = h.post("/api/v1/auth/magic-link/consume", string(consume), nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPasswordResetFlow(t *testing.T) {
	h := newHarness(t)
	signup(t, h, "frank@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	body := `{"email":"frank@example.com"}`
	rec := h.post("/api/v1/auth/password-reset/request", body, nil)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	msg := mustReceive(t, h.emails)
	token := extractTokenFromEmail(t, msg, "/auth/reset-password?token=")

	confirm, _ := json.Marshal(map[string]string{
		"token":        token,
		"new_password": "a-brand-new-password",
	})
	rec = h.post("/api/v1/auth/password-reset/confirm", string(confirm), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// New password works.
	login := `{"email":"frank@example.com","password":"a-brand-new-password"}`
	rec = h.post("/api/v1/auth/login", login, nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Old password doesn't.
	login = `{"email":"frank@example.com","password":"correct-horse-battery-staple"}`
	rec = h.post("/api/v1/auth/login", login, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// ---- helpers --------------------------------------------------------------

func drainEmails(h *testHarness) {
	for {
		select {
		case <-h.emails:
		default:
			return
		}
	}
}

func mustReceive(t *testing.T, ch <-chan email.Message) email.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("email not received")
	}
	return email.Message{}
}

func extractTokenFromEmail(t *testing.T, msg email.Message, urlPrefix string) string {
	t.Helper()
	haystack := msg.TextBody + msg.HTMLBody
	i := strings.Index(haystack, urlPrefix)
	require.GreaterOrEqual(t, i, 0, "url prefix %q not in email body", urlPrefix)
	rest := haystack[i+len(urlPrefix):]
	// Token ends at the first character that can't legally appear in a
	// URL-safe base64 token (RFC 4648 §5: A-Z a-z 0-9 - _).
	end := len(rest)
	for j, r := range rest {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			end = j
			break
		}
	}
	return rest[:end]
}

