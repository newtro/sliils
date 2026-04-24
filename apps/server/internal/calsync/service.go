package calsync

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sliils/sliils/apps/server/internal/secretbox"
)

// Service is the glue layer between HTTP handlers and providers:
//   - provider lookup by name
//   - state signing/verifying for the OAuth redirect round-trip
//   - encryption of refresh tokens at rest
//
// Held by the Server as `s.calSync`; nil when M9-P3 is disabled.
type Service struct {
	providers map[string]Provider
	box       *secretbox.Box
	stateKey  []byte
}

// Options collects what the Server's main.go wires at startup.
type Options struct {
	EncryptionKey string // 32 raw bytes or 64 hex chars
	StateHMACKey  []byte // any length; typically the server's JWT signing key

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	MicrosoftClientID     string
	MicrosoftClientSecret string
	MicrosoftRedirectURL  string
}

func NewService(opts Options) (*Service, error) {
	keyBytes, err := secretbox.MustParseKey(opts.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("calendar encryption key: %w", err)
	}
	box, err := secretbox.New(keyBytes)
	if err != nil {
		return nil, err
	}
	if len(opts.StateHMACKey) == 0 {
		return nil, errors.New("calsync state hmac key required")
	}
	svc := &Service{
		providers: make(map[string]Provider),
		box:       box,
		stateKey:  opts.StateHMACKey,
	}
	if opts.GoogleClientID != "" {
		svc.providers["google"] = NewGoogle(opts.GoogleClientID, opts.GoogleClientSecret, opts.GoogleRedirectURL)
	}
	if opts.MicrosoftClientID != "" {
		svc.providers["microsoft"] = NewMicrosoft(opts.MicrosoftClientID, opts.MicrosoftClientSecret, opts.MicrosoftRedirectURL)
	}
	return svc, nil
}

// Provider returns the configured provider for a given key; (nil, false)
// if not configured.
func (s *Service) Provider(name string) (Provider, bool) {
	p, ok := s.providers[name]
	return p, ok
}

// Providers returns every configured provider (for the pull worker).
func (s *Service) Providers() map[string]Provider {
	return s.providers
}

// Encrypt / Decrypt forward to the AEAD box.
func (s *Service) Encrypt(plain []byte) (string, error)   { return s.box.Encrypt(plain) }
func (s *Service) Decrypt(ciphertext string) ([]byte, error) { return s.box.Decrypt(ciphertext) }

// ---- OAuth state signing -----------------------------------------------

// State format: base64url(userID + "." + nonce + "." + expiry + "." + hex(hmac))
// The nonce prevents replay; expiry (15 min) bounds the window; HMAC
// binds it all to our state key so the provider callback can't accept
// a forged state.

const stateValidity = 15 * time.Minute

// SignState creates a URL-safe state value binding this OAuth flow to
// the given userID.
func (s *Service) SignState(userID int64) (string, error) {
	nonceBytes := make([]byte, 12)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	expiry := time.Now().Add(stateValidity).Unix()
	payload := fmt.Sprintf("%d.%s.%d", userID, nonce, expiry)
	mac := hmac.New(sha256.New, s.stateKey)
	mac.Write([]byte(payload))
	sum := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "." + sum)), nil
}

// VerifyState confirms the given state was signed by us + is within the
// 15-minute validity window + carries a valid userID.
func (s *Service) VerifyState(encoded string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, fmt.Errorf("decode state: %w", err)
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 4 {
		return 0, errors.New("state: malformed")
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("state: bad userID")
	}
	expiry, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, errors.New("state: bad expiry")
	}
	if time.Now().Unix() > expiry {
		return 0, errors.New("state: expired")
	}
	// Recompute HMAC over first three components.
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	mac := hmac.New(sha256.New, s.stateKey)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[3])) {
		return 0, errors.New("state: signature mismatch")
	}
	return userID, nil
}

// Forward a short context-based cancel helper for provider calls. Avoids
// every caller reinventing a timeout.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

var _ = contextWithTimeout // reserved for worker use
