//go:build integration

package server_test

// M12-P3 integration tests: admin dashboard (members + audit + settings + export).

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAdminHarness reuses the apps harness — no app-platform state is
// needed for most P3 tests but wiring is identical.
func newAdminHarness(t *testing.T) *testHarness {
	h := newAppsHarness(t)
	// Temporarily swap the logger to stdout so failing tests can show
	// the underlying cause.
	return h
}

// makeWorkspaceWithMember returns:
//   adminToken, memberToken, memberUserID
func makeWorkspaceWithMember(t *testing.T, h *testHarness, slug string) (string, string, int64) {
	t.Helper()
	adminResp, _ := signup(t, h, "admin-p3@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "P3 Co", slug)

	// Invite + accept
	invRec := h.postAuth("/api/v1/workspaces/"+slug+"/invites", "{}", adminResp.AccessToken)
	require.Equal(t, http.StatusCreated, invRec.Code)
	var inv struct{ Token string `json:"token"` }
	require.NoError(t, json.Unmarshal(invRec.Body.Bytes(), &inv))

	memberResp, _ := signup(t, h, "member-p3@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	rec := h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "{}", memberResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	// Find member id via /me
	rec = h.get("/api/v1/me", memberResp.AccessToken)
	var me struct{ ID int64 `json:"id"` }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))

	return adminResp.AccessToken, memberResp.AccessToken, me.ID
}

// ---- members -----------------------------------------------------------

func TestM12P3ListMembersAdminOnly(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, memberToken, _ := makeWorkspaceWithMember(t, h, "p3co")

	// Admin can list
	rec := h.get("/api/v1/workspaces/p3co/admin/members", adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "admin list: %s", rec.Body.String())
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)

	// Member cannot
	rec = h.get("/api/v1/workspaces/p3co/admin/members", memberToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestM12P3PromoteAndDemote(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, _, memberID := makeWorkspaceWithMember(t, h, "p3co")

	// Promote to admin
	rec := h.patchAuth(fmt.Sprintf("/api/v1/workspaces/p3co/admin/members/%d", memberID),
		`{"role":"admin"}`, adminToken)
	assert.Equal(t, http.StatusNoContent, rec.Code, "promote: %s", rec.Body.String())

	// Confirm via list
	rec = h.get("/api/v1/workspaces/p3co/admin/members", adminToken)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	found := false
	for _, m := range list {
		if int64(m["user_id"].(float64)) == memberID {
			assert.Equal(t, "admin", m["role"])
			found = true
		}
	}
	assert.True(t, found)

	// Invalid role → 400
	rec = h.patchAuth(fmt.Sprintf("/api/v1/workspaces/p3co/admin/members/%d", memberID),
		`{"role":"god"}`, adminToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestM12P3CannotDemoteLastOwner(t *testing.T) {
	h := newAdminHarness(t)
	adminResp, _ := signup(t, h, "only-owner@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "Solo", "solo")

	rec := h.get("/api/v1/me", adminResp.AccessToken)
	var me struct{ ID int64 `json:"id"` }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))

	// Self-demote to member with no other owners → 409
	rec = h.patchAuth(fmt.Sprintf("/api/v1/workspaces/solo/admin/members/%d", me.ID),
		`{"role":"member"}`, adminResp.AccessToken)
	assert.Equal(t, http.StatusConflict, rec.Code, "last-owner demote: %s", rec.Body.String())
}

func TestM12P3CannotDeactivateSelf(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, _, _ := makeWorkspaceWithMember(t, h, "p3co")
	rec := h.get("/api/v1/me", adminToken)
	var me struct{ ID int64 `json:"id"` }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))

	rec = httptestDelete(h, fmt.Sprintf("/api/v1/workspaces/p3co/admin/members/%d", me.ID), adminToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- audit log ---------------------------------------------------------

func TestM12P3AuditLogCapturesChanges(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, _, memberID := makeWorkspaceWithMember(t, h, "p3co")

	// Generate two audit events
	rec := h.patchAuth(fmt.Sprintf("/api/v1/workspaces/p3co/admin/members/%d", memberID),
		`{"role":"admin"}`, adminToken)
	require.Equal(t, http.StatusNoContent, rec.Code)
	rec = h.patchAuth("/api/v1/workspaces/p3co/admin/settings",
		`{"brand_color":"#ff8800","retention_days":30}`, adminToken)
	require.Equal(t, http.StatusNoContent, rec.Code, "settings: %s", rec.Body.String())

	rec = h.get("/api/v1/workspaces/p3co/admin/audit", adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "audit: %s", rec.Body.String())
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entries))
	// Most-recent first: settings update, then role change.
	require.GreaterOrEqual(t, len(entries), 2)
	actions := []string{entries[0]["action"].(string), entries[1]["action"].(string)}
	assert.Contains(t, actions, "workspace.settings_updated")
	assert.Contains(t, actions, "member.role_changed")
}

// ---- workspace settings ------------------------------------------------

func TestM12P3PatchWorkspaceSettings(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, _, _ := makeWorkspaceWithMember(t, h, "p3co")

	// Setting retention_days works
	rec := h.patchAuth("/api/v1/workspaces/p3co/admin/settings",
		`{"name":"Renamed Co","description":"a new desc","brand_color":"#123456","retention_days":14}`,
		adminToken)
	require.Equal(t, http.StatusNoContent, rec.Code, "settings: %s", rec.Body.String())

	// Clearing retention
	rec = h.patchAuth("/api/v1/workspaces/p3co/admin/settings",
		`{"clear_retention":true}`, adminToken)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ---- exports -----------------------------------------------------------

func TestM12P3WorkspaceExportZip(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, _, _ := makeWorkspaceWithMember(t, h, "p3co")

	// Post one message so the export has content
	chRec := h.get("/api/v1/workspaces/p3co/channels", adminToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(chRec.Body.Bytes(), &channels)
	require.NotEmpty(t, channels)
	chID := channels[0].ID
	msgRec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chID),
		`{"body_md":"hello from the export test"}`, adminToken)
	require.Equal(t, http.StatusCreated, msgRec.Code)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/p3co/admin/export", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	if rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export failed: code=%d body=%q ct=%q",
			rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))

	// Parse the zip
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)
	names := map[string]bool{}
	var msgsContent []byte
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "messages.ndjson" {
			r, err := f.Open()
			require.NoError(t, err)
			msgsContent, err = io.ReadAll(r)
			require.NoError(t, err)
			r.Close()
		}
	}
	assert.True(t, names["workspace.json"])
	assert.True(t, names["members.json"])
	assert.True(t, names["channels.json"])
	assert.True(t, names["messages.ndjson"])
	assert.Contains(t, string(msgsContent), "hello from the export test")
}

func TestM12P3UserExportZip(t *testing.T) {
	h := newAdminHarness(t)
	adminToken, memberToken, memberID := makeWorkspaceWithMember(t, h, "p3co")

	// Member posts a message
	chRec := h.get("/api/v1/workspaces/p3co/channels", memberToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(chRec.Body.Bytes(), &channels)
	chID := channels[0].ID
	_ = h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chID),
		`{"body_md":"my thoughts for the GDPR request"}`, memberToken)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/workspaces/p3co/admin/export/user/%d", memberID), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "user export: %s", rec.Body.String())

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)
	var profile, messages string
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		switch f.Name {
		case "profile.json":
			profile = string(b)
		case "messages.ndjson":
			messages = string(b)
		}
	}
	assert.Contains(t, profile, "member-p3@m12.test")
	assert.Contains(t, messages, "GDPR request")
}

// ---- helpers -----------------------------------------------------------

func httptestDelete(h *testHarness, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// keep `strings` import healthy
var _ = strings.Contains
