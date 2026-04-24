//go:build integration

package server_test

// M9 integration tests: events + RSVP + RRULE + visibility.
//
// External-calendar sync (M9-P3) has its own test file once Google OAuth
// wiring lands — these cover the workspace-local calendar only.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

func newEventsHarness(t *testing.T) *testHarness {
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
	// Owner pool required for the M7 invite accept path (token → membership
	// bypasses RLS because the accepting user isn't in the workspace yet).
	ownerPool, err := db.OpenOwner(context.Background(), dsn, logger)
	require.NoError(t, err)

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")
	t.Setenv("SLIILS_SEARCH_ENABLED", "false")
	t.Setenv("SLIILS_CALLS_ENABLED", "false")

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"
	cfg.CallsEnabled = false

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		ownerPool.Close()
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}
}

type occurrenceAPI struct {
	SeriesID      int64     `json:"series_id"`
	InstanceStart time.Time `json:"instance_start"`
	Title         string    `json:"title"`
	RRule         string    `json:"rrule"`
	MyRSVP        string    `json:"my_rsvp"`
	Attendees     []struct {
		UserID *int64 `json:"user_id"`
		RSVP   string `json:"rsvp"`
	} `json:"attendees"`
}

func TestCreateSingleEvent(t *testing.T) {
	h := newEventsHarness(t)
	resp, _ := signup(t, h, "owner@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "CalCo", "cal-co")

	start := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(30 * time.Minute)
	body := fmt.Sprintf(
		`{"title":"Kickoff","start_at":%q,"end_at":%q,"time_zone":"UTC"}`,
		start.Format(time.RFC3339), end.Format(time.RFC3339),
	)
	rec := h.postAuth("/api/v1/workspaces/cal-co/events", body, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create event: %s", rec.Body.String())
	var ev map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ev))
	assert.Equal(t, "Kickoff", ev["title"])
	assert.Equal(t, "yes", ev["my_rsvp"])

	// List covers next 28 days by default — should include this event.
	rec = h.get("/api/v1/workspaces/cal-co/events", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var occs []occurrenceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	require.Len(t, occs, 1)
	assert.Equal(t, "Kickoff", occs[0].Title)
	assert.WithinDuration(t, start, occs[0].InstanceStart, time.Second)
	assert.Equal(t, "yes", occs[0].MyRSVP)
}

func TestRecurringEventExpansion(t *testing.T) {
	h := newEventsHarness(t)
	resp, _ := signup(t, h, "owner-rrule@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "RRuleCo", "rrule-co")

	// Start next Monday at 10:00 UTC. Find the nearest coming Monday.
	now := time.Now().UTC()
	daysUntilMonday := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	start := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)

	body := fmt.Sprintf(
		`{"title":"Standup","start_at":%q,"end_at":%q,"time_zone":"UTC","rrule":"FREQ=WEEKLY;BYDAY=MO;COUNT=4"}`,
		start.Format(time.RFC3339), end.Format(time.RFC3339),
	)
	rec := h.postAuth("/api/v1/workspaces/rrule-co/events", body, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create rrule event: %s", rec.Body.String())

	// List for the next 6 weeks — expects 4 occurrences (COUNT=4).
	from := now.Add(-24 * time.Hour).Format(time.RFC3339)
	to := start.Add(6 * 7 * 24 * time.Hour).Format(time.RFC3339)
	rec = h.get(fmt.Sprintf("/api/v1/workspaces/rrule-co/events?from=%s&to=%s", from, to), resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "list: %s", rec.Body.String())
	var occs []occurrenceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	assert.Len(t, occs, 4, "expected 4 Monday occurrences from COUNT=4")
	for i, o := range occs {
		assert.Equal(t, "Standup", o.Title, "occurrence %d title", i)
	}
}

func TestRSVPAndCancel(t *testing.T) {
	h := newEventsHarness(t)
	respA, _ := signup(t, h, "alice-rsvp@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob-rsvp@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "RSVPCo", "rsvp-co")

	aliceID := int64(respA.User["id"].(float64))
	bobID := int64(respB.User["id"].(float64))

	// Invite Bob via the M7 invite flow.
	invRec := h.postAuth("/api/v1/workspaces/rsvp-co/invites",
		`{"email":"bob-rsvp@cal.test","role":"member"}`, respA.AccessToken)
	require.Equal(t, http.StatusCreated, invRec.Code)
	var inv struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(invRec.Body.Bytes(), &inv))
	accRec := h.postAuth("/api/v1/invites/"+inv.Token+"/accept", "", respB.AccessToken)
	require.Equal(t, http.StatusOK, accRec.Code)

	// Alice schedules event with Bob.
	start := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(30 * time.Minute)
	body := fmt.Sprintf(
		`{"title":"Pairing","start_at":%q,"end_at":%q,"attendee_user_ids":[%d]}`,
		start.Format(time.RFC3339), end.Format(time.RFC3339), bobID,
	)
	rec := h.postAuth("/api/v1/workspaces/rsvp-co/events", body, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	var ev struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ev))

	// Bob sees it, with RSVP=pending.
	rec = h.get("/api/v1/workspaces/rsvp-co/events", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var occs []occurrenceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	require.Len(t, occs, 1)
	assert.Equal(t, "pending", occs[0].MyRSVP)

	// Bob accepts.
	rec = h.postAuth(fmt.Sprintf("/api/v1/events/%d/rsvp", ev.ID), `{"rsvp":"yes"}`, respB.AccessToken)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Bob's list now reflects yes.
	rec = h.get("/api/v1/workspaces/rsvp-co/events", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	require.Len(t, occs, 1)
	assert.Equal(t, "yes", occs[0].MyRSVP)

	// Bob tries to cancel — not the creator, 403.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/events/%d", ev.ID), "", respB.AccessToken)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Alice cancels.
	rec = h.deleteAuth(fmt.Sprintf("/api/v1/events/%d", ev.ID), "", respA.AccessToken)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Now the list returns zero events.
	rec = h.get("/api/v1/workspaces/rsvp-co/events", respA.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	assert.Empty(t, occs)

	_ = aliceID
}

func TestCrossWorkspaceEventInvisibility(t *testing.T) {
	h := newEventsHarness(t)
	respA, _ := signup(t, h, "alice-xw@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob-xw@cal.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, respA.AccessToken, "AlphaCo", "alpha-co")
	createWorkspace(t, h, respB.AccessToken, "BetaCo", "beta-co")

	start := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	end := start.Add(30 * time.Minute)
	body := fmt.Sprintf(`{"title":"Alice private","start_at":%q,"end_at":%q}`, start.Format(time.RFC3339), end.Format(time.RFC3339))
	rec := h.postAuth("/api/v1/workspaces/alpha-co/events", body, respA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Bob's own workspace list is empty.
	rec = h.get("/api/v1/workspaces/beta-co/events", respB.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var occs []occurrenceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &occs))
	assert.Empty(t, occs)

	// Bob listing Alice's workspace — 404 because RLS hides workspace
	// existence (same as M7 cross-workspace probe).
	rec = h.get("/api/v1/workspaces/alpha-co/events", respB.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
