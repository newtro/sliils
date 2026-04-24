package apps

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HMAC signatures for webhook deliveries + slash command invocations (M12-P2).
//
// Our signature shape mirrors Slack's (so existing receivers need
// minimal changes):
//
//   X-SliilS-Request-Timestamp: 1756989123
//   X-SliilS-Signature: v0=<hex>
//
// where <hex> = HMAC-SHA256(secret, "v0:" + timestamp + ":" + body).
//
// The timestamp is checked against a 5-minute replay window.

const (
	SignatureVersion = "v0"
	// SignatureSkew caps how stale a timestamp can be. Keep tight:
	// 5 min is Slack's default and sufficient for any realistic net.
	SignatureSkew = 5 * time.Minute
)

// NewWebhookSecret returns a plaintext secret + its SHA-256 hash. The
// plaintext goes to the developer ONCE; the hash lives in the DB.
func NewWebhookSecret() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "whsec_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(sum[:])
	return
}

// VerifyWebhookSecret checks a provided plaintext against a stored hash.
func VerifyWebhookSecret(plain, storedHash string) bool {
	sum := sha256.Sum256([]byte(plain))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(storedHash)) == 1
}

// SignBody produces the value of the X-SliilS-Signature header for a
// given (timestamp, body, secret) tuple. Used both by our outgoing
// fan-out worker and by callers that want to mirror the Slack
// verify-request-signature idiom in tests.
func SignBody(secret string, ts time.Time, body []byte) string {
	base := fmt.Sprintf("%s:%d:%s", SignatureVersion, ts.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	return SignatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature returns nil when the signature matches AND the
// timestamp is within the 5-minute skew window; returns an error
// otherwise. Constant-time to avoid signature-probe attacks.
func VerifySignature(secret string, tsHeader, sigHeader string, body []byte) error {
	if tsHeader == "" || sigHeader == "" {
		return fmt.Errorf("signature: missing timestamp or signature header")
	}
	tsUnix, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("signature: bad timestamp %q", tsHeader)
	}
	ts := time.Unix(tsUnix, 0)
	if time.Since(ts) > SignatureSkew || time.Until(ts) > SignatureSkew {
		return fmt.Errorf("signature: timestamp outside skew window")
	}
	expected := SignBody(secret, ts, body)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(sigHeader))) != 1 {
		return fmt.Errorf("signature: mismatch")
	}
	return nil
}
