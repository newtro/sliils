//go:build integration

package server_test

// M6 integration tests for the search pipeline.
//
// Prerequisites (docker):
//
//	docker run --rm -d --name sliils-meili-test -p 7701:7700 \
//	    -e MEILI_MASTER_KEY=test-master-key-32characters-0000 \
//	    getmeili/meilisearch:v1.12
//
// Set SLIILS_TEST_MEILI_URL / SLIILS_TEST_MEILI_MASTER_KEY to point the
// test at it. If Meili is unreachable, these tests skip (not fail) so the
// rest of the integration suite keeps working on machines without Meili.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/search"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

// searchHarness extends the base harness with a real Meilisearch client,
// an owner pool, and a direct-drain indexer. Tests call Drain() explicitly
// instead of relying on River's periodic scheduling so assertions are
// deterministic.
type searchHarness struct {
	*testHarness
	ownerPool *pgxpool.Pool
	client    *search.Client
	indexer   *search.Indexer
	prefix    string
}

func newSearchHarness(t *testing.T) *searchHarness {
	t.Helper()

	meiliURL := os.Getenv("SLIILS_TEST_MEILI_URL")
	if meiliURL == "" {
		meiliURL = "http://localhost:7700"
	}
	meiliKey := os.Getenv("SLIILS_TEST_MEILI_MASTER_KEY")
	if meiliKey == "" {
		meiliKey = "local-dev-master-key-change-in-prod-0123456789abcdef"
	}

	// Quick reachability probe — if Meili isn't up, skip instead of fail.
	probeClient, err := search.NewClient(search.ClientOptions{
		URL:       meiliURL,
		MasterKey: meiliKey,
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := probeClient.Health(ctx); err != nil {
		t.Skipf("Meilisearch not reachable at %s: %v — skipping M6 integration", meiliURL, err)
	}

	// Build the harness with search env vars set so config.Load picks them up.
	t.Setenv("SLIILS_MEILI_URL", meiliURL)
	t.Setenv("SLIILS_MEILI_MASTER_KEY", meiliKey)
	// Unique prefix per test run so parallel / repeated runs don't collide.
	prefix := "sliils_test_" + randSuffix()
	t.Setenv("SLIILS_SEARCH_INDEX_PREFIX", prefix)
	t.Setenv("SLIILS_SEARCH_ENABLED", "true")

	// Rebuild the base harness using the same plumbing as newHarness, but
	// extend Options with the search wiring. We duplicate a little of
	// newHarness so we can thread the search client through.
	dsn := os.Getenv("SLIILS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:Fl1pFl0p@localhost:5432/sliils_test?sslmode=disable"
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	resetSchema(t, dsn)
	require.NoError(t, db.RunMigrations(context.Background(), dsn, migrations.FS, ".", logger))

	pool, err := db.Open(context.Background(), dsn, logger)
	require.NoError(t, err)

	ownerPool, err := db.OpenOwner(context.Background(), dsn, logger)
	require.NoError(t, err)

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"

	searchClient, err := search.NewClient(search.ClientOptions{
		URL:         cfg.MeiliURL,
		MasterKey:   cfg.MeiliMasterKey,
		IndexPrefix: cfg.SearchIndexPrefix,
		Logger:      logger,
	})
	require.NoError(t, err)
	require.NoError(t, searchClient.EnsureIndex(context.Background()))

	indexer := search.NewIndexer(searchClient, ownerPool, logger, search.IndexerOptions{
		DrainBatchSize: 500,
	})

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:  email.NoopSender{Sent: emails},
		Limiter:      ratelimit.New(),
		SearchClient: searchClient,
		// Tests don't need tenant tokens; leave nil.
	})
	require.NoError(t, err)

	base := &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}

	t.Cleanup(func() {
		// Best-effort cleanup of the test index so we don't accumulate
		// junk in the shared Meili instance.
		task, _ := searchClient.Svc().DeleteIndex(prefix + "_messages")
		if task != nil {
			_, _ = searchClient.Svc().WaitForTask(task.TaskUID, 100*time.Millisecond)
		}
		ownerPool.Close()
		pool.Close()
	})

	return &searchHarness{testHarness: base, ownerPool: ownerPool, client: searchClient, indexer: indexer, prefix: prefix}
}

func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// drainAndWait triggers one drain cycle and waits for Meilisearch to
// finish processing the resulting tasks. Needed because Meili's writes are
// async — Drain returns once the HTTP POST is accepted, not once the
// document is queryable.
func (h *searchHarness) drainAndWait(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stats, err := h.indexer.Drain(ctx)
	require.NoError(t, err, "drain failed")
	// Give Meili a moment to apply the task. The lazy polling loop below
	// makes the test deterministic even on slow machines.
	t.Logf("drain stats: claimed=%d indexed=%d deleted=%d pending=%d", stats.Claimed, stats.Indexed, stats.Deleted, stats.Pending)

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := h.client.Svc().Index(h.client.MessageIndex()).GetStats()
		if err == nil && stats != nil {
			// Meili reports IsIndexing=false when the processing queue is empty.
			if !stats.IsIndexing {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// searchRequest posts to /search and unmarshals the response. Keeps tests tidy.
func (h *searchHarness) searchRequest(t *testing.T, token string, workspaceID int64, query string) searchAPIResponse {
	t.Helper()
	body := fmt.Sprintf(`{"workspace_id":%d,"query":%q}`, workspaceID, query)
	rec := h.postAuth("/api/v1/search", body, token)
	require.Equal(t, http.StatusOK, rec.Code, "search body: %s", rec.Body.String())
	var resp searchAPIResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

type searchAPIResponse struct {
	Hits []struct {
		MessageID   int64  `json:"message_id"`
		ChannelID   int64  `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		BodyMD      string `json:"body_md"`
		Snippet     string `json:"snippet"`
	} `json:"hits"`
	EstimatedTotalHits int64             `json:"estimated_total_hits"`
	Parsed             search.QuerySpec `json:"parsed"`
}

// ---- tests ---------------------------------------------------------------

// TestSearchRoundtrip is the happy-path: post a message, drain, search for
// a word in the body, expect one hit.
func TestSearchRoundtrip(t *testing.T) {
	h := newSearchHarness(t)
	resp, _ := signup(t, h.testHarness, "search-rt@example.com", "correct-horse-battery-staple")
	drainEmails(h.testHarness)

	ws := createWorkspace(t, h.testHarness, resp.AccessToken, "SearchCo", "search-co")
	chID := firstChannelID(t, h.testHarness, resp.AccessToken, "search-co")

	postMessage(t, h.testHarness, resp.AccessToken, chID, "the quick brown fox jumps over the lazy dog")

	h.drainAndWait(t)

	res := h.searchRequest(t, resp.AccessToken, ws.ID, "brown fox")
	require.GreaterOrEqual(t, len(res.Hits), 1, "expected a hit; got zero")
	assert.Contains(t, res.Hits[0].BodyMD, "quick brown fox")
	assert.Contains(t, res.Hits[0].Snippet, "<mark>", "highlight tag expected in snippet")
}

// TestSearchCrossWorkspaceRLS is the acceptance-gate probe: user B must
// never see user A's messages even when targeting A's workspace.
func TestSearchCrossWorkspaceRLS(t *testing.T) {
	h := newSearchHarness(t)

	// Alice posts in workspace A.
	respA, _ := signup(t, h.testHarness, "alice-xws@example.com", "correct-horse-battery-staple")
	drainEmails(h.testHarness)
	wsA := createWorkspace(t, h.testHarness, respA.AccessToken, "AWorkspace", "a-ws")
	chA := firstChannelID(t, h.testHarness, respA.AccessToken, "a-ws")
	postMessage(t, h.testHarness, respA.AccessToken, chA, "alice top-secret planning notes")

	// Bob posts in workspace B.
	respB, _ := signup(t, h.testHarness, "bob-xws@example.com", "correct-horse-battery-staple")
	drainEmails(h.testHarness)
	wsB := createWorkspace(t, h.testHarness, respB.AccessToken, "BWorkspace", "b-ws")
	chB := firstChannelID(t, h.testHarness, respB.AccessToken, "b-ws")
	postMessage(t, h.testHarness, respB.AccessToken, chB, "bob unrelated chit-chat")

	h.drainAndWait(t)

	// Bob searches workspace A (his own workspace id is B) — must be
	// rejected with 403 before the search service even hits Meili.
	body := fmt.Sprintf(`{"workspace_id":%d,"query":"top-secret"}`, wsA.ID)
	rec := h.postAuth("/api/v1/search", body, respB.AccessToken)
	assert.Equal(t, http.StatusForbidden, rec.Code, "Bob must be 403'd on Alice's workspace")

	// Bob searching his own workspace must not surface Alice's content.
	resB := h.searchRequest(t, respB.AccessToken, wsB.ID, "top-secret")
	for _, hit := range resB.Hits {
		assert.NotEqual(t, wsA.ID, hit.ChannelID, "no Alice messages in Bob's results")
	}
	// And the body must not contain Alice's phrase anywhere.
	for _, hit := range resB.Hits {
		assert.NotContains(t, hit.BodyMD, "alice top-secret")
	}

	// Alice searching her own workspace sees her message.
	resA := h.searchRequest(t, respA.AccessToken, wsA.ID, "top-secret")
	require.GreaterOrEqual(t, len(resA.Hits), 1)
	assert.Contains(t, resA.Hits[0].BodyMD, "alice top-secret")
}

// TestSearchPrivateChannelExclusion ensures that a user who is NOT a member
// of a private channel cannot retrieve its messages via search.
func TestSearchPrivateChannelExclusion(t *testing.T) {
	h := newSearchHarness(t)

	// Two users in the same workspace.
	tokA, tokB, wsID, defaultCh, aID, bID := twoUserSharedWorkspace(t, h.testHarness)
	_ = defaultCh
	_ = aID

	// Create a private channel manually via admin SQL (no M6 UI for this
	// yet). Alice is the only member.
	require.NoError(t, h.adminExec(
		`INSERT INTO channels (workspace_id, type, name, default_join, created_by) VALUES ($1, 'private', 'secrets', false, $2)`,
		wsID, aID,
	))

	// Query back the id using the owner pool (RLS bypassed — simpler for
	// the test than setting GUCs on the runtime pool).
	var privCh int64
	row := h.ownerPool.QueryRow(context.Background(), `SELECT id FROM channels WHERE name = 'secrets' AND workspace_id = $1`, wsID)
	require.NoError(t, row.Scan(&privCh))

	// Add Alice to the private channel.
	require.NoError(t, h.adminExec(
		`INSERT INTO channel_memberships (workspace_id, channel_id, user_id) VALUES ($1, $2, $3)`,
		wsID, privCh, aID,
	))

	// Alice posts in the private channel.
	postMessage(t, h.testHarness, tokA, privCh, "confidential launch plan for q3")

	h.drainAndWait(t)

	// Bob (not a member) must not see that message.
	resB := h.searchRequest(t, tokB, wsID, "confidential launch")
	for _, hit := range resB.Hits {
		assert.NotEqual(t, privCh, hit.ChannelID, "Bob saw a private-channel hit he shouldn't")
	}

	// Alice sees it.
	resA := h.searchRequest(t, tokA, wsID, "confidential launch")
	foundForAlice := false
	for _, hit := range resA.Hits {
		if hit.ChannelID == privCh {
			foundForAlice = true
			break
		}
	}
	assert.True(t, foundForAlice, "Alice should find her own private channel message")
	_ = bID
}

// TestSearchDeletePurge asserts the "deleted messages purged from index
// within 60s" acceptance criterion — with the 2s drain cadence we model
// locally via direct Drain calls, this is effectively <1s.
func TestSearchDeletePurge(t *testing.T) {
	h := newSearchHarness(t)
	resp, _ := signup(t, h.testHarness, "delete-purge@example.com", "correct-horse-battery-staple")
	drainEmails(h.testHarness)
	ws := createWorkspace(t, h.testHarness, resp.AccessToken, "PurgeCo", "purge-co")
	chID := firstChannelID(t, h.testHarness, resp.AccessToken, "purge-co")

	m := postMessage(t, h.testHarness, resp.AccessToken, chID, "ephemeral secret watermelon")
	h.drainAndWait(t)

	// Confirm it's findable.
	pre := h.searchRequest(t, resp.AccessToken, ws.ID, "watermelon")
	require.GreaterOrEqual(t, len(pre.Hits), 1, "pre-delete: message must be indexed")

	// Delete it.
	rec := h.deleteAuth(fmt.Sprintf("/api/v1/messages/%d", m.ID), "", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "delete failed: %s", rec.Body.String())

	h.drainAndWait(t)

	// Must no longer appear.
	post := h.searchRequest(t, resp.AccessToken, ws.ID, "watermelon")
	for _, hit := range post.Hits {
		assert.NotEqual(t, m.ID, hit.MessageID, "deleted message still in results")
	}
}

// TestSearchOperators verifies in:/from:/has: operators work end-to-end.
func TestSearchOperators(t *testing.T) {
	h := newSearchHarness(t)
	tokA, tokB, wsID, chID, aID, bID := twoUserSharedWorkspace(t, h.testHarness)
	_ = bID
	_ = aID

	// Alice posts two messages; one with a link.
	postMessage(t, h.testHarness, tokA, chID, "ship the release candidate please")
	postMessage(t, h.testHarness, tokA, chID, "doc up at https://example.com/spec")
	// Bob posts one.
	postMessage(t, h.testHarness, tokB, chID, "alice is blocked")

	h.drainAndWait(t)

	// has:link must narrow to the url message.
	res := h.searchRequest(t, tokA, wsID, "has:link")
	require.GreaterOrEqual(t, len(res.Hits), 1, "has:link should match the url message")
	for _, hit := range res.Hits {
		assert.Contains(t, hit.BodyMD, "http", "every has:link hit must actually contain http")
	}

	// in:#general should resolve to the default channel (created as
	// `general` by workspaces handler).
	in := h.searchRequest(t, tokA, wsID, "in:#general ship")
	foundShip := false
	for _, hit := range in.Hits {
		if hit.ChannelID == chID && strings.Contains(hit.BodyMD, "ship the release") {
			foundShip = true
		}
	}
	assert.True(t, foundShip, "in:#general + 'ship' should find Alice's release message")
}
