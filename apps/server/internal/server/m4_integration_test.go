//go:build integration

package server_test

// M4 feature tests: mentions, threads, mark-read + unread counts, and
// presence/typing. These lean on the same testHarness used for M3.

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

// twoUserSharedWorkspace sets up Alice + Bob both in one workspace so we
// can exercise cross-user features. Returns (alice-token, bob-token, ws-id,
// channel-id, alice-user-id, bob-user-id).
func twoUserSharedWorkspace(t *testing.T, h *testHarness) (string, string, int64, int64, int64, int64) {
	t.Helper()
	respA, _ := signup(t, h, "alice@m4.test", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob@m4.test", "correct-horse-battery-staple")
	drainEmails(h)

	ws := createWorkspace(t, h, respA.AccessToken, "Shared", "shared-m4")
	chID := firstChannelID(t, h, respA.AccessToken, "shared-m4")

	meRec := h.get("/api/v1/me", respA.AccessToken)
	var meA map[string]any
	require.NoError(t, json.NewDecoder(meRec.Body).Decode(&meA))
	aID := int64(meA["id"].(float64))

	meRec = h.get("/api/v1/me", respB.AccessToken)
	var meB map[string]any
	require.NoError(t, json.NewDecoder(meRec.Body).Decode(&meB))
	bID := int64(meB["id"].(float64))

	require.NoError(t, h.adminExec(
		`INSERT INTO workspace_memberships (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		ws.ID, bID,
	), "manual membership insert (M7 adds real invite flow)")

	return respA.AccessToken, respB.AccessToken, ws.ID, chID, aID, bID
}

func TestMentionParsedAndBroadcast(t *testing.T) {
	h := newHarness(t)
	tokA, tokB, _, chID, _, bID := twoUserSharedWorkspace(t, h)

	body := fmt.Sprintf(`{"body_md":"hey <@%d> check this out"}`, bID)
	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chID), body, tokA)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var m struct {
		ID       int64   `json:"id"`
		Mentions []int64 `json:"mentions"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	assert.Equal(t, []int64{bID}, m.Mentions, "mention resolved to bob's user id")
	_ = tokB

	// Unread + mention counts for Bob in the shared channel.
	rec = h.get("/api/v1/workspaces/shared-m4/channels", tokB)
	require.Equal(t, http.StatusOK, rec.Code)
	var channels []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&channels))
	require.Len(t, channels, 1)
	assert.EqualValues(t, 1, channels[0]["unread_count"])
	assert.EqualValues(t, 1, channels[0]["mention_count"])
}

func TestThreadRoundtrip(t *testing.T) {
	h := newHarness(t)
	tokA, _, _, chID, _, _ := twoUserSharedWorkspace(t, h)

	root := postMessage(t, h, tokA, chID, "thread root here")

	body := fmt.Sprintf(`{"body_md":"first reply","parent_id":%d}`, root.ID)
	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chID), body, tokA)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	var reply struct {
		ID           int64  `json:"id"`
		ThreadRootID *int64 `json:"thread_root_id"`
		ParentID     *int64 `json:"parent_id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&reply))
	require.NotNil(t, reply.ThreadRootID)
	assert.Equal(t, root.ID, *reply.ThreadRootID)
	assert.Equal(t, root.ID, *reply.ParentID)

	// Channel list should NOT include the reply (thread replies are only
	// visible inside the thread view).
	rec = h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), tokA)
	var list listMsgsResp
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	require.Len(t, list.Messages, 1, "reply must be hidden from channel feed")
	assert.Equal(t, root.ID, list.Messages[0].ID)

	// GET thread returns root + replies with reply_count.
	rec = h.get(fmt.Sprintf("/api/v1/messages/%d/thread", root.ID), tokA)
	require.Equal(t, http.StatusOK, rec.Code)
	var thread struct {
		Root struct {
			ID         int64 `json:"id"`
			ReplyCount int64 `json:"reply_count"`
		} `json:"root"`
		Replies []messageDTO `json:"replies"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&thread))
	assert.Equal(t, root.ID, thread.Root.ID)
	assert.EqualValues(t, 1, thread.Root.ReplyCount)
	require.Len(t, thread.Replies, 1)
	assert.Equal(t, reply.ID, thread.Replies[0].ID)
}

func TestMarkReadClearsUnread(t *testing.T) {
	h := newHarness(t)
	tokA, tokB, _, chID, _, _ := twoUserSharedWorkspace(t, h)

	m1 := postMessage(t, h, tokA, chID, "first")
	postMessage(t, h, tokA, chID, "second")
	postMessage(t, h, tokA, chID, "third")

	rec := h.get("/api/v1/workspaces/shared-m4/channels", tokB)
	var channels []map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&channels))
	assert.EqualValues(t, 3, channels[0]["unread_count"])

	// Mark up through the first message only.
	body := fmt.Sprintf(`{"message_id":%d}`, m1.ID)
	rec = h.postAuth(fmt.Sprintf("/api/v1/channels/%d/mark-read", chID), body, tokB)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = h.get("/api/v1/workspaces/shared-m4/channels", tokB)
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&channels))
	assert.EqualValues(t, 2, channels[0]["unread_count"], "2 of 3 remain after marking first as read")
}

// waitForEvent reads WS envelopes until one with type=event and the given
// inner event type arrives, or the deadline passes. Skips anything else
// (hello, presence, pongs, other event types). Returns the event's data.
func waitForEvent(t *testing.T, c *websocket.Conn, eventType string, within time.Duration) json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timeout waiting for %q", eventType)
		}
		c.SetReadDeadline(time.Now().Add(remaining))
		var env wsEnv
		if err := c.ReadJSON(&env); err != nil {
			t.Fatalf("ws read waiting for %q: %v", eventType, err)
		}
		if env.Type != "event" {
			continue
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(env.Data, &ev); err != nil {
			continue
		}
		if ev.Type == eventType {
			return ev.Data
		}
	}
}

// dialWS opens a WebSocket, consumes the hello envelope, subscribes to the
// given topics, and returns the connection ready for event reads.
func dialWS(t *testing.T, ts *httptest.Server, token string, topics []string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/socket?token=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	require.NoError(t, err)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	var env wsEnv
	require.NoError(t, conn.ReadJSON(&env))
	require.Equal(t, "hello", env.Type)

	topicJSON, _ := json.Marshal(map[string]any{"topics": topics})
	require.NoError(t, conn.WriteJSON(wsEnv{V: 1, Type: "subscribe", Data: topicJSON}))
	return conn
}

func TestPresenceOnline(t *testing.T) {
	h := newHarness(t)
	tokA, tokB, wsID, chID, _, _ := twoUserSharedWorkspace(t, h)

	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()

	topics := []string{fmt.Sprintf("ws:%d", wsID), fmt.Sprintf("ws:%d:ch:%d", wsID, chID)}

	connA := dialWS(t, ts, tokA, topics)
	defer connA.Close()

	// B comes online after A is already subscribed; A must receive a
	// presence.changed event with status=online.
	connB := dialWS(t, ts, tokB, topics)
	defer connB.Close()

	data := waitForEvent(t, connA, "presence.changed", 3*time.Second)
	var presence struct {
		Status string `json:"status"`
		UserID int64  `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(data, &presence))
	assert.Equal(t, "online", presence.Status)
}

func TestTypingBroadcast(t *testing.T) {
	h := newHarness(t)
	tokA, tokB, wsID, chID, _, _ := twoUserSharedWorkspace(t, h)

	ts := httptest.NewServer(h.srv.Handler())
	defer ts.Close()

	topics := []string{fmt.Sprintf("ws:%d", wsID), fmt.Sprintf("ws:%d:ch:%d", wsID, chID)}
	connA := dialWS(t, ts, tokA, topics)
	defer connA.Close()
	connB := dialWS(t, ts, tokB, topics)
	defer connB.Close()

	// Give both subscriptions a beat to settle so A's typing heartbeat
	// after this point reliably finds B subscribed at the broker.
	time.Sleep(50 * time.Millisecond)

	typingPayload := fmt.Sprintf(`{"workspace_id":%d,"channel_id":%d}`, wsID, chID)
	require.NoError(t, connA.WriteJSON(wsEnv{V: 1, Type: "typing.heartbeat", Data: json.RawMessage(typingPayload)}))

	data := waitForEvent(t, connB, "typing.started", 3*time.Second)
	var tpevt struct {
		ChannelID int64 `json:"channel_id"`
		UserID    int64 `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(data, &tpevt))
	assert.Equal(t, chID, tpevt.ChannelID)

	require.NoError(t, connA.WriteJSON(wsEnv{V: 1, Type: "typing.stopped", Data: json.RawMessage(typingPayload)}))
	waitForEvent(t, connB, "typing.stopped", 3*time.Second)
}
