package realtime

import (
	"sync"
	"time"
)

// Typing debounces typing.heartbeat events from clients and broadcasts
// typing.started / typing.stopped transitions. Each (channel, user) pair
// has its own timer; receiving a heartbeat while the timer is live resets
// it without re-broadcasting started.
type Typing struct {
	mu      sync.Mutex
	active  map[typingKey]*time.Timer
	broker  *Broker
	timeout time.Duration
}

type typingKey struct {
	WorkspaceID int64
	ChannelID   int64
	UserID      int64
}

type typingEvent struct {
	WorkspaceID int64 `json:"workspace_id"`
	ChannelID   int64 `json:"channel_id"`
	UserID      int64 `json:"user_id"`
}

func NewTyping(broker *Broker) *Typing {
	return &Typing{
		active:  make(map[typingKey]*time.Timer),
		broker:  broker,
		timeout: 5 * time.Second,
	}
}

// Heartbeat records typing activity for a (workspace, channel, user).
// Emits typing.started on first call; subsequent calls within `timeout`
// just extend the expiry. When the timer fires with no further heartbeats
// we emit typing.stopped.
func (t *Typing) Heartbeat(workspaceID, channelID, userID int64) {
	key := typingKey{WorkspaceID: workspaceID, ChannelID: channelID, UserID: userID}

	t.mu.Lock()
	existing := t.active[key]
	if existing == nil {
		t.active[key] = time.AfterFunc(t.timeout, func() { t.expire(key) })
		t.mu.Unlock()
		t.publish(workspaceID, channelID, userID, "typing.started")
		return
	}
	existing.Reset(t.timeout)
	t.mu.Unlock()
}

// Stop immediately ends the typing state and broadcasts typing.stopped.
// Called when the client sends a message or clears the composer explicitly.
func (t *Typing) Stop(workspaceID, channelID, userID int64) {
	key := typingKey{WorkspaceID: workspaceID, ChannelID: channelID, UserID: userID}
	t.mu.Lock()
	existing := t.active[key]
	if existing == nil {
		t.mu.Unlock()
		return
	}
	existing.Stop()
	delete(t.active, key)
	t.mu.Unlock()
	t.publish(workspaceID, channelID, userID, "typing.stopped")
}

func (t *Typing) expire(key typingKey) {
	t.mu.Lock()
	if _, ok := t.active[key]; !ok {
		t.mu.Unlock()
		return
	}
	delete(t.active, key)
	t.mu.Unlock()
	t.publish(key.WorkspaceID, key.ChannelID, key.UserID, "typing.stopped")
}

func (t *Typing) publish(workspaceID, channelID, userID int64, eventType string) {
	payload, _ := marshalJSON(typingEvent{
		WorkspaceID: workspaceID,
		ChannelID:   channelID,
		UserID:      userID,
	})
	t.broker.Publish(TopicChannel(workspaceID, channelID), eventType, payload)
}
