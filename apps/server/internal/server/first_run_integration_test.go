//go:build integration

package server_test

// Integration tests for the first-run bootstrap + super-admin guards.
// These live behind the `integration` build tag because they need a real
// Postgres so transactions and advisory-lock behaviour can be verified.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
)

// newFirstRunHarness wires a server with the install service + owner
// pool that first-run requires. Unlike the plain harness, this one
// does NOT pre-seed a user — the test IS the first-run.
func newFirstRunHarness(t *testing.T) *testHarness {
	t.Helper()
	h := newHarness(t)

	// Wire the install service + owner pool on top of the existing harness.
	// Reuse the same DSN for the owner pool so both pools hit the same DB.
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ownerPool, err := db.OpenOwner(ctx, h.dsn, logger)
	require.NoError(t, err)
	t.Cleanup(func() { ownerPool.Close() })

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"
	cfg.AllowDevOrigins = true

	emails := make(chan email.Message, 16)
	installSvc := install.NewService(ownerPool, nil)
	srv, err := server.New(cfg, logger, h.pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
		Install:       installSvc,
	})
	require.NoError(t, err)
	h.srv = srv
	h.emails = emails
	return h
}

func TestFirstRunBootstrapCreatesSuperAdminAndWorkspace(t *testing.T) {
	h := newFirstRunHarness(t)

	body := `{
		"admin": {"email": "admin@example.com", "password": "correct-horse-battery-staple", "display_name": "Admin"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Acme", "slug": "acme"}
	}`
	rec := h.post("/api/v1/first-run/bootstrap", body, nil)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["access_token"])
	assert.Equal(t, "acme", resp["workspace_slug"])

	// State reflects the bootstrap.
	rec = h.post("/api/v1/first-run/state", "", nil)
	// state is GET, not POST — re-do
	req := httptest.NewRequest(http.MethodGet, "/api/v1/first-run/state", nil)
	rec = httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var state map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &state))
	assert.Equal(t, true, state["completed"])
	assert.Equal(t, float64(1), state["users_count"])
}

func TestFirstRunBootstrapRejectsAlreadyInitialised(t *testing.T) {
	h := newFirstRunHarness(t)

	body := `{
		"admin": {"email": "admin@example.com", "password": "correct-horse-battery-staple", "display_name": "Admin"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Acme", "slug": "acme"}
	}`
	rec := h.post("/api/v1/first-run/bootstrap", body, nil)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Second call on an initialised install must be refused.
	body2 := `{
		"admin": {"email": "evil@example.com", "password": "correct-horse-battery-staple", "display_name": "Evil"},
		"email": {},
		"signup_mode": "open",
		"workspace": {"name": "Evil Co", "slug": "evil"}
	}`
	rec = h.post("/api/v1/first-run/bootstrap", body2, nil)
	assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestFirstRunBootstrapRaceOnlyOneWinner(t *testing.T) {
	h := newFirstRunHarness(t)

	// Two concurrent bootstraps with different emails. Only one should
	// succeed; the loser sees 409. The advisory lock serialises them.
	body1 := `{
		"admin": {"email": "a@example.com", "password": "correct-horse-battery-staple", "display_name": "Alice"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Alice Co", "slug": "alice"}
	}`
	body2 := `{
		"admin": {"email": "b@example.com", "password": "correct-horse-battery-staple", "display_name": "Bob"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Bob Co", "slug": "bob"}
	}`

	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0] = h.post("/api/v1/first-run/bootstrap", body1, nil).Code
	}()
	go func() {
		defer wg.Done()
		codes[1] = h.post("/api/v1/first-run/bootstrap", body2, nil).Code
	}()
	wg.Wait()

	successCount := 0
	conflictCount := 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			successCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("unexpected status: %d", c)
		}
	}
	assert.Equal(t, 1, successCount, "exactly one bootstrap should succeed")
	assert.Equal(t, 1, conflictCount, "the other should 409")

	// DB state: exactly one super-admin row.
	var count int
	err := h.pool.Pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE is_super_admin = true AND deactivated_at IS NULL").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "only one super-admin should exist")
}

func TestFirstRunBootstrapValidationRejectsBadInput(t *testing.T) {
	h := newFirstRunHarness(t)

	cases := map[string]string{
		"bad email": `{"admin": {"email": "not-an-email", "password": "correct-horse-battery-staple", "display_name": "A"}, "workspace": {"name": "A", "slug": "a1"}}`,
		"weak password": `{"admin": {"email": "a@example.com", "password": "short", "display_name": "A"}, "workspace": {"name": "A", "slug": "a2"}}`,
		"bad slug": `{"admin": {"email": "a@example.com", "password": "correct-horse-battery-staple", "display_name": "A"}, "workspace": {"name": "A", "slug": "A"}}`,
		"empty workspace name": `{"admin": {"email": "a@example.com", "password": "correct-horse-battery-staple", "display_name": "A"}, "workspace": {"name": "", "slug": "a3"}}`,
		"rtl workspace name": `{"admin": {"email": "a@example.com", "password": "correct-horse-battery-staple", "display_name": "A"}, "workspace": {"name": "A‮B", "slug": "a4"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := h.post("/api/v1/first-run/bootstrap", body, nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

// TestRequireSuperAdminGatesInstallEndpoints — a plain workspace owner
// is NOT a super-admin; the install-level endpoints must refuse them.
func TestRequireSuperAdminGatesInstallEndpoints(t *testing.T) {
	h := newFirstRunHarness(t)

	// Bootstrap as super-admin.
	body := `{
		"admin": {"email": "super@example.com", "password": "correct-horse-battery-staple", "display_name": "Super"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Co", "slug": "co"}
	}`
	rec := h.post("/api/v1/first-run/bootstrap", body, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var boot map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &boot))
	superTok := boot["access_token"].(string)

	// Create a second user via invite + signup who becomes workspace admin
	// but NOT super-admin.
	invRec := h.postAuth("/api/v1/workspaces/co/invites", `{"role":"admin"}`, superTok)
	require.Equal(t, http.StatusCreated, invRec.Code, "body: %s", invRec.Body.String())
	var inv struct{ Token string `json:"token"` }
	require.NoError(t, json.Unmarshal(invRec.Body.Bytes(), &inv))

	// Signup as the invitee (invite-only mode: token required).
	signupBody := `{"email":"member@example.com","password":"correct-horse-battery-staple","display_name":"Member","invite_token":"` + inv.Token + `"}`
	rec = h.post("/api/v1/auth/signup", signupBody, nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var session sessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &session))

	// Accept the invite so they become a workspace admin.
	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "{}", session.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Workspace-admin but not super-admin: /install/* should all 403.
	cases := []struct {
		method, path, body string
	}{
		{"GET", "/api/v1/install/signup-mode", ""},
		{"GET", "/api/v1/install/email", ""},
		{"GET", "/api/v1/install/infrastructure", ""},
		{"GET", "/api/v1/install/super-admins", ""},
		{"POST", "/api/v1/install/vapid/generate", ""},
	}
	for _, tc := range cases {
		rec := h.authJSON(tc.method, tc.path, tc.body, session.AccessToken)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", tc.method, tc.path, rec.Body.String())
	}

	// Super-admin can reach the same endpoints.
	rec = h.get("/api/v1/install/signup-mode", superTok)
	assert.Equal(t, http.StatusOK, rec.Code, "super-admin should reach install endpoints: %s", rec.Body.String())
}

// TestLastSuperAdminCannotBeDemoted — demoting the only active
// super-admin must be refused so an operator cannot accidentally lock
// the install out of every /install/* endpoint.
func TestLastSuperAdminCannotBeDemoted(t *testing.T) {
	h := newFirstRunHarness(t)

	body := `{
		"admin": {"email": "solo@example.com", "password": "correct-horse-battery-staple", "display_name": "Solo"},
		"email": {},
		"signup_mode": "invite_only",
		"workspace": {"name": "Co", "slug": "co"}
	}`
	rec := h.post("/api/v1/first-run/bootstrap", body, nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	var boot map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &boot))
	tok := boot["access_token"].(string)
	uid := int64(boot["user_id"].(float64))

	// List super-admins should show exactly one.
	rec = h.get("/api/v1/install/super-admins", tok)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)

	// Demote self = demote the last super-admin = 409.
	path := "/api/v1/install/super-admins/" + strconv.FormatInt(uid, 10) + "/demote"
	rec = h.postAuth(path, "", tok)
	assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

	// DB state: the one super-admin still exists.
	var count int
	err := h.pool.Pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE is_super_admin = true AND deactivated_at IS NULL").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

