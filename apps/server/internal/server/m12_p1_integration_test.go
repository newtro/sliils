//go:build integration

package server_test

// M12-P1 integration tests: dev portal + OAuth install flow.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

func newAppsHarness(t *testing.T) *testHarness {
	t.Helper()
	dsn := os.Getenv("SLIILS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:Fl1pFl0p@localhost:5432/sliils_test?sslmode=disable"
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	resetSchema(t, dsn)
	require.NoError(t, db.RunMigrations(context.Background(), dsn, migrations.FS, ".", logger))

	pool, err := db.Open(context.Background(), dsn, logger)
	require.NoError(t, err)
	ownerPool, err := db.OpenOwner(context.Background(), dsn, logger)
	require.NoError(t, err)

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")
	t.Setenv("SLIILS_SEARCH_ENABLED", "false")
	t.Setenv("SLIILS_CALLS_ENABLED", "false")
	t.Setenv("SLIILS_PAGES_ENABLED", "false")
	t.Setenv("SLIILS_PUSH_ENABLED", "false")

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		ownerPool.Close()
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}
}

// ---- dev portal --------------------------------------------------------

type createdApp struct {
	App struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		ClientID string `json:"client_id"`
		Manifest struct {
			Scopes       []string `json:"scopes"`
			RedirectURIs []string `json:"redirect_uris"`
		} `json:"manifest"`
	} `json:"app"`
	ClientSecret string `json:"client_secret"`
}

func createBasicApp(t *testing.T, h *testHarness, token, slug string) createdApp {
	t.Helper()
	body := fmt.Sprintf(`{
		"slug": %q,
		"name": "Test App",
		"description": "An app for tests",
		"manifest": {
			"scopes": ["chat:write","channels:read","bot"],
			"redirect_uris": ["http://localhost:9999/cb"],
			"bot_user": {"display_name": "Testbot"}
		}
	}`, slug)
	rec := h.postAuth("/api/v1/dev/apps", body, token)
	require.Equal(t, http.StatusCreated, rec.Code, "create app: %s", rec.Body.String())
	var c createdApp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &c))
	assert.NotEmpty(t, c.ClientSecret, "secret must be returned once")
	assert.NotEmpty(t, c.App.ClientID)
	return c
}

func TestM12P1CreateAndListApp(t *testing.T) {
	h := newAppsHarness(t)
	resp, _ := signup(t, h, "dev@m12.test", "correct-horse-battery-staple")
	drainEmails(h)

	created := createBasicApp(t, h, resp.AccessToken, "myapp")
	assert.True(t, strings.HasPrefix(created.App.ClientID, "slis-app-"))
	assert.True(t, strings.HasPrefix(created.ClientSecret, "slis-secret-"))

	rec := h.get("/api/v1/dev/apps", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.Equal(t, "myapp", list[0]["slug"])
}

func TestM12P1CreateAppRejectsUnknownScope(t *testing.T) {
	h := newAppsHarness(t)
	resp, _ := signup(t, h, "dev@m12.test", "correct-horse-battery-staple")
	drainEmails(h)

	rec := h.postAuth("/api/v1/dev/apps", `{
		"slug":"bad","name":"Bad","manifest":{"scopes":["take-over-the-world"]}
	}`, resp.AccessToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestM12P1OwnerIsolation(t *testing.T) {
	h := newAppsHarness(t)
	rA, _ := signup(t, h, "alice@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	rB, _ := signup(t, h, "bob@m12.test", "correct-horse-battery-staple")
	drainEmails(h)

	createBasicApp(t, h, rA.AccessToken, "alice-app")

	// Bob cannot see alice's app — list should be empty, and GET by slug 404.
	rec := h.get("/api/v1/dev/apps", rB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", strings.TrimSpace(rec.Body.String()))

	rec = h.get("/api/v1/dev/apps/alice-app", rB.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestM12P1RotateSecretReturnsNewValue(t *testing.T) {
	h := newAppsHarness(t)
	resp, _ := signup(t, h, "rot@m12.test", "correct-horse-battery-staple")
	drainEmails(h)

	created := createBasicApp(t, h, resp.AccessToken, "rotapp")
	rec := h.postAuth("/api/v1/dev/apps/rotapp/rotate-secret", "{}", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var rotated struct {
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rotated))
	assert.NotEmpty(t, rotated.ClientSecret)
	assert.NotEqual(t, created.ClientSecret, rotated.ClientSecret)
}

// ---- OAuth flow --------------------------------------------------------

func TestM12P1OAuthHappyPath(t *testing.T) {
	h := newAppsHarness(t)

	// Developer creates app
	devResp, _ := signup(t, h, "devops@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	app := createBasicApp(t, h, devResp.AccessToken, "ci-bot")

	// Workspace admin is a different user
	adminResp, _ := signup(t, h, "admin@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	ws := createWorkspace(t, h, adminResp.AccessToken, "M12 Co", "m12co")
	_ = ws

	// Generate PKCE pair
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r-wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authorizeURL := fmt.Sprintf(
		"/api/v1/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&workspace_slug=m12co&state=xyz",
		url.QueryEscape(app.App.ClientID),
		url.QueryEscape("http://localhost:9999/cb"),
		url.QueryEscape("chat:write channels:read bot"),
		url.QueryEscape(challenge),
	)
	rec := h.get(authorizeURL, adminResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "authorize: %s", rec.Body.String())
	var authz struct {
		RedirectTo string `json:"redirect_to"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &authz))
	assert.Contains(t, authz.RedirectTo, "code=")
	assert.Contains(t, authz.RedirectTo, "state=xyz")

	// Extract the code from the redirect URL
	u, err := url.Parse(authz.RedirectTo)
	require.NoError(t, err)
	code := u.Query().Get("code")
	require.NotEmpty(t, code)

	// Exchange the code for a token
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost:9999/cb")
	form.Set("client_id", app.App.ClientID)
	form.Set("code_verifier", verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(tokRec, req)
	require.Equal(t, http.StatusOK, tokRec.Code, "token: %s", tokRec.Body.String())

	var tok struct {
		AccessToken    string `json:"access_token"`
		TokenType      string `json:"token_type"`
		Scope          string `json:"scope"`
		InstallationID int64  `json:"installation_id"`
		BotUserID      *int64 `json:"bot_user_id"`
		AppID          int64  `json:"app_id"`
	}
	require.NoError(t, json.Unmarshal(tokRec.Body.Bytes(), &tok))
	assert.True(t, strings.HasPrefix(tok.AccessToken, "slis-xat-"), "token shape: %q", tok.AccessToken)
	assert.Equal(t, "Bearer", tok.TokenType)
	assert.Contains(t, tok.Scope, "chat:write")
	assert.Greater(t, tok.InstallationID, int64(0))
	require.NotNil(t, tok.BotUserID, "bot scope should provision a bot user")

	// Replay attack: same code a second time → 400
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/token", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusBadRequest, rec2.Code)
}

func TestM12P1OAuthUnregisteredRedirectURIRejected(t *testing.T) {
	h := newAppsHarness(t)
	devResp, _ := signup(t, h, "devops@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	app := createBasicApp(t, h, devResp.AccessToken, "strict-cb")

	adminResp, _ := signup(t, h, "admin@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "M12 Co", "m12co")

	authorizeURL := fmt.Sprintf(
		"/api/v1/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&workspace_slug=m12co",
		url.QueryEscape(app.App.ClientID),
		url.QueryEscape("http://evil.example/cb"),
		url.QueryEscape("chat:write"),
		url.QueryEscape("abc123"),
	)
	rec := h.get(authorizeURL, adminResp.AccessToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestM12P1OAuthNonAdminRejected(t *testing.T) {
	h := newAppsHarness(t)
	devResp, _ := signup(t, h, "devops@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	app := createBasicApp(t, h, devResp.AccessToken, "needs-admin")

	adminResp, _ := signup(t, h, "admin@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "M12 Co", "m12co")

	// Invite a non-admin member
	memberResp, _ := signup(t, h, "member@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	invRec := h.postAuth("/api/v1/workspaces/m12co/invites", "{}", adminResp.AccessToken)
	require.Equal(t, http.StatusCreated, invRec.Code)
	var inv struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(invRec.Body.Bytes(), &inv))
	rec := h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "{}", memberResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	authorizeURL := fmt.Sprintf(
		"/api/v1/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&workspace_slug=m12co",
		url.QueryEscape(app.App.ClientID),
		url.QueryEscape("http://localhost:9999/cb"),
		url.QueryEscape("chat:write"),
		url.QueryEscape("abc123"),
	)
	rec = h.get(authorizeURL, memberResp.AccessToken)
	assert.Equal(t, http.StatusForbidden, rec.Code, "non-admin install: %s", rec.Body.String())
}

func TestM12P1ListInstalledAppsAndUninstall(t *testing.T) {
	h := newAppsHarness(t)

	// Dev + app
	devResp, _ := signup(t, h, "dev@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	app := createBasicApp(t, h, devResp.AccessToken, "listable")

	// Admin + workspace + install
	adminResp, _ := signup(t, h, "admin@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "M12 Co", "m12co")

	verifier := "some-long-verifier-string-for-pkce-rfc7636-min-length-ok"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authorizeURL := fmt.Sprintf(
		"/api/v1/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&workspace_slug=m12co",
		url.QueryEscape(app.App.ClientID),
		url.QueryEscape("http://localhost:9999/cb"),
		url.QueryEscape("chat:write bot"),
		url.QueryEscape(challenge),
	)
	rec := h.get(authorizeURL, adminResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.get("/api/v1/workspaces/m12co/apps", adminResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1, "workspace should have one installed app")
	installationID := int64(list[0]["id"].(float64))

	// Uninstall
	uninst := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/installations/%d", installationID), nil)
	uninst.Header.Set("Authorization", "Bearer "+adminResp.AccessToken)
	uninstRec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(uninstRec, uninst)
	assert.Equal(t, http.StatusNoContent, uninstRec.Code)

	// List now empty
	rec = h.get("/api/v1/workspaces/m12co/apps", adminResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", strings.TrimSpace(rec.Body.String()))
}
