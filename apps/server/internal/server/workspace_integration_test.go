//go:build integration

package server_test

// Workspace + RLS integration tests. Includes the M2 acceptance gate: a
// cross-workspace RLS probe confirming user A cannot see user B's workspace
// or its rows via any query path, including crafted requests and raw SQL
// under the sliils_app role.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wsDTO struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type membershipDTO struct {
	Workspace wsDTO  `json:"workspace"`
	Role      string `json:"role"`
}

type channelDTO struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func createWorkspace(t *testing.T, h *testHarness, token, name, slug string) wsDTO {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"slug":%q,"description":"test"}`, name, slug)
	rec := h.postAuth("/api/v1/workspaces", body, token)
	require.Equal(t, http.StatusCreated, rec.Code, "create: %s", rec.Body.String())
	var ws wsDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&ws))
	return ws
}

func listMyWorkspaces(t *testing.T, h *testHarness, token string) []membershipDTO {
	t.Helper()
	rec := h.get("/api/v1/me/workspaces", token)
	require.Equal(t, http.StatusOK, rec.Code)
	var out []membershipDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	return out
}

func TestWorkspaceHappyPath(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "alice@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	assert.Empty(t, listMyWorkspaces(t, h, resp.AccessToken))

	ws := createWorkspace(t, h, resp.AccessToken, "Acme Inc", "acme")
	assert.Equal(t, "acme", ws.Slug)

	rec := h.get("/api/v1/me", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var me map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&me))
	assert.Equal(t, false, me["needs_setup"])

	ms := listMyWorkspaces(t, h, resp.AccessToken)
	require.Len(t, ms, 1)
	assert.Equal(t, "owner", ms[0].Role)
	assert.Equal(t, "Acme Inc", ms[0].Workspace.Name)

	rec = h.get("/api/v1/workspaces/acme/channels", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var channels []channelDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&channels))
	require.Len(t, channels, 1)
	assert.Equal(t, "general", channels[0].Name)
}

func TestWorkspaceDuplicateSlugRejected(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "bob@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	createWorkspace(t, h, resp.AccessToken, "Acme", "acme-dup")

	body := `{"name":"Second","slug":"acme-dup","description":""}`
	rec := h.postAuth("/api/v1/workspaces", body, resp.AccessToken)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestWorkspaceInvalidSlugRejected(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "carol@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	cases := []string{
		"",              // empty
		"A",             // too short (normalizes to "a", 1 char)
		"has spaces",    // spaces
		"-startshyphen", // leading hyphen
		"endshyphen-",   // trailing hyphen
		"api",           // reserved
		"setup",         // reserved
	}
	for _, slug := range cases {
		t.Run("slug="+slug, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"Test","slug":%q,"description":""}`, slug)
			rec := h.postAuth("/api/v1/workspaces", body, resp.AccessToken)
			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"slug %q should 400, got %d: %s", slug, rec.Code, rec.Body.String())
		})
	}
}

// TestRLSCrossWorkspaceProbe is the M2 acceptance gate. Two users, each
// with their own workspace. User A must not see B's data through any HTTP
// path OR raw SQL under sliils_app with A's GUC.
func TestRLSCrossWorkspaceProbe(t *testing.T) {
	h := newHarness(t)

	respA, _ := signup(t, h, "alice@probe.com", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob@probe.com", "correct-horse-battery-staple")
	drainEmails(h)

	wsA := createWorkspace(t, h, respA.AccessToken, "Alice Co", "alice-co")
	wsB := createWorkspace(t, h, respB.AccessToken, "Bob Co", "bob-co")

	// /me/workspaces only shows your own workspace.
	msA := listMyWorkspaces(t, h, respA.AccessToken)
	require.Len(t, msA, 1)
	assert.Equal(t, wsA.ID, msA[0].Workspace.ID)

	msB := listMyWorkspaces(t, h, respB.AccessToken)
	require.Len(t, msB, 1)
	assert.Equal(t, wsB.ID, msB[0].Workspace.ID)

	// Fetching a non-member workspace must 404, not 200, not 403.
	rec := h.get("/api/v1/workspaces/bob-co", respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"user A must not see user B's workspace (expected 404)")

	rec = h.get("/api/v1/workspaces/alice-co", respB.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Channel listing must also 404 for non-members (routes through the same
	// slug-resolve that RLS protects).
	rec = h.get("/api/v1/workspaces/bob-co/channels", respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Raw-SQL probe: with A's user_id GUC set under the sliils_app role,
	// a SELECT * FROM workspaces must only return A's row. This verifies
	// RLS is actually enforced at the DB layer, not just in handler logic.
	ctx := context.Background()
	conn, err := h.pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	rec = h.get("/api/v1/me", respA.AccessToken)
	var meA map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&meA))
	aUserID := fmt.Sprint(int64(meA["id"].(float64)))

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, aUserID)
	require.NoError(t, err)

	// Workspaces visible to A.
	rows, err := tx.Query(ctx, "SELECT slug FROM workspaces ORDER BY slug")
	require.NoError(t, err)
	var slugs []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		slugs = append(slugs, s)
	}
	rows.Close()
	assert.Contains(t, slugs, wsA.Slug, "A must see A's workspace")
	assert.NotContains(t, slugs, wsB.Slug,
		"A must NOT see B's workspace via raw SQL — RLS is broken")

	// Memberships visible to A.
	rows, err = tx.Query(ctx, "SELECT workspace_id FROM workspace_memberships")
	require.NoError(t, err)
	var mbIDs []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		mbIDs = append(mbIDs, id)
	}
	rows.Close()
	assert.Contains(t, mbIDs, wsA.ID)
	assert.NotContains(t, mbIDs, wsB.ID,
		"A must NOT see B's membership row — RLS is broken")
}
