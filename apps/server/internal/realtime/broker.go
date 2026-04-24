// Package realtime owns the in-process pub/sub bus that fans WebSocket
// events from HTTP handlers to connected clients.
//
// At v1 single-node scale the broker lives in memory. A Broker interface
// will let us swap in Redis Streams / NATS JetStream at v1.1 without
// touching handlers or the WebSocket gateway.
package realtime

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event is what flows over the bus. Type / Data are application-specific
// (e.g. "message.created" + marshalled JSON). ID is a monotonically
// increasing server-assigned identifier used for reconnect-replay.
type Event struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Topic     string    `json:"-"`
	Data      []byte    `json:"data"` // pre-marshalled JSON payload
	Timestamp time.Time `json:"ts"`
}

// Subscriber receives events on a buffered channel. SlowConsumerDropped
// reports how many events were dropped because the consumer fell behind.
type Subscriber struct {
	ch                  chan Event
	slowConsumerDropped atomic.Int64
	closeOnce           sync.Once
}

// C exposes the receive channel. Close detection via `_, ok := <-sub.C()`.
func (s *Subscriber) C() <-chan Event { return s.ch }

// Dropped returns the number of events skipped due to a full channel.
func (s *Subscriber) Dropped() int64 { return s.slowConsumerDropped.Load() }

// Broker is the topic-addressable bus.
type Broker struct {
	mu          sync.RWMutex
	topics      map[string]map[*Subscriber]struct{}
	buffer      *ringBuffer
	nextEventID atomic.Int64

	// subBuffer is the per-subscriber channel depth. 256 is large enough to
	// absorb short bursts (client GC pause, network hiccup) without blocking
	// publishers; beyond that the subscriber gets "dropped" on the oldest.
	subBuffer int
}

func NewBroker() *Broker {
	return &Broker{
		topics:    make(map[string]map[*Subscriber]struct{}),
		buffer:    newRingBuffer(10_000), // ~ 5 min at low-hundreds events/sec
		subBuffer: 256,
	}
}

// Publish fans an event to every subscriber currently on the topic. Returns
// the assigned event id so callers can log/ack.
func (b *Broker) Publish(topic, typ string, data []byte) int64 {
	ev := Event{
		ID:        b.nextEventID.Add(1),
		Type:      typ,
		Topic:     topic,
		Data:      data,
		Timestamp: time.Now().UTC(),
	}
	b.buffer.Append(ev)

	b.mu.RLock()
	subs := b.topics[topic]
	// Copy pointer slice under the lock so we don't block publishers while
	// we deliver. The slice header is tiny; the subscribers themselves are
	// addressed by pointer.
	snapshot := make([]*Subscriber, 0, len(subs))
	for s := range subs {
		snapshot = append(snapshot, s)
	}
	b.mu.RUnlock()

	for _, s := range snapshot {
		select {
		case s.ch <- ev:
		default:
			// Slow consumer: drop and move on. The client can detect this
			// by the gap in event ids and request a full resync.
			s.slowConsumerDropped.Add(1)
		}
	}
	return ev.ID
}

// Subscribe registers a new subscriber against the given topic set.
// Returns the subscriber plus an unsubscribe func that detaches from all
// topics and drains the channel.
func (b *Broker) Subscribe(topics []string) (*Subscriber, func()) {
	sub := &Subscriber{ch: make(chan Event, b.subBuffer)}
	b.AddTopics(sub, topics)
	return sub, func() { b.remove(sub) }
}

// AddTopics grows a subscriber's topic set without creating a new channel.
// Safe to call concurrently with publishers.
func (b *Broker) AddTopics(sub *Subscriber, topics []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range topics {
		set := b.topics[t]
		if set == nil {
			set = make(map[*Subscriber]struct{})
			b.topics[t] = set
		}
		set[sub] = struct{}{}
	}
}

// RemoveTopics shrinks a subscriber's topic set.
func (b *Broker) RemoveTopics(sub *Subscriber, topics []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range topics {
		if set, ok := b.topics[t]; ok {
			delete(set, sub)
			if len(set) == 0 {
				delete(b.topics, t)
			}
		}
	}
}

// remove detaches sub from every topic and closes its channel. Idempotent.
func (b *Broker) remove(sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for t, set := range b.topics {
		if _, ok := set[sub]; ok {
			delete(set, sub)
			if len(set) == 0 {
				delete(b.topics, t)
			}
		}
	}
	// Safe channel close: we hold the write lock so no publisher can be
	// mid-send into sub.ch. sync.Once makes double-unsubscribe a no-op.
	sub.closeOnce.Do(func() { close(sub.ch) })
}

// Replay returns every event on the given topics with ID > sinceID.
// Returns !complete if the requested sinceID has already been evicted from
// the ring buffer — caller should trigger a full resync.
func (b *Broker) Replay(topics []string, sinceID int64) (events []Event, complete bool) {
	return b.buffer.Replay(topics, sinceID)
}

// LastEventID returns the id of the most recently published event (0 if
// nothing has been published yet). Used by the WebSocket hello handshake
// so reconnecting clients know whether to request a replay.
func (b *Broker) LastEventID() int64 {
	return b.nextEventID.Load()
}

// ---- ringBuffer ----------------------------------------------------------

// ringBuffer keeps the last N published events so reconnecting clients can
// replay what they missed. Small and lock-free-ish: a single Mutex guards
// the head pointer and the underlying slice.
type ringBuffer struct {
	mu   sync.Mutex
	data []Event
	cap  int
	head int
	size int
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{data: make([]Event, cap), cap: cap}
}

func (r *ringBuffer) Append(ev Event) {
	r.mu.Lock()
	r.data[r.head] = ev
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
	r.mu.Unlock()
}

// Replay returns every buffered event whose topic is in topicSet and
// whose ID is strictly greater than sinceID, in ascending ID order.
// complete=false means sinceID is older than anything still in the buffer
// and the caller needs a full resync to avoid gaps.
func (r *ringBuffer) Replay(topics []string, sinceID int64) ([]Event, bool) {
	topicSet := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		topicSet[t] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// The oldest retained event lives at head-size (mod cap). If sinceID is
	// older than the oldest event still in the buffer, we cannot guarantee
	// a gap-free replay.
	start := (r.head - r.size + r.cap) % r.cap
	oldestID := int64(0)
	if r.size > 0 {
		oldestID = r.data[start].ID
	}
	complete := sinceID+1 >= oldestID

	out := make([]Event, 0, r.size)
	for i := 0; i < r.size; i++ {
		ev := r.data[(start+i)%r.cap]
		if ev.ID <= sinceID {
			continue
		}
		if _, ok := topicSet[ev.Topic]; ok {
			out = append(out, ev)
		}
	}
	return out, complete
}

// TopicChannel is the v1 channel-level topic. Format: ws:{workspace}:ch:{id}.
func TopicChannel(workspaceID, channelID int64) string {
	return topicChannel(workspaceID, channelID)
}
