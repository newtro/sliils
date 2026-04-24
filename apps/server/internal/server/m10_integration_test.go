//go:build integration

package server_test

// M10 integration tests: pages + comments + WOPI.
//
// Y-Sweet is stubbed with a small httptest.Server so we can verify the
// server hits the right URLs without needing a live Y-Sweet. WOPI
// endpoints use an in-process token roundtrip — no external process.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/pages"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/internal/wopi"
	"github.com/sliils/sliils/apps/server/migrations"
)

// ysweetStub stands in for a real Y-Sweet server. It records every call
// the SliilS app makes and returns canned responses. Keeps the test
// self-contained while still exercising the real HTTP client paths.
type ysweetStub struct {
	srv         *httptest.Server
	createdDocs map[string]bool
	snapshots   map[string][]byte
}

func newYSweetStub() *ysweetStub {
	s := &ysweetStub{
		createdDocs: map[string]bool{},
		snapshots:   map[string][]byte{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/doc/new", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DocID string `json:"docId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.createdDocs[body.DocID] = true
		_, _ = w.Write([]byte(`{"docId":"` + body.DocID + `"}`))
	})
	mux.HandleFunc("/doc/", func(w http.ResponseWriter, r *http.Request) {
		// Covers /doc/{id}/auth, /doc/{id}/as-update, /doc/{id}/update
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/auth"):
			docID := strings.TrimPrefix(path, "/doc/")
			docID = strings.TrimSuffix(docID, "/auth")
			resp := map[string]string{
				"url":     "wss://ysweet.test/ws/" + docID,
				"baseUrl": s.srv.URL,
				"docId":   docID,
				"token":   "stub-token-" + docID,
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasSuffix(path, "/as-update"):
			docID := strings.TrimPrefix(path, "/doc/")
			docID = strings.TrimSuffix(docID, "/as-update")
			data := s.snapshots[docID]
			if data == nil {
				data = []byte{0x01, 0x02, 0x03} // pretend Yjs update
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
		case strings.HasSuffix(path, "/update"):
			docID := strings.TrimPrefix(path, "/doc/")
			docID = strings.TrimSuffix(docID, "/update")
			body, _ := io.ReadAll(r.Body)
			s.snapshots[docID] = body
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Health probe — any response is "alive" for our client.
		http.Error(w, "ok", http.StatusNotFound)
	})
	s.srv = httptest.NewServer(mux)
	return s
}

func (s *ysweetStub) Close() { s.srv.Close() }

func newPagesHarness(t *testing.T) (*testHarness, *ysweetStub) {
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
	ownerPool, err := db.OpenOwner(context.Background(), dsn, logger)
	require.NoError(t, err)

	stub := newYSweetStub()

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")
	t.Setenv("SLIILS_SEARCH_ENABLED", "false")
	t.Setenv("SLIILS_CALLS_ENABLED", "false")
	t.Setenv("SLIILS_PAGES_ENABLED", "true")
	t.Setenv("SLIILS_YSWEET_URL", stub.srv.URL)

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"
	cfg.CallsEnabled = false

	yc, err := pages.NewClient(pages.Options{BaseURL: stub.srv.URL, Logger: logger})
	require.NoError(t, err)

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
		YSweet:        yc,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		stub.Close()
		ownerPool.Close()
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}, stub
}

// ---- tests -------------------------------------------------------------

type pageAPI struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	DocID       string `json:"doc_id"`
	WorkspaceID int64  `json:"workspace_id"`
}

func TestM10CreateAndListPage(t *testing.T) {
	h, stub := newPagesHarness(t)
	resp, _ := signup(t, h, "creator@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "M10Co", "m10-co")

	rec := h.postAuth("/api/v1/workspaces/m10-co/pages",
		`{"title":"Design notes"}`, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create page: %s", rec.Body.String())
	var p pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "Design notes", p.Title)
	assert.NotEmpty(t, p.DocID)
	assert.True(t, stub.createdDocs[p.DocID], "y-sweet CreateDoc should have been called")

	rec = h.get("/api/v1/workspaces/m10-co/pages", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.Equal(t, p.ID, list[0].ID)
}

func TestM10IssuePageAuth(t *testing.T) {
	h, _ := newPagesHarness(t)
	resp, _ := signup(t, h, "creator@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "M10Co", "m10-co")

	rec := h.postAuth("/api/v1/workspaces/m10-co/pages", `{"title":"x"}`, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var p pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))

	rec = h.postAuth(fmt.Sprintf("/api/v1/pages/%d/auth", p.ID), "{}", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "auth: %s", rec.Body.String())
	var a struct {
		URL   string `json:"url"`
		DocID string `json:"doc_id"`
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &a))
	assert.Contains(t, a.URL, "ws")
	assert.Equal(t, p.DocID, a.DocID)
	assert.NotEmpty(t, a.Token)
}

func TestM10CommentsLifecycle(t *testing.T) {
	h, _ := newPagesHarness(t)
	resp, _ := signup(t, h, "creator@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "M10Co", "m10-co")

	rec := h.postAuth("/api/v1/workspaces/m10-co/pages", `{"title":"x"}`, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var p pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))

	// Add a comment
	rec = h.postAuth(fmt.Sprintf("/api/v1/pages/%d/comments", p.ID),
		`{"body_md":"first thought"}`, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create comment: %s", rec.Body.String())
	var comment struct {
		ID     int64  `json:"id"`
		BodyMD string `json:"body_md"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &comment))
	assert.Equal(t, "first thought", comment.BodyMD)

	// Resolve
	rec = h.patchAuth(fmt.Sprintf("/api/v1/comments/%d", comment.ID),
		`{"resolved":true}`, resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var resolved struct {
		ResolvedAt *string `json:"resolved_at"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resolved))
	assert.NotNil(t, resolved.ResolvedAt)

	// List should still show it
	rec = h.get(fmt.Sprintf("/api/v1/pages/%d/comments", p.ID), resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.NotNil(t, list[0]["resolved_at"])
}

func TestM10SnapshotRoundtrip(t *testing.T) {
	h, stub := newPagesHarness(t)
	resp, _ := signup(t, h, "creator@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "M10Co", "m10-co")

	rec := h.postAuth("/api/v1/workspaces/m10-co/pages", `{"title":"x"}`, resp.AccessToken)
	var p pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	// Prime the stub with some "doc bytes"
	stub.snapshots[p.DocID] = []byte("yjs-update-bytes-v1")

	rec = h.postAuth(fmt.Sprintf("/api/v1/pages/%d/snapshots", p.ID), "{}", resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create snapshot: %s", rec.Body.String())
	var s struct {
		ID       int64  `json:"id"`
		ByteSize int32  `json:"byte_size"`
		Reason   string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.Equal(t, int32(len("yjs-update-bytes-v1")), s.ByteSize)
	assert.Equal(t, "manual", s.Reason)

	// Restore — stub will receive the apply
	rec = h.postAuth(fmt.Sprintf("/api/v1/pages/%d/snapshots/%d/restore", p.ID, s.ID), "{}", resp.AccessToken)
	require.Equal(t, http.StatusNoContent, rec.Code, "restore: %s", rec.Body.String())
	// Applied bytes should match the stored snapshot.
	assert.Equal(t, []byte("yjs-update-bytes-v1"), stub.snapshots[p.DocID])

	// Restore also writes a fresh "restore" snapshot.
	rec = h.get(fmt.Sprintf("/api/v1/pages/%d/snapshots", p.ID), resp.AccessToken)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	assert.Equal(t, "restore", list[0]["reason"])
}

func TestM10CrossWorkspaceIsolation(t *testing.T) {
	h, _ := newPagesHarness(t)

	// User A, workspace A-co
	rA, _ := signup(t, h, "alice@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, rA.AccessToken, "ACo", "a-co")

	rec := h.postAuth("/api/v1/workspaces/a-co/pages", `{"title":"alice's doc"}`, rA.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var p pageAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))

	// User B, their own workspace; MUST NOT see alice's page
	rB, _ := signup(t, h, "bob@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, rB.AccessToken, "BCo", "b-co")

	rec = h.get(fmt.Sprintf("/api/v1/pages/%d", p.ID), rB.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code, "cross-tenant read should 404")

	rec = h.postAuth(fmt.Sprintf("/api/v1/pages/%d/auth", p.ID), "{}", rB.AccessToken)
	assert.Equal(t, http.StatusNotFound, rec.Code, "cross-tenant auth issuance should 404")
}

// ---- WOPI test ---------------------------------------------------------

func TestM10WOPITokenRoundtrip(t *testing.T) {
	h, _ := newPagesHarness(t)
	resp, _ := signup(t, h, "creator@m10.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, resp.AccessToken, "M10Co", "m10-co")

	// Upload a .docx stub (file bytes don't matter for this test — WOPI
	// just needs a real files row).
	ws := workspaceBySlug(t, h, resp.AccessToken, "m10-co")
	fileID := uploadDocxStub(t, h, resp.AccessToken, ws.ID)

	// Ask for an edit session. Collabora isn't configured in the test, so
	// the endpoint should 503 cleanly — we confirm that the gate works.
	rec := h.postAuth(fmt.Sprintf("/api/v1/files/%d/edit-session", fileID),
		"{}", resp.AccessToken)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"edit-session should 503 when COLLABORA_URL is empty: %s", rec.Body.String())

	// Validate WOPI token plumbing:
	//  - missing token → 401
	//  - token signed with the wrong key → 401
	//  - token whose file_id doesn't match the path → 403
	rec = h.get(fmt.Sprintf("/api/v1/wopi/files/%d", fileID), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "missing token: %s", rec.Body.String())

	// Wrong signing key → invalid token
	badIss := wopi.NewTokenIssuer([]byte("wrong-key-0123456789abcdef0123456789abcdef"), 10*time.Minute)
	badTok, _, err := badIss.Issue(999, 999, fileID, false)
	require.NoError(t, err)
	rec = h.get(fmt.Sprintf("/api/v1/wopi/files/%d?access_token=%s", fileID, badTok), "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "bad-key token: %s", rec.Body.String())

	// Token for a different file_id than the path → 403
	rightKey := os.Getenv("SLIILS_JWT_SIGNING_KEY")
	iss := wopi.NewTokenIssuer([]byte(rightKey), 10*time.Minute)
	otherTok, _, err := iss.Issue(1, 1, fileID+999, true)
	require.NoError(t, err)
	rec = h.get(fmt.Sprintf("/api/v1/wopi/files/%d?access_token=%s", fileID, otherTok), "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "cross-file token: %s", rec.Body.String())
}

// ---- helpers -----------------------------------------------------------

// workspaceBySlug resolves a workspace row via /me/workspaces so the
// WOPI test can hand its ID to uploadFile.
func workspaceBySlug(t *testing.T, h *testHarness, token, slug string) struct {
	ID int64 `json:"id"`
} {
	t.Helper()
	rec := h.get("/api/v1/me/workspaces", token)
	require.Equal(t, http.StatusOK, rec.Code, "list workspaces: %s", rec.Body.String())
	var rows []struct {
		Workspace struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	for _, r := range rows {
		if r.Workspace.Slug == slug {
			return struct {
				ID int64 `json:"id"`
			}{ID: r.Workspace.ID}
		}
	}
	t.Fatalf("workspace %q not found in me/workspaces", slug)
	return struct {
		ID int64 `json:"id"`
	}{}
}

// uploadDocxStub uploads a tiny file pretending to be a .docx using the
// shared uploadFile helper. Returns the new file id.
func uploadDocxStub(t *testing.T, h *testHarness, token string, workspaceID int64) int64 {
	t.Helper()
	dto := uploadFile(t, h, token, workspaceID, "doc.docx", []byte("PK\x03\x04stub-docx-bytes"))
	idF, ok := dto["id"].(float64)
	require.True(t, ok, "file dto missing id: %v", dto)
	return int64(idF)
}

// mintWOPIToken builds a WOPI access token using the same issuer the
// server uses. Tests exercise the Parse side via the /wopi endpoints.
func mintWOPIToken(t *testing.T, fileID, userID, workspaceID int64, canWrite bool) string {
	t.Helper()
	// 10 minutes is fine for a test
	_ = time.Second
	// Re-use the env key set by newPagesHarness
	key := os.Getenv("SLIILS_JWT_SIGNING_KEY")
	require.NotEmpty(t, key)
	iss := wopi.NewTokenIssuer([]byte(key), 10*time.Minute)
	tok, _, err := iss.Issue(userID, workspaceID, fileID, canWrite)
	require.NoError(t, err)
	return tok
}
