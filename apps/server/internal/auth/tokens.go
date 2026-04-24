package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// RandomToken generates a URL-safe opaque token with `numBytes` of entropy.
// The caller stores its SHA-256 hash; only the raw token ever leaves the server.
// 32 bytes = 256 bits of entropy, which is the floor for any auth token.
func RandomToken(numBytes int) (string, error) {
	if numBytes < 16 {
		return "", fmt.Errorf("token entropy too low: %d bytes (want >= 16)", numBytes)
	}
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns SHA-256(token). Database stores this; the raw token goes
// to the user (email link, refresh cookie) and is never persisted.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
