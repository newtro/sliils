package apps

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestClientSecretRoundtrip(t *testing.T) {
	plain, hash, err := NewClientSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "slis-secret-") {
		t.Fatalf("unexpected secret prefix: %q", plain)
	}
	if !VerifyClientSecret(plain, hash) {
		t.Fatal("correct secret failed verification")
	}
	if VerifyClientSecret("slis-secret-bogus", hash) {
		t.Fatal("wrong secret verified")
	}
}

func TestAccessTokenRoundtrip(t *testing.T) {
	plain, hash, err := NewAccessToken(42)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "slis-xat-42-") {
		t.Fatalf("unexpected token format: %q", plain)
	}
	id, secret, err := ParseAccessToken(plain)
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id: got %d want 42", id)
	}
	if !VerifyAccessTokenSecret(secret, hash) {
		t.Fatal("secret did not verify against its hash")
	}
}

func TestParseAccessTokenBadInputs(t *testing.T) {
	cases := []string{
		"",
		"not-a-token",
		"slis-xat-",
		"slis-xat-abc-secret",      // non-numeric id
		"slis-xat-42",              // missing secret
	}
	for _, raw := range cases {
		if _, _, err := ParseAccessToken(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

func TestPKCES256(t *testing.T) {
	verifier := "pkce-verifier-that-is-long-enough-to-pass-rfc7636-minimum"
	// Generate the matching challenge by running the same hash the client would.
	plain, hash, err := NewClientSecret()
	_ = plain
	_ = hash
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately construct a known pair via the helper. We know the
	// S256 branch hashes + base64url-no-pad encodes.
	challenge := pkceS256(verifier)
	if !VerifyPKCE(challenge, "S256", verifier) {
		t.Fatal("matching verifier failed")
	}
	if VerifyPKCE(challenge, "S256", "wrong-verifier") {
		t.Fatal("wrong verifier verified")
	}
	if VerifyPKCE(challenge, "MD5-is-not-supported", verifier) {
		t.Fatal("unknown method should return false")
	}
}

func TestManifestValidateUnknownScopeRejected(t *testing.T) {
	m := &Manifest{Scopes: []string{"chat:write", "do-everything"}}
	if err := m.Validate(); err == nil {
		t.Fatal("unknown scope should fail validation")
	}
}

func TestManifestValidateRedirectURIShape(t *testing.T) {
	m := &Manifest{RedirectURIs: []string{"not a url"}}
	if err := m.Validate(); err == nil {
		t.Fatal("invalid redirect uri should fail")
	}
	m2 := &Manifest{RedirectURIs: []string{"https://example.com/cb"}}
	if err := m2.Validate(); err != nil {
		t.Fatalf("valid redirect should pass: %v", err)
	}
}

func TestDecodeManifestEmptyIsZero(t *testing.T) {
	m, err := DecodeManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil || len(m.Scopes) != 0 {
		t.Fatalf("empty input should yield zero-value manifest, got %+v", m)
	}
}

func TestEncodeDecodeScopes(t *testing.T) {
	in := []string{"chat:write", "channels:read"}
	raw := EncodeScopes(in)
	var back []string
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0] != "chat:write" {
		t.Fatalf("unexpected roundtrip: %v", back)
	}
}

// pkceS256 mirrors VerifyPKCE's S256 branch so the test can generate
// a matching challenge for a given verifier.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
