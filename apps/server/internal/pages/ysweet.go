// Package pages owns SliilS's native collaborative-document integration
// (M10-P1). The server-side responsibilities are thin:
//
//   - Issuing short-lived client auth tokens so browsers can connect to
//     the Y-Sweet websocket layer directly. Y-Sweet handles the realtime
//     Yjs CRDT sync; we just gate who can speak to which document.
//
//   - Persisting periodic snapshots of a doc's state so we can show a
//     version-history UI and survive a Y-Sweet reset. Snapshots are the
//     full `Y.encodeStateAsUpdate` byte string — applying them to a fresh
//     Y.Doc reconstructs the document at snapshot time.
//
// Y-Sweet itself is an out-of-process Rust server (`y-sweet serve`) that
// speaks HTTP for document management and WebSocket for live sync. This
// file is the thin Go client we use to talk to it.
package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ParseURL is a thin wrapper around net/url.Parse exported so the server
// package can rewrite Y-Sweet-issued URLs without pulling another import.
func ParseURL(s string) (*url.URL, error) { return url.Parse(s) }

// Client is the HTTP facade for the Y-Sweet server.
//
// We intentionally expose a small surface: CreateDoc / IssueClientAuth /
// GetSnapshot / Health. Everything else (CRDT merge semantics, WS
// multiplexing) lives in Y-Sweet and the browser SDK.
type Client struct {
	baseURL     string
	serverToken string // optional bearer; empty means an unsecured dev server
	httpClient  *http.Client
	logger      *slog.Logger
}

type Options struct {
	// BaseURL is the Y-Sweet server URL, e.g. http://localhost:8787.
	// Strip trailing slashes — we append paths directly.
	BaseURL string
	// ServerToken is the bearer sent on every request to Y-Sweet. Leave
	// empty when running a local dev instance without --auth. Production
	// MUST set it; `y-sweet gen-auth` produces a matching pair.
	ServerToken string
	// Timeout caps every HTTP round trip. 10s is generous for
	// snapshot reads; IssueClientAuth resolves in <100ms normally.
	Timeout time.Duration
	Logger  *slog.Logger
}

func NewClient(opts Options) (*Client, error) {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		return nil, errors.New("y-sweet base url is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Client{
		baseURL:     base,
		serverToken: opts.ServerToken,
		httpClient:  &http.Client{Timeout: opts.Timeout},
		logger:      opts.Logger,
	}, nil
}

// ---- public API --------------------------------------------------------

// CreateDoc asks Y-Sweet to initialise a document with the given id.
// Idempotent — Y-Sweet returns 200 if the doc already exists.
func (c *Client) CreateDoc(ctx context.Context, docID string) error {
	body, _ := json.Marshal(map[string]string{"docId": docID})
	resp, err := c.do(ctx, http.MethodPost, "/doc/new", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("y-sweet create doc: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("y-sweet create doc: status %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}

// ClientAuth bundles everything the browser needs to open a live
// collaboration session with a document.
type ClientAuth struct {
	URL     string    `json:"url"`      // wss://y-sweet/... bound to the doc
	BaseURL string    `json:"base_url"` // http(s) endpoint Y-Sweet serves the doc at
	DocID   string    `json:"doc_id"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// IssueClientAuth mints a short-lived client auth struct for one document.
// The returned token is scoped to the doc; a compromised client cannot use
// it to access sibling docs.
func (c *Client) IssueClientAuth(ctx context.Context, docID string) (*ClientAuth, error) {
	path := "/doc/" + docID + "/auth"
	resp, err := c.do(ctx, http.MethodPost, path, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("y-sweet issue client auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("y-sweet issue client auth: status %d: %s", resp.StatusCode, string(msg))
	}

	// Y-Sweet response shape. BaseURL may be absent on older builds; the
	// SDK falls back to the configured default in that case, so we just
	// pass whatever we get through.
	var raw struct {
		URL     string `json:"url"`
		BaseURL string `json:"baseUrl"`
		DocID   string `json:"docId"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("y-sweet decode auth: %w", err)
	}
	// Y-Sweet tokens are short-lived (~30 min). We don't get the exact
	// TTL in the response; report a conservative 25-minute window so the
	// client can refresh before expiry.
	return &ClientAuth{
		URL:     raw.URL,
		BaseURL: raw.BaseURL,
		DocID:   firstNonEmpty(raw.DocID, docID),
		Token:   raw.Token,
		Expires: time.Now().Add(25 * time.Minute),
	}, nil
}

// GetSnapshot fetches the full Yjs update bytes for `docID`, suitable for
// persisting in page_snapshots.snapshot_data. The caller is responsible
// for bounding how often this runs; repeated calls are cheap-ish but not
// free for large documents.
func (c *Client) GetSnapshot(ctx context.Context, docID string) ([]byte, error) {
	path := "/doc/" + docID + "/as-update"
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("y-sweet snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrDocNotFound
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("y-sweet snapshot: status %d: %s", resp.StatusCode, string(msg))
	}
	return io.ReadAll(resp.Body)
}

// ApplyUpdate pushes a Yjs update to a document. Used by the restore flow
// to roll a document back to a snapshot.
func (c *Client) ApplyUpdate(ctx context.Context, docID string, update []byte) error {
	path := "/doc/" + docID + "/update"
	resp, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(update))
	if err != nil {
		return fmt.Errorf("y-sweet apply update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("y-sweet apply update: status %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}

// Health is wired into /readyz. A slow or unreachable Y-Sweet surfaces
// there as a readiness failure; we never fail startup on it.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// Y-Sweet's root returns 404 with a JSON body — that's "alive".
	// Only connection errors above would return err here.
	return nil
}

// BaseURL lets callers (e.g., the page-list response) hand a sensible
// fallback base URL to browser SDKs that can't see our internal URL.
func (c *Client) BaseURL() string { return c.baseURL }

// ---- internal ----------------------------------------------------------

// ErrDocNotFound is returned when a document has not yet been created.
var ErrDocNotFound = errors.New("y-sweet: document not found")

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.serverToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.serverToken)
	}
	return c.httpClient.Do(req)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
