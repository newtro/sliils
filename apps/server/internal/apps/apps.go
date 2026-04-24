// Package apps owns SliilS's third-party app platform (M12-P1+P2).
//
// Responsibilities:
//
//   - Manifest validation: what scopes an app asks for, what redirect
//     URIs it's allowed to land users at, which slash-commands and
//     event subscriptions it claims.
//   - OAuth 2.0 + PKCE flow: authorization codes, token exchange, token
//     hashing at rest.
//   - Token format + parsing: "slis-xat-{token_id}-{secret}" bearer
//     strings scoped to a single installation.
//   - Scope enforcement helpers used by the bot API handlers in server/.
//
// Everything here is transport-agnostic — no HTTP, no echo. The server
// package owns wire framing and auth middleware.
package apps

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ---- manifest ----------------------------------------------------------

// Manifest is the developer-declared capabilities surface.
// Persisted as JSONB on the apps row; decoded here when we need to
// validate an install request.
type Manifest struct {
	Scopes          []string        `json:"scopes,omitempty"`
	RedirectURIs    []string        `json:"redirect_uris,omitempty"`
	EventSubs       []string        `json:"event_subs,omitempty"`
	SlashCommands   []SlashCommand  `json:"slash_commands,omitempty"`
	BotUser         *BotUserConfig  `json:"bot_user,omitempty"`
	Webhooks        *WebhookConfig  `json:"webhooks,omitempty"`
}

type SlashCommand struct {
	Command     string `json:"command"`      // "/poll"
	URL         string `json:"url"`          // where the invocation POSTs
	Description string `json:"description"`
	UsageHint   string `json:"usage_hint,omitempty"`
}

type BotUserConfig struct {
	DisplayName string `json:"display_name"`
	AlwaysOnline bool  `json:"always_online,omitempty"`
}

type WebhookConfig struct {
	EventsURL string `json:"events_url,omitempty"` // outgoing events fan-out here
}

// KnownScopes defines which scopes SliilS recognises at v1. Unknown
// scopes in a manifest fail validation — this is strict by design so
// developers can't silently ask for permissions we don't understand.
var KnownScopes = map[string]string{
	"chat:write":     "post messages as the bot user",
	"channels:read":  "list public channels + membership",
	"channels:history": "read message history in channels the bot is added to",
	"users:read":     "list workspace members",
	"commands":       "respond to slash commands",
	"incoming-webhook": "receive an incoming webhook URL on install",
	"bot":            "create a bot user in the workspace",
}

// ValidateManifest returns an error describing the first problem it
// finds, or nil. Call-side errors bubble as 400s to the developer
// portal so the shape stays easy to iterate.
func (m *Manifest) Validate() error {
	for _, s := range m.Scopes {
		if _, ok := KnownScopes[s]; !ok {
			return fmt.Errorf("unknown scope %q (see /docs/apps#scopes)", s)
		}
	}
	for _, u := range m.RedirectURIs {
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "https" && p.Scheme != "http") {
			return fmt.Errorf("redirect_uri %q must be an absolute http(s) URL", u)
		}
	}
	for _, c := range m.SlashCommands {
		if !strings.HasPrefix(c.Command, "/") || len(c.Command) < 2 {
			return fmt.Errorf("slash command %q must start with /", c.Command)
		}
	}
	return nil
}

// HasScope reports whether the install's granted scopes include the one
// requested. Case-sensitive on purpose — scopes are strict identifiers.
func HasScope(granted []string, required string) bool {
	for _, s := range granted {
		if s == required {
			return true
		}
	}
	return false
}

// ---- client id / secret -------------------------------------------------

// NewClientID returns a developer-shareable identifier. Stable for the
// lifetime of the app — this is what third parties paste into their
// configs. Prefix makes it obvious it's a SliilS app id in a grep.
func NewClientID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "slis-app-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// NewClientSecret returns the secret part plus its hash. The plain
// secret is shown to the developer ONCE; only the hash is persisted.
func NewClientSecret() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "slis-secret-" + base64.RawURLEncoding.EncodeToString(b)
	hash = hashSecret(plain)
	return
}

// VerifyClientSecret is constant-time so timing attacks can't enumerate
// valid secrets character-by-character.
func VerifyClientSecret(plain, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(hashSecret(plain)), []byte(storedHash)) == 1
}

// ---- authorization codes -----------------------------------------------

// NewAuthorizationCode returns an opaque 256-bit URL-safe code.
func NewAuthorizationCode() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// VerifyPKCE checks code_verifier against code_challenge + method. RFC 7636.
// S256 is the only method new apps should use; plain is accepted for dev tools
// that don't implement SHA-256 (we store which was used so we can deprecate
// `plain` in v1.1 without changing the verify API).
func VerifyPKCE(challenge, method, verifier string) bool {
	switch method {
	case "S256":
		sum := sha256.Sum256([]byte(verifier))
		got := base64.RawURLEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
	case "plain":
		return subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) == 1
	default:
		return false
	}
}

// ---- access tokens -----------------------------------------------------

// Token format: `slis-xat-{token_id}-{secret}`.
//   - `slis-xat` is a constant prefix that makes tokens grep-detectable
//     in logs and lets secret scanners (GitHub, TruffleHog) flag them.
//   - `token_id` is the integer primary key of the app_tokens row. We
//     use it to go from token → DB row in O(1) rather than scanning.
//   - `secret` is a 32-byte base64url-encoded random string. Only the
//     SHA-256 hash is stored.
const tokenPrefix = "slis-xat-"

// NewAccessToken mints a fresh (tokenID, plainToken, hash) tuple.
func NewAccessToken(tokenID int64) (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	plain = fmt.Sprintf("%s%d-%s", tokenPrefix, tokenID, secret)
	hash = hashSecret(secret)
	return
}

// ParseAccessToken splits `slis-xat-{id}-{secret}` into its parts.
// The returned `id` is the DB row id; `secret` is what the caller
// compares against the stored hash.
func ParseAccessToken(raw string) (tokenID int64, secret string, err error) {
	if !strings.HasPrefix(raw, tokenPrefix) {
		return 0, "", ErrInvalidTokenFormat
	}
	rest := strings.TrimPrefix(raw, tokenPrefix)
	sep := strings.IndexByte(rest, '-')
	if sep <= 0 {
		return 0, "", ErrInvalidTokenFormat
	}
	idPart := rest[:sep]
	secret = rest[sep+1:]
	tokenID, err = strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		return 0, "", ErrInvalidTokenFormat
	}
	return tokenID, secret, nil
}

// VerifyAccessTokenSecret is constant-time.
func VerifyAccessTokenSecret(secret, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(storedHash)) == 1
}

// ErrInvalidTokenFormat is a sentinel callers can check without
// inspecting the error string.
var ErrInvalidTokenFormat = errors.New("apps: invalid access token format")

// ---- manifest helpers --------------------------------------------------

// DecodeManifest lifts a JSONB column into a typed Manifest. Empty
// bytes decode to a zero-value Manifest rather than an error.
func DecodeManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return &Manifest{}, nil
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("apps: decode manifest: %w", err)
	}
	return &m, nil
}

// EncodeScopes persists an `[]string` as JSONB. Centralising it keeps
// the handlers free of `json.Marshal` boilerplate.
func EncodeScopes(scopes []string) []byte {
	b, _ := json.Marshal(scopes)
	return b
}

// DecodeScopes lifts a JSONB `["a","b"]` back to `[]string`.
func DecodeScopes(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

// ---- internal ----------------------------------------------------------

func hashSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
