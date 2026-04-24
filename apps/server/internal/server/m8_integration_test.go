//go:build integration

package server_test

// M8 integration tests: DMs + meetings.
//
// LiveKit doesn't need to be running for these. The call handler mints
// a JWT locally using the same HMAC secret LiveKit would verify with, so
// we just validate claims end-to-end. The EndMeeting goroutine tries to
// reach LiveKit; that failure is logged-not-fatal by design, so these
// tests pass without a running LiveKit (it logs a warning that the
// harness's io.Discard slog swallows).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/calls"
	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

func newCallHarness(t *testing.T) *testHarness {
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

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"
	cfg.CallsEnabled = true
	cfg.CallJoinTokenTTL = 30 * time.Minute

	// Real calls.Client with a deterministic dev key pair. The test never
	// talks to LiveKit — it just verifies the server hands back a JWT
	// whose claims are correctly shaped and HMAC-signed with our secret.
	cc, err := calls.NewClient(calls.Options{
		APIKey:    "devkey",
		APISecret: "sliils-local-livekit-secret-0123456789abcdef0123456789abcdef",
		HTTPURL:   "http://127.0.0.1:7880",
		WSURL:     "ws://127.0.0.1:7880",
		Logger:    logger,
	})
	require.NoError(t, err)

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
		CallsClient:   cc,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		ownerPool.Close()
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}
}

// --- tests --------------------------------------------------------------

func TestDMFindOrCreateIdempotent(t *testing.T) {
	h := newCallHarness(t)
	tokA, tokB, aliceID, bobID, _ := sharedWorkspaceViaInvite(t, h, "m8-dm")

	// Alice creates DM with Bob.
	rec := h.postAuth("/api/v1/workspaces/m8-dm/dms", fmt.Sprintf(`{"user_id":%d}`, bobID), tokA)
	require.Equal(t, http.StatusOK, rec.Code, "create dm: %s", rec.Body.String())
	var dm1 struct {
		ChannelID        int64  `json:"channel_id"`
		OtherUserID      int64  `json:"other_user_id"`
		OtherDisplayName string `json:"other_display_name"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dm1))
	assert.Equal(t, bobID, dm1.OtherUserID)
	assert.NotZero(t, dm1.ChannelID)

	// Second call returns the same channel.
	rec = h.postAuth("/api/v1/workspaces/m8-dm/dms", fmt.Sprintf(`{"user_id":%d}`, bobID), tokA)
	require.Equal(t, http.StatusOK, rec.Code)
	var dm2 struct {
		ChannelID int64 `json:"channel_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dm2))
	assert.Equal(t, dm1.ChannelID, dm2.ChannelID, "duplicate DM channel")

	// Bob creating DM with Alice returns the same channel too (pair is symmetric).
	rec = h.postAuth("/api/v1/workspaces/m8-dm/dms", fmt.Sprintf(`{"user_id":%d}`, aliceID), tokB)
	require.Equal(t, http.StatusOK, rec.Code)
	var dm3 struct {
		ChannelID int64 `json:"channel_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dm3))
	assert.Equal(t, dm1.ChannelID, dm3.ChannelID, "symmetric DM returns same channel")

	// Both see the DM in listDMs.
	for _, tok := range []string{tokA, tokB} {
		rec = h.get("/api/v1/workspaces/m8-dm/dms", tok)
		require.Equal(t, http.StatusOK, rec.Code)
		var list []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		require.Len(t, list, 1)
	}
}

func TestMeetingLifecycle(t *testing.T) {
	h := newCallHarness(t)
	tokA, tokB, _, _, chID := sharedWorkspaceViaInvite(t, h, "m8-meet")

	// Start a meeting.
	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/meetings", chID), "", tokA)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var m1 struct {
		ID          int64  `json:"id"`
		LiveKitRoom string `json:"livekit_room"`
		ChannelID   int64  `json:"channel_id"`
		EndedAt     string `json:"ended_at"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m1))
	assert.NotZero(t, m1.ID)
	assert.Equal(t, fmt.Sprintf("sliils-meeting-%d", m1.ID), m1.LiveKitRoom)
	assert.Empty(t, m1.EndedAt)

	// Idempotent: second call returns same meeting.
	rec = h.postAuth(fmt.Sprintf("/api/v1/channels/%d/meetings", chID), "", tokB)
	require.Equal(t, http.StatusOK, rec.Code)
	var m2 struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m2))
	assert.Equal(t, m1.ID, m2.ID)

	// Both participants get join tokens.
	rec = h.postAuth(fmt.Sprintf("/api/v1/meetings/%d/join", m1.ID), "", tokA)
	require.Equal(t, http.StatusOK, rec.Code)
	var joinA struct {
		Token    string `json:"token"`
		WSURL    string `json:"ws_url"`
		Room     string `json:"livekit_room"`
		Identity string `json:"identity"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &joinA))
	require.NotEmpty(t, joinA.Token)
	assert.Equal(t, "ws://127.0.0.1:7880", joinA.WSURL)
	assert.Equal(t, m1.LiveKitRoom, joinA.Room)
	assertJWTPayload(t, joinA.Token, m1.LiveKitRoom, true /* roomAdmin for the starter */)

	rec = h.postAuth(fmt.Sprintf("/api/v1/meetings/%d/join", m1.ID), "", tokB)
	require.Equal(t, http.StatusOK, rec.Code)
	var joinB struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &joinB))
	assertJWTPayload(t, joinB.Token, m1.LiveKitRoom, false /* Bob is NOT the starter */)

	// End the meeting.
	rec = h.postAuth(fmt.Sprintf("/api/v1/meetings/%d/end", m1.ID), "", tokA)
	require.Equal(t, http.StatusOK, rec.Code, "end: %s", rec.Body.String())

	// Channel now carries a "Call ended" system message.
	rec = h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), tokA)
	require.Equal(t, http.StatusOK, rec.Code)
	var listed struct {
		Messages []struct {
			BodyMD       string `json:"body_md"`
			AuthorUserID *int64 `json:"author_user_id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.NotEmpty(t, listed.Messages)
	last := listed.Messages[len(listed.Messages)-1]
	assert.Contains(t, last.BodyMD, "Call ended")
	assert.Nil(t, last.AuthorUserID, "system message has no author")

	// Joining after end returns 409.
	rec = h.postAuth(fmt.Sprintf("/api/v1/meetings/%d/join", m1.ID), "", tokA)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestMeetingJoinNonMemberForbidden(t *testing.T) {
	h := newCallHarness(t)
	tokA, _, _, _, chID := sharedWorkspaceViaInvite(t, h, "m8-forbid")

	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/meetings", chID), "", tokA)
	require.Equal(t, http.StatusOK, rec.Code)
	var m struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))

	// Some stranger who isn't in the workspace signs up.
	respX, _ := signup(t, h, "stranger@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	rec = h.postAuth(fmt.Sprintf("/api/v1/meetings/%d/join", m.ID), "", respX.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// sharedWorkspaceViaInvite builds a two-user workspace using the M7
// invite flow (not the older adminExec pattern), so these tests also
// double-check that the invite + DM + call interaction lights up.
func sharedWorkspaceViaInvite(t *testing.T, h *testHarness, slug string) (
	tokA, tokB string, aliceID, bobID int64, chID int64,
) {
	t.Helper()
	respA, _ := signup(t, h, "alice-"+slug+"@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "M8 Team "+slug, slug)
	aliceID = int64(respA.User["id"].(float64))

	invRec := h.postAuth("/api/v1/workspaces/"+slug+"/invites",
		`{"email":"bob-`+slug+`@example.com","role":"member"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, invRec.Code)
	var inv struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(invRec.Body.Bytes(), &inv))

	respB, _ := signup(t, h, "bob-"+slug+"@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	accRec := h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respB.AccessToken)
	require.Equal(t, http.StatusOK, accRec.Code)
	bobID = int64(respB.User["id"].(float64))

	chID = firstChannelID(t, h, respA.AccessToken, slug)
	return respA.AccessToken, respB.AccessToken, aliceID, bobID, chID
}

// assertJWTPayload decodes the (unverified) payload portion of a LiveKit
// token and checks the video grant. Full HMAC verification happens on the
// LiveKit side at connect time; this is enough to catch shape regressions.
func assertJWTPayload(t *testing.T, token, expectedRoom string, expectRoomAdmin bool) {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT should have three segments")
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims struct {
		Iss   string `json:"iss"`
		Sub   string `json:"sub"`
		Video struct {
			Room           string `json:"room"`
			RoomJoin       bool   `json:"roomJoin"`
			RoomAdmin      bool   `json:"roomAdmin"`
			CanPublish     bool   `json:"canPublish"`
			CanSubscribe   bool   `json:"canSubscribe"`
			CanPublishData bool   `json:"canPublishData"`
		} `json:"video"`
	}
	require.NoError(t, json.Unmarshal(b, &claims))
	assert.Equal(t, "devkey", claims.Iss)
	assert.NotEmpty(t, claims.Sub, "identity must be set")
	assert.Equal(t, expectedRoom, claims.Video.Room)
	assert.True(t, claims.Video.RoomJoin)
	assert.True(t, claims.Video.CanPublish)
	assert.True(t, claims.Video.CanSubscribe)
	assert.Equal(t, expectRoomAdmin, claims.Video.RoomAdmin)
}
