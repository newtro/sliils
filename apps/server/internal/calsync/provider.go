// Package calsync owns the external-calendar sync plumbing: OAuth
// negotiation, refresh-token storage, push (SliilS → provider), and pull
// (provider → SliilS). Structured so Google and Microsoft are
// interchangeable behind a Provider interface — the worker code is
// provider-agnostic.
package calsync

import (
	"context"
	"errors"
	"time"

	"golang.org/x/oauth2"
)

// Provider is the contract every calendar backend implements.
type Provider interface {
	// Name is the DB-stored provider key: "google" | "microsoft" | ...
	Name() string

	// AuthCodeURL builds the start-OAuth URL. state is a CSRF token the
	// caller generates and checks again on the callback.
	AuthCodeURL(state string) string

	// Exchange swaps an auth code (from the callback) for a token set.
	// The returned token MUST include a refresh token — the callback
	// handler rejects flows that don't grant offline access.
	Exchange(ctx context.Context, code string) (*oauth2.Token, error)

	// AccountEmail fetches the connected account's email, used as the
	// display identifier ("which Gmail?").
	AccountEmail(ctx context.Context, refreshToken string) (string, error)

	// Pull runs one incremental sync cycle. `syncToken` is the
	// provider-issued cursor from the previous cycle (empty on first
	// run). Returns the changed events + a new cursor.
	Pull(ctx context.Context, refreshToken, syncToken string) (*PullResult, error)

	// Push creates or updates an event on the provider side. If
	// existingID is non-empty, we update; otherwise create.
	Push(ctx context.Context, refreshToken string, evt *Event, existingID string) (newID, etag string, err error)

	// Delete removes the event from the provider side.
	Delete(ctx context.Context, refreshToken, externalID string) error
}

// Event is the provider-neutral shape passed between SliilS and the
// provider-specific code. Times are always stored as UTC; the provider
// packs back into its own tz format (Google wants RFC3339 + tz id).
type Event struct {
	Title       string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	TimeZone    string
	RRule       string
	Attendees   []Attendee
}

type Attendee struct {
	Email       string
	DisplayName string
	RSVP        string // "yes" | "no" | "maybe" | "pending"
}

// PullResult is the provider's delta response. IncrementalCursor is what
// the worker stores back into external_calendars.sync_token.
type PullResult struct {
	Changed           []ChangedEvent
	IncrementalCursor string
}

type ChangedEvent struct {
	ExternalID  string
	ETag        string
	Event       *Event // nil if Deleted
	Deleted     bool
}

// ErrNeedsReauth is returned when the refresh token is rejected by the
// provider (user revoked access, changed password, etc.). The worker
// bubbles this up so the connection can be marked disconnected rather
// than spinning forever.
var ErrNeedsReauth = errors.New("external calendar needs reauth")
