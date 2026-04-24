package realtime

import (
	"sync"
	"time"
)

// Presence tracks which users are currently online per workspace. v1 single-
// node; migrating to a Redis/NATS-backed map at v1.1+ is a straight swap of
// this type behind a small interface.
//
// A user is "online" while they have at least one live WebSocket connection
// joined to their workspace. When their last connection drops we broadcast
// presence.changed with status="offline" after a short grace period so a
// refresh doesn't flicker.
type Presence struct {
	mu            sync.Mutex
	connsByUser   map[presenceKey]int // how many live WS conns a (ws, user) pair holds
	lastChangedAt map[presenceKey]time.Time
	broker        *Broker
	now           func() time.Time
}

type presenceKey struct {
	WorkspaceID int64
	UserID      int64
}

type presenceEvent struct {
	WorkspaceID int64     `json:"workspace_id"`
	UserID      int64     `json:"user_id"`
	Status      string    `json:"status"` // "online" | "offline"
	ChangedAt   time.Time `json:"changed_at"`
}

func NewPresence(broker *Broker) *Presence {
	return &Presence{
		connsByUser:   make(map[presenceKey]int),
		lastChangedAt: make(map[presenceKey]time.Time),
		broker:        broker,
		now:           time.Now,
	}
}

// Enter marks a user as online in a workspace. Idempotent — multiple tabs
// from the same user increment a refcount; only the first tab triggers the
// online broadcast.
func (p *Presence) Enter(workspaceID, userID int64) {
	p.mu.Lock()
	key := presenceKey{WorkspaceID: workspaceID, UserID: userID}
	prev := p.connsByUser[key]
	p.connsByUser[key] = prev + 1
	p.lastChangedAt[key] = p.now()
	p.mu.Unlock()

	if prev == 0 {
		p.publish(workspaceID, userID, "online")
	}
}

// Leave decrements the conn refcount. When it hits zero we broadcast
// offline. Grace periods to handle reconnect blips belong in a follow-up;
// for M4 we broadcast immediately to keep the demo crisp.
func (p *Presence) Leave(workspaceID, userID int64) {
	p.mu.Lock()
	key := presenceKey{WorkspaceID: workspaceID, UserID: userID}
	prev := p.connsByUser[key]
	if prev <= 1 {
		delete(p.connsByUser, key)
		p.lastChangedAt[key] = p.now()
		p.mu.Unlock()
		if prev == 1 {
			p.publish(workspaceID, userID, "offline")
		}
		return
	}
	p.connsByUser[key] = prev - 1
	p.mu.Unlock()
}

// Snapshot returns every user currently online in the workspace. Used by
// newly-connected clients to populate their presence UI without waiting
// for change events.
func (p *Presence) Snapshot(workspaceID int64) []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int64, 0)
	for key := range p.connsByUser {
		if key.WorkspaceID == workspaceID {
			out = append(out, key.UserID)
		}
	}
	return out
}

func (p *Presence) publish(workspaceID, userID int64, status string) {
	ev := presenceEvent{
		WorkspaceID: workspaceID,
		UserID:      userID,
		Status:      status,
		ChangedAt:   p.now().UTC(),
	}
	payload, _ := marshalJSON(ev)
	p.broker.Publish(TopicWorkspace(workspaceID), "presence.changed", payload)
}
