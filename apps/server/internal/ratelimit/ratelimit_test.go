package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sliils/sliils/apps/server/internal/ratelimit"
)

func TestAllowUnderCapacity(t *testing.T) {
	l := ratelimit.New()
	rule := ratelimit.Rule{Capacity: 3, RefillEvery: 1 * time.Minute}
	for i := 0; i < 3; i++ {
		assert.True(t, l.Allow("k", rule), "call %d should pass", i)
	}
	assert.False(t, l.Allow("k", rule), "4th call should be blocked")
}

func TestAllowIsolatesKeys(t *testing.T) {
	l := ratelimit.New()
	rule := ratelimit.Rule{Capacity: 1, RefillEvery: 1 * time.Minute}
	assert.True(t, l.Allow("a", rule))
	assert.True(t, l.Allow("b", rule))
	assert.False(t, l.Allow("a", rule))
	assert.False(t, l.Allow("b", rule))
}

func TestReset(t *testing.T) {
	l := ratelimit.New()
	rule := ratelimit.Rule{Capacity: 1, RefillEvery: 1 * time.Hour}
	assert.True(t, l.Allow("k", rule))
	assert.False(t, l.Allow("k", rule))
	l.Reset("k")
	assert.True(t, l.Allow("k", rule))
}

func TestZeroCapacityAllowsAll(t *testing.T) {
	l := ratelimit.New()
	for i := 0; i < 100; i++ {
		assert.True(t, l.Allow("k", ratelimit.Rule{}))
	}
}
