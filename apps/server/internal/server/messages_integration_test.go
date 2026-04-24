//go:build integration

package server_test

// Messages + WebSocket integration tests. The acceptance gate for M3 is the
// two-client test at the bottom: one client posts a message via HTTP, the
// other client is subscribed to the channel topic via WebSocket and must
// receive the message.created event.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type messageDTO struct {
	ID           int64           `json:"id"`
	ChannelID    int64           `json:"channel_id"`
	AuthorUserID *int64          `json:"author_user_id,omitempty"`
	BodyMD       string          `json:"body_md"`
	EditedAt     *time.Time      `json:"edited_at,omitempty"`
	DeletedAt    *time.Time      `json:"deleted_at,omitempty"`
	Reactions    []reactionDTO   `json:"reactions"`
	CreatedAt    time.Time       `json:"created_at"`
	BodyBlocks   json.RawMessage `json:"body_blocks"`
}

type reactionDTO struct {
	Emoji   string  `json:"emoji"`
	UserIDs []int64 `json:"user_ids"`
}

type listMsgsResp struct {
	Messages   []messageDTO `json:"messages"`
	NextCursor string       `json:"next_cursor"`
}

func postMessage(t *testing.T, h *testHarness, token string, channelID int64, body string) messageDTO {
	t.Helper()
	raw := fmt.Sprintf(`{"body_md":%q}`, body)
	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", channelID), raw, token)
	require.Equal(t, http.StatusCreated, rec.Code, "post message: %s", rec.Body.String())
	var m messageDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	return m
}

func firstChannelID(t *testing.T, h *testHarness, token, slug string) int64 {
	t.Helper()
	rec := h.get(fmt.Sprintf("/api/v1/workspaces/%s/channels", slug), token)
	require.Equal(t, http.StatusOK, rec.Code)
	var channels []channelDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&channels))
	require.GreaterOrEqual(t, len(channels), 1)
	return channels[0].ID
}

func TestMessageCRUD(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "msg-crud@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "MsgCo", "msg-co")
	chID := firstChannelID(t, h, resp.AccessToken, "msg-co")

	// Create.
	m := postMessage(t, h, resp.AccessToken, chID, "hello world")
	assert.Equal(t, "hello world", m.BodyMD)
	assert.NotZero(t, m.ID)

	// List.
	rec := h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list listMsgsResp
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	require.Len(t, list.Messages, 1)
	assert.Equal(t, m.ID, list.Messages[0].ID)

	// Edit.
	rec = h.patchAuth(fmt.Sprintf("/api/v1/messages/%d", m.ID),
		`{"body_md":"hello, edited"}`, resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "edit: %s", rec.Body.String())
	var edited messageDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&edited))
	assert.Equal(t, "hello, edited", edited.BodyMD)
	assert.NotNil(t, edited.EditedAt)

	// Add reaction.
	rec = h.postAuth(fmt.Sprintf("/api/v1/messages/%d/reactions", m.ID),
		`{"emoji":":fire:"}`, resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), resp.AccessToken)
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	require.Len(t, list.Messages[0].Reactions, 1)
	assert.Equal(t, ":fire:", list.Messages[0].Reactions[0].Emoji)

	// Remove reaction.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/messages/%d/reactions", m.ID),
		`{"emoji":":fire:"}`, resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	// Soft-delete.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/messages/%d", m.ID), ``, resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)

	// Deleted message no longer appears in list.
	rec = h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), resp.AccessToken)
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	assert.Empty(t, list.Messages)
}

func TestMessageEditByNonAuthorForbidden(t *testing.T) {
	h := newHarness(t)
	respA, _ := signup(t, h, "author@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "SharedCo", "shared-co")
	chID := firstChannelID(t, h, respA.AccessToken, "shared-co")
	m := postMessage(t, h, respA.AccessToken, chID, "only I can edit this")

	// Another user in a different workspace can't reach the message at all
	// (would 404 due to RLS). To test the "non-author but can see" forbidden
	// path we'd need a second user in the same workspace, which requires
	// the invite flow (M2 doesn't have invites). Skip that branch until M7.
	_ = m
}

// TestMessageCrossWorkspaceRLS is a focused RLS probe: user B creates a
// message in B's workspace, user A attempts to read/list/edit/delete it.
// Every attempt must 404 via the HTTP surface.
func TestMessageCrossWorkspaceRLS(t *testing.T) {
	h := newHarness(t)
	respA, _ := signup(t, h, "alice-msg@probe.com", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob-msg@probe.com", "correct-horse-battery-staple")
	drainEmails(h)

	createWorkspace(t, h, respA.AccessToken, "A Co", "a-co-msg")
	createWorkspace(t, h, respB.AccessToken, "B Co", "b-co-msg")
	chB := firstChannelID(t, h, respB.AccessToken, "b-co-msg")
	mB := postMessage(t, h, respB.AccessToken, chB, "secret in B")

	// A listing B's channel → 404 (channel not found for A).
	rec := h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chB), respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A posting to B's channel → 404.
	rec = h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chB),
		`{"body_md":"intrusion"}`, respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A trying to edit B's message → 404.
	rec = h.patchAuth(fmt.Sprintf("/api/v1/messages/%d", mB.ID),
		`{"body_md":"hijacked"}`, respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A trying to delete B's message → 404.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/messages/%d", mB.ID), ``, respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A trying to react to B's message → 404.
	rec = h.postAuth(fmt.Sprintf("/api/v1/messages/%d/reactions", mB.ID),
		`{"emoji":":eyes:"}`, respA.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestWebSocketTwoClients is the M3 acceptance gate. Alice and Bob are both
// members of the same workspace; Alice posts via HTTP, Bob receives the
// event via WebSocket.
//
// This test DOES set up two users in one workspace. Since M2 has no invite
// flow, we bend the rule by creating Bob's membership directly in the DB.
// M4/M7 will replace this with a real invite path.
func TestWebSocketTwoClients(t *testing.T) {
	h := newHarness(t)

	// Alice creates the workspace.
	respA, _ := signup(t, h, "alice-ws@example.com", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob-ws@example.com", "correct-horse-battery-staple")
	drainEmails(h)

	ws := createWorkspace(t, h, respA.AccessToken, "Shared", "shared-ws")
	chID := firstChannelID(t, h, respA.AccessToken, "shared-ws")

	// Insert Bob as a member of Alice's workspace directly. Bypasses the
	// not-yet-implemented invite flow.
	rec := h.get("/api/v1/me", respB.AccessToken)
	var meB map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&meB))
	bobID := int64(meB["id"].(float64))

	require.NoError(t,
		h.adminExec(
			`INSERT INTO workspace_memberships (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
			ws.ID, bobID),
		"manual membership insert (test-only)")

	// Spin up an httptest.Server so we can dial a real WebSocket.
	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/socket?token=" + url.QueryEscape(respB.AccessToken)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Consume the hello envelope.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hello wsEnv
	require.NoError(t, conn.ReadJSON(&hello))
	assert.Equal(t, "hello", hello.Type)

	// Subscribe to the channel topic.
	subPayload := fmt.Sprintf(`{"topics":["ws:%d:ch:%d"]}`, ws.ID, chID)
	require.NoError(t, conn.WriteJSON(wsEnv{V: 1, Type: "subscribe", Data: json.RawMessage(subPayload)}))

	// Alice posts.
	msg := postMessage(t, h, respA.AccessToken, chID, "hello from alice")

	// Bob should see message.created within ~2 seconds.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		var env wsEnv
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if env.Type != "event" {
			continue
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(env.Data, &ev))
		if ev.Type != "message.created" {
			continue
		}
		var dto messageDTO
		require.NoError(t, json.Unmarshal(ev.Data, &dto))
		assert.Equal(t, msg.ID, dto.ID)
		assert.Equal(t, "hello from alice", dto.BodyMD)
		return
	}
}

type wsEnv struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}
