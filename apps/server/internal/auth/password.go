// Package auth provides primitives for password hashing, token generation, and
// JWT issuance/validation. All functions are safe for concurrent use.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters per tech-spec §3.2: m=64MB, t=3, p=4.
type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		Memory:      64 * 1024,
		Iterations:  3,
		Parallelism: 4,
		SaltLength:  16,
		KeyLength:   32,
	}
}

type PasswordHasher struct {
	Params PasswordParams
}

func NewDefaultHasher() *PasswordHasher {
	return &PasswordHasher{Params: DefaultPasswordParams()}
}

// Hash returns an encoded argon2id string of the form:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt-b64>$<key-b64>
//
// Matches RFC 9106 / libsodium convention; self-describing so we can rotate
// parameters later without migration scripts.
func (h *PasswordHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password required")
	}

	salt := make([]byte, h.Params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.Params.Iterations, h.Params.Memory, h.Params.Parallelism, h.Params.KeyLength)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.Params.Memory, h.Params.Iterations, h.Params.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	), nil
}

// Verify is constant-time. Returns true only if the password matches the stored hash.
// Returns a non-nil error only for malformed hash strings — a mismatched password
// is signaled by (false, nil).
func (h *PasswordHasher) Verify(password, encodedHash string) (bool, error) {
	if password == "" || encodedHash == "" {
		return false, nil
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false, errors.New("malformed argon2id hash")
	}
	if parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported hash algorithm %q", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse version: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("parse params: %w", err)
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode key: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(want, got) == 1, nil
}
