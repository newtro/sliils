// Package wopi implements SliilS's Collabora Online integration (M10-P2).
//
// WOPI (Web Application Open Platform Interface) is the protocol Collabora
// and Microsoft Office Online both speak. The editor makes three HTTP calls
// back to us per session:
//
//   GET  /wopi/files/{id}          → CheckFileInfo: JSON metadata + perms
//   GET  /wopi/files/{id}/contents → GetFile:       raw document bytes
//   POST /wopi/files/{id}/contents → PutFile:       accept new version
//
// Every call carries an `access_token` query param that we sign on the way
// out and validate on the way back in. Tokens are short-lived (10 min) and
// bind user + file + write-permission, so a leaked token can neither
// escalate privilege nor be reused past its TTL.
//
// Discovery: Collabora publishes `/hosting/discovery` — an XML manifest
// mapping MIME types to editor URLs. We parse it once on first use and
// cache for 24h; the "Open in editor" endpoint picks the right URL based
// on the file's mime type.
package wopi

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---- token issuance ----------------------------------------------------

// Claims is the body of a WOPI access_token.
//
// Scope is deliberately narrow: (user, file, write-permission, exp). The
// token cannot be used to access any other file; a compromised WOPI
// endpoint at Collabora can only do what the token already allows.
type Claims struct {
	UserID      int64 `json:"uid,string"`
	WorkspaceID int64 `json:"ws,string"`
	FileID      int64 `json:"fid,string"`
	CanWrite    bool  `json:"w"`
	jwt.RegisteredClaims
}

type TokenIssuer struct {
	signingKey []byte
	ttl        time.Duration
}

func NewTokenIssuer(signingKey []byte, ttl time.Duration) *TokenIssuer {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &TokenIssuer{signingKey: signingKey, ttl: ttl}
}

func (t *TokenIssuer) TTL() time.Duration { return t.ttl }

func (t *TokenIssuer) Issue(userID, workspaceID, fileID int64, canWrite bool) (string, time.Time, error) {
	if len(t.signingKey) == 0 {
		return "", time.Time{}, errors.New("wopi issuer: signing key not configured")
	}
	now := time.Now().UTC()
	exp := now.Add(t.ttl)
	c := Claims{
		UserID:      userID,
		WorkspaceID: workspaceID,
		FileID:      fileID,
		CanWrite:    canWrite,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "sliils-wopi",
			Audience:  jwt.ClaimStrings{"sliils-wopi"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(t.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	return signed, exp, nil
}

// ErrInvalidToken collapses signature / expiry / malformed-claim failures
// into a single sentinel so callers don't leak which one tripped.
var ErrInvalidToken = errors.New("wopi: invalid access token")

func (t *TokenIssuer) Parse(raw string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(raw, &c,
		func(tok *jwt.Token) (any, error) {
			if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected alg: %v", tok.Header["alg"])
			}
			return t.signingKey, nil
		},
		jwt.WithIssuer("sliils-wopi"),
		jwt.WithAudience("sliils-wopi"),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// ---- discovery ---------------------------------------------------------

// Action is one entry from Collabora's discovery.xml:
//   <action name="edit" ext="docx" urlsrc="https://.../cool.html?..." />
type Action struct {
	Name   string // "edit", "view", "embedview", ...
	Ext    string
	Mime   string
	URLSrc string
}

// Discovery holds the parsed discovery.xml and indexes it two ways:
// by MIME type (preferred — exact match) and by filename extension.
type Discovery struct {
	FetchedAt time.Time
	ByMime    map[string][]Action
	ByExt     map[string][]Action
}

// DiscoveryClient fetches + caches the Collabora discovery.xml. Exported
// so tests can stub it. Cache TTL is 24h; Collabora discovery changes
// only when the operator deploys a new version.
type DiscoveryClient struct {
	collaboraURL string
	httpClient   *http.Client
	logger       *slog.Logger
	ttl          time.Duration

	mu    sync.Mutex
	cache *Discovery
}

func NewDiscoveryClient(collaboraURL string, logger *slog.Logger) *DiscoveryClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &DiscoveryClient{
		collaboraURL: strings.TrimRight(collaboraURL, "/"),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		ttl:          24 * time.Hour,
	}
}

// Get returns a fresh-enough Discovery, refreshing from Collabora when the
// cache is empty or stale.
func (d *DiscoveryClient) Get(ctx context.Context) (*Discovery, error) {
	d.mu.Lock()
	if d.cache != nil && time.Since(d.cache.FetchedAt) < d.ttl {
		cached := d.cache
		d.mu.Unlock()
		return cached, nil
	}
	d.mu.Unlock()

	disc, err := d.fetch(ctx)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.cache = disc
	d.mu.Unlock()
	return disc, nil
}

// ActionForMime picks the best edit action for a given mime type. Falls
// back to extension-based matching when the exact mime isn't published
// (Collabora 24.x publishes mime on most actions; older releases don't).
func (d *Discovery) ActionForMime(mime, filename, preferredName string) *Action {
	if preferredName == "" {
		preferredName = "edit"
	}
	if actions, ok := d.ByMime[strings.ToLower(mime)]; ok {
		for _, a := range actions {
			if a.Name == preferredName {
				return &a
			}
		}
		if len(actions) > 0 {
			return &actions[0]
		}
	}
	ext := strings.ToLower(strings.TrimPrefix(extFromName(filename), "."))
	if actions, ok := d.ByExt[ext]; ok {
		for _, a := range actions {
			if a.Name == preferredName {
				return &a
			}
		}
		if len(actions) > 0 {
			return &actions[0]
		}
	}
	return nil
}

func (d *DiscoveryClient) fetch(ctx context.Context) (*Discovery, error) {
	if d.collaboraURL == "" {
		return nil, errors.New("wopi: collabora url is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.collaboraURL+"/hosting/discovery", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wopi: fetch discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("wopi: discovery status %d: %s", resp.StatusCode, string(msg))
	}
	var doc struct {
		XMLName xml.Name `xml:"wopi-discovery"`
		NetZone struct {
			Apps []struct {
				Name    string `xml:"name,attr"` // e.g. "writer", "calc"
				Actions []struct {
					Name   string `xml:"name,attr"`
					Ext    string `xml:"ext,attr"`
					Mime   string `xml:"default,attr"` // Collabora puts mime in "default" in newer builds
					URLSrc string `xml:"urlsrc,attr"`
				} `xml:"action"`
			} `xml:"app"`
		} `xml:"net-zone"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("wopi: parse discovery: %w", err)
	}
	disc := &Discovery{
		FetchedAt: time.Now(),
		ByMime:    make(map[string][]Action),
		ByExt:     make(map[string][]Action),
	}
	for _, app := range doc.NetZone.Apps {
		mimeForApp := strings.ToLower(strings.TrimSpace(app.Name))
		for _, a := range app.Actions {
			act := Action{
				Name:   a.Name,
				Ext:    strings.ToLower(a.Ext),
				Mime:   strings.ToLower(firstNonEmpty(a.Mime, mimeForApp)),
				URLSrc: a.URLSrc,
			}
			if act.Mime != "" {
				disc.ByMime[act.Mime] = append(disc.ByMime[act.Mime], act)
			}
			if act.Ext != "" {
				disc.ByExt[act.Ext] = append(disc.ByExt[act.Ext], act)
			}
		}
	}
	return disc, nil
}

// ---- helpers -----------------------------------------------------------

func extFromName(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// IsCollaboraEditable is a fast check the UI can use to hide the
// "Open in editor" button for files Collabora won't handle.
func IsCollaboraEditable(mime, filename string) bool {
	m := strings.ToLower(mime)
	if strings.HasPrefix(m, "application/vnd.openxmlformats-officedocument") ||
		strings.HasPrefix(m, "application/vnd.oasis.opendocument") ||
		m == "application/msword" ||
		m == "application/vnd.ms-excel" ||
		m == "application/vnd.ms-powerpoint" ||
		m == "application/rtf" ||
		m == "text/csv" {
		return true
	}
	switch strings.ToLower(strings.TrimPrefix(extFromName(filename), ".")) {
	case "docx", "xlsx", "pptx",
		"doc", "xls", "ppt",
		"odt", "ods", "odp",
		"rtf", "csv":
		return true
	}
	return false
}
