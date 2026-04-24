package realtime_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/realtime"
)

func TestPublishDeliversToAllSubscribers(t *testing.T) {
	b := realtime.NewBroker()
	topic := realtime.TopicChannel(1, 1)

	subA, doneA := b.Subscribe([]string{topic})
	defer doneA()
	subB, doneB := b.Subscribe([]string{topic})
	defer doneB()

	b.Publish(topic, "message.created", []byte(`{"id":1}`))

	assertReceives(t, subA.C(), "message.created")
	assertReceives(t, subB.C(), "message.created")
}

func TestPublishIsolatesTopics(t *testing.T) {
	b := realtime.NewBroker()
	topicA := realtime.TopicChannel(1, 1)
	topicB := realtime.TopicChannel(1, 2)

	subA, doneA := b.Subscribe([]string{topicA})
	defer doneA()

	b.Publish(topicB, "message.created", nil)

	select {
	case ev := <-subA.C():
		t.Fatalf("subA should not have received event for topic %s, got %v", topicB, ev)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := realtime.NewBroker()
	topic := realtime.TopicChannel(1, 1)

	sub, done := b.Subscribe([]string{topic})
	done()

	_, ok := <-sub.C()
	assert.False(t, ok, "channel should be closed after unsubscribe")
}

func TestReplayFromSinceID(t *testing.T) {
	b := realtime.NewBroker()
	topic := realtime.TopicChannel(1, 1)

	id1 := b.Publish(topic, "message.created", []byte(`{"n":1}`))
	id2 := b.Publish(topic, "message.created", []byte(`{"n":2}`))
	id3 := b.Publish(topic, "message.created", []byte(`{"n":3}`))

	events, complete := b.Replay([]string{topic}, id1)
	require.True(t, complete, "buffer should still have all events")
	require.Len(t, events, 2)
	assert.Equal(t, id2, events[0].ID)
	assert.Equal(t, id3, events[1].ID)
}

func TestSlowConsumerGetsDropped(t *testing.T) {
	b := realtime.NewBroker()
	topic := realtime.TopicChannel(1, 1)

	sub, done := b.Subscribe([]string{topic})
	defer done()

	// Publish way past the subscriber's buffer depth (256) without reading.
	for i := 0; i < 1000; i++ {
		b.Publish(topic, "message.created", nil)
	}
	assert.Greater(t, sub.Dropped(), int64(0),
		"slow consumer should have dropped events")
}

func TestConcurrentPublishersAndSubscribers(t *testing.T) {
	// Invariant: every published event is either delivered to the
	// subscriber or explicitly accounted for in Dropped(). The broker must
	// never silently lose an event. The exact split between delivered and
	// dropped depends on scheduling and is not part of the contract.
	b := realtime.NewBroker()
	topic := realtime.TopicChannel(1, 1)
	const publishers = 8
	const perPublisher = 500
	total := int64(publishers * perPublisher)

	sub, done := b.Subscribe([]string{topic})
	defer done()

	received := int64(0)
	drainDone := make(chan struct{})
	go func() {
		for range sub.C() {
			received++
		}
		close(drainDone)
	}()

	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				b.Publish(topic, "message.created", []byte(fmt.Sprintf(`{"p":%d,"i":%d}`, id, i)))
			}
		}(p)
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond)
	done()
	<-drainDone

	assert.Equal(t, total, received+sub.Dropped(),
		"every event must be either delivered or explicitly dropped (got received=%d dropped=%d, want sum=%d)",
		received, sub.Dropped(), total)
}

func assertReceives(t *testing.T, ch <-chan realtime.Event, typ string) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok)
		assert.Equal(t, typ, ev.Type)
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", typ)
	}
}
