package secretbox

import (
	"bytes"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	key, err := MustParseKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte("refresh-token-abc123-!@#$%^&*()")
	ciphertext, err := box.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := box.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	key, _ := MustParseKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	box, _ := New(key)
	ct, _ := box.Encrypt([]byte("secret"))
	// Flip one character (safely stays in base64url alphabet).
	bad := []rune(ct)
	if bad[len(bad)-1] == 'A' {
		bad[len(bad)-1] = 'B'
	} else {
		bad[len(bad)-1] = 'A'
	}
	if _, err := box.Decrypt(string(bad)); err == nil {
		t.Fatal("expected decrypt failure on tampered ciphertext")
	}
}

func TestWrongKeyFails(t *testing.T) {
	keyA, _ := MustParseKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	keyB, _ := MustParseKey("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	boxA, _ := New(keyA)
	boxB, _ := New(keyB)
	ct, _ := boxA.Encrypt([]byte("secret"))
	if _, err := boxB.Decrypt(ct); err == nil {
		t.Fatal("expected cross-key decrypt failure")
	}
}
