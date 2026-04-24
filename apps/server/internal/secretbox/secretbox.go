// Package secretbox wraps AES-256-GCM for at-rest encryption of
// sensitive strings (OAuth refresh tokens, today; API tokens, tomorrow).
//
// Threat model: a database dump without the app key must not leak
// usable secrets. We assume the key itself lives in a separate secret
// store (env var set by the operator, future KMS/Vault integration).
//
// Ciphertext format:
//     base64url( nonce || ciphertext || tag )
// where nonce is 12 bytes. No embedded key id — v1 has a single key, so
// rotation means re-encrypting every row with the new key. When multi-
// key rotation matters (v2), prepend a 1-byte key-index.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// NonceSize is the AES-GCM standard 96-bit nonce.
const NonceSize = 12

// Box holds the AEAD primitive plus a reference to the key material for
// future rotate-vs-decrypt decisions.
type Box struct {
	aead cipher.AEAD
}

// New accepts a 32-byte key (AES-256). Use DeriveKey to convert a
// password/hex string to the right length deterministically — or
// require the operator to hand us an already-32-byte value via env.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext and returns a base64-url string suitable for
// storage. Safe to call concurrently; AEAD's Seal is stateless.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Returns an error if the ciphertext was
// truncated, tampered with, or produced by a different key.
func (b *Box) Decrypt(encoded string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(raw) < NonceSize+1 {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, body := raw[:NonceSize], raw[NonceSize:]
	plain, err := b.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("aead open: %w", err)
	}
	return plain, nil
}

// MustParseKey converts a hex string into a 32-byte key. Panics on
// anything that can't produce exactly 32 bytes — callers should only
// use this on operator-supplied config, not user input.
func MustParseKey(hex32 string) ([]byte, error) {
	b, err := parseHexOrRaw(hex32)
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(b))
	}
	return b, nil
}

// parseHexOrRaw accepts either a 64-char hex string or a 32-byte raw
// string. Gives operators flexibility without making the config format
// brittle.
func parseHexOrRaw(s string) ([]byte, error) {
	// First try hex.
	if len(s) == 64 {
		decoded := make([]byte, 32)
		for i := 0; i < 32; i++ {
			hi, err := hexDigit(s[i*2])
			if err != nil {
				goto tryRaw
			}
			lo, err := hexDigit(s[i*2+1])
			if err != nil {
				goto tryRaw
			}
			decoded[i] = byte(hi<<4 | lo)
		}
		return decoded, nil
	tryRaw:
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("key must be 32 raw bytes or 64 hex chars, got %d", len(s))
}

func hexDigit(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, nil
	}
	return 0, fmt.Errorf("not hex: %c", b)
}
