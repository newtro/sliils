//go:build integration

package server_test

// M7A integration tests for workspace invites.
//
// Gates we care about:
//   - Admin can send an email-targeted invite and a link-only invite.
//   - Non-admin cannot send invites (403).
//   - Accepting an invite enrolls the caller and clears the default-channel
//     membership via the existing trigger.
//   - Second accept of the same token is a conflict, not a second row.
//   - Revoked invites cannot be accepted.
//   - Expired invites cannot be accepted.
//   - Email-targeted invite refuses a caller whose email doesn't match.
//   - Cross-workspace probe: admin of workspace A can't create invites
//     scoped to workspace B by slug-spoofing.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

// newInviteHarness reuses the base testHarness but also attaches an owner
// pool so the invite handler can run the accept path. The server under
// test is otherwise identical to the M1–M6 harness.
func newInviteHarness(t *testing.T) (*testHarness, *pgxpool.Pool) {
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
	// Invites don't need Meilisearch; keep search off so the harness
	// stays stable on machines without Meili running.
	t.Setenv("SLIILS_SEARCH_ENABLED", "false")

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

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}, ownerPool
}

type inviteDTO struct {
	ID          int64     `json:"id"`
	WorkspaceID int64     `json:"workspace_id"`
	Token       string    `json:"token"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type invitePreviewDTO struct {
	WorkspaceSlug string    `json:"workspace_slug"`
	WorkspaceName string    `json:"workspace_name"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	ExpiresAt     time.Time `json:"expires_at"`
	Accepted      bool      `json:"accepted"`
	Revoked       bool      `json:"revoked"`
	Expired       bool      `json:"expired"`
}

func TestInviteRoundtrip(t *testing.T) {
	h, _ := newInviteHarness(t)

	// Alice creates a workspace and sends herself an email invite for Bob.
	respA, _ := signup(t, h, "alice-inv@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "InviteCo", "invite-co")

	rec := h.postAuth("/api/v1/workspaces/invite-co/invites",
		`{"email":"bob-inv@example.com","role":"member"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var inv inviteDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &inv))
	assert.NotEmpty(t, inv.Token, "admin should receive token in response")
	assert.Equal(t, "bob-inv@example.com", inv.Email)
	assert.Equal(t, "member", inv.Role)
	assert.True(t, inv.ExpiresAt.After(time.Now().Add(6*24*time.Hour)))

	// Email was enqueued best-effort by the background goroutine; drain a
	// generous window.
	select {
	case msg := <-h.emails:
		assert.Contains(t, msg.Subject, "InviteCo")
		assert.Equal(t, []string{"bob-inv@example.com"}, msg.To)
	case <-time.After(2 * time.Second):
		t.Fatal("invite email was not enqueued")
	}

	// Preview endpoint works unauthenticated.
	rec = h.get("/api/v1/invites/"+inv.Token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var preview invitePreviewDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	assert.Equal(t, "invite-co", preview.WorkspaceSlug)
	assert.Equal(t, "InviteCo", preview.WorkspaceName)
	assert.Equal(t, "bob-inv@example.com", preview.Email)
	assert.False(t, preview.Accepted)

	// Bob signs up (gets `needs_setup: true`) and accepts.
	respB, _ := signup(t, h, "bob-inv@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "accept body: %s", rec.Body.String())
	var result struct {
		WorkspaceSlug string `json:"workspace_slug"`
		Role          string `json:"role"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, "invite-co", result.WorkspaceSlug)
	assert.Equal(t, "member", result.Role)

	// Bob's /me/workspaces now lists InviteCo.
	rec = h.get("/api/v1/me/workspaces", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var memberships []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &memberships))
	require.Len(t, memberships, 1)
	ws := memberships[0]["workspace"].(map[string]any)
	assert.Equal(t, "invite-co", ws["slug"])

	// And Bob can list channels in InviteCo — the default-channel
	// auto-enroll trigger put him in #general.
	rec = h.get("/api/v1/workspaces/invite-co/channels", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "list channels body: %s", rec.Body.String())

	// Second accept is a conflict.
	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respB.AccessToken)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestInviteLinkOnly(t *testing.T) {
	h, _ := newInviteHarness(t)
	respA, _ := signup(t, h, "alice-link@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "LinkCo", "link-co")

	rec := h.postAuth("/api/v1/workspaces/link-co/invites", `{"role":"guest"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var inv inviteDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &inv))
	assert.Empty(t, inv.Email, "link-only invite has no email")
	assert.Equal(t, "guest", inv.Role)

	// A random user with any email can claim the link.
	respX, _ := signup(t, h, "xavier@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respX.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestInviteEmailMismatch(t *testing.T) {
	h, _ := newInviteHarness(t)
	respA, _ := signup(t, h, "alice-mm@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "MmCo", "mm-co")

	rec := h.postAuth("/api/v1/workspaces/mm-co/invites",
		`{"email":"expected@example.com","role":"member"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var inv inviteDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &inv))

	// Wrong user tries to claim.
	respW, _ := signup(t, h, "wrong@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respW.AccessToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestInviteRevoked(t *testing.T) {
	h, _ := newInviteHarness(t)
	respA, _ := signup(t, h, "alice-rv@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "RvCo", "rv-co")

	rec := h.postAuth("/api/v1/workspaces/rv-co/invites", `{"role":"member"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var inv inviteDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &inv))

	// Revoke it.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/workspaces/rv-co/invites/%d", inv.ID), "", respA.AccessToken)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// Accept now returns 409.
	respB, _ := signup(t, h, "bob-rv@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	rec = h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respB.AccessToken)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestInviteNonAdminForbidden(t *testing.T) {
	h, _ := newInviteHarness(t)

	// Set up a two-user workspace with twoUserSharedWorkspace.
	_, tokB, _, _, _, _ := twoUserSharedWorkspace(t, h)

	// Bob is a plain 'member' (twoUserSharedWorkspace inserts 'member'). He
	// can't create invites.
	rec := h.postAuth("/api/v1/workspaces/shared-m4/invites",
		`{"email":"outsider@example.com"}`, tokB)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestInviteCrossWorkspaceSlugSpoof(t *testing.T) {
	h, _ := newInviteHarness(t)

	// Alice owns workspace A. Bob owns workspace B.
	respA, _ := signup(t, h, "alice-cw@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "WsA", "ws-a-cw")

	respB, _ := signup(t, h, "bob-cw@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respB.AccessToken, "WsB", "ws-b-cw")

	// Alice tries to create an invite in Bob's workspace by slug.
	rec := h.postAuth("/api/v1/workspaces/ws-b-cw/invites",
		`{"email":"outsider@example.com"}`, respA.AccessToken)
	// 404 from RLS (workspace not visible to Alice) is the right answer —
	// don't even leak workspace existence across tenants. Accept either
	// 404 or 403 depending on future policy choices.
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 404 or 403 for cross-workspace spoof, got %d body=%s",
			rec.Code, rec.Body.String())
	}
}
