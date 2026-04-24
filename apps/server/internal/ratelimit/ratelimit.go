// Package ratelimit implements an in-memory per-key token bucket.
//
// v1 single-node scope only. When multi-node lands (v1.1+), swap the backing
// store for Postgres or Redis behind the same Limiter surface without
// touching handlers.
package ratelimit

import (
	"sync"
	"time"
)

// Rule captures the bucket geometry for one named category.
type Rule struct {
	// Capacity is the burst size; RefillEvery describes the steady-state rate.
	// Rate = Capacity / RefillEvery tokens per duration.
	Capacity     int
	RefillEvery  time.Duration
}

// Rules mirror tech-spec §3.5. Keys are "<category>:<identifier>" where
// identifier is typically an IP, a user id, or an email.
var (
	RuleLogin           = Rule{Capacity: 5, RefillEvery: 1 * time.Minute}
	RuleLoginPerUser    = Rule{Capacity: 10, RefillEvery: 1 * time.Hour}
	RuleSignup          = Rule{Capacity: 3, RefillEvery: 1 * time.Hour}
	RuleMagicLinkIssue  = Rule{Capacity: 5, RefillEvery: 15 * time.Minute}
	RulePasswordReset   = Rule{Capacity: 5, RefillEvery: 1 * time.Hour}
	RuleGeneral         = Rule{Capacity: 1000, RefillEvery: 1 * time.Hour}
	// RuleFirstRun gates /first-run/state + /first-run/bootstrap so a
	// drive-by scanner can't hammer an install that's still coming up.
	RuleFirstRun = Rule{Capacity: 30, RefillEvery: 1 * time.Minute}
	// RuleTenantWrite applies to workspace-scoped create/upload paths
	// (new channel, message, DM, invite, file upload). Keyed per
	// workspace+user so one noisy member can't block other tenants.
	// Capacity 60 in 10s means a burst of 60 then ~6/s sustained —
	// enough for normal chat, stops a runaway script.
	RuleTenantWrite = Rule{Capacity: 60, RefillEvery: 10 * time.Second}
)

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

func New() *Limiter {
	return &Limiter{
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow returns true if the caller may proceed. It decrements the bucket on
// success. The first call for a key starts full.
func (l *Limiter) Allow(key string, rule Rule) bool {
	if rule.Capacity <= 0 || rule.RefillEvery <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(rule.Capacity) - 1, lastRefill: now}
		l.buckets[key] = b
		return true
	}

	// Refill based on elapsed time.
	ratePerSec := float64(rule.Capacity) / rule.RefillEvery.Seconds()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * ratePerSec
	if b.tokens > float64(rule.Capacity) {
		b.tokens = float64(rule.Capacity)
	}
	b.lastRefill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Reset clears the bucket for a key. Handlers call this after a successful
// login so the attacker's failures don't poison the legitimate user.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Prune removes buckets that haven't been touched in the given window.
// Callers can run this periodically to bound memory.
func (l *Limiter) Prune(olderThan time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-olderThan)
	for k, b := range l.buckets {
		if b.lastRefill.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
