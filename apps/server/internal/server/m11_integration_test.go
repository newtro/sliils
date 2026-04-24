//go:build integration

package server_test

// M11 integration tests: device registration, DND controls, web push
// delivery against a stub receiver.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/push"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/migrations"
)

func newPushHarness(t *testing.T) (*testHarness, *push.Service) {
	t.Helper()
	dsn := os.Getenv("SLIILS_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:Fl1pFl0p@localhost:5432/sliils_test?sslmode=disable"
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	resetSchema(t, dsn)
	require.NoError(t, db.RunMigrations(context.Background(), dsn, migrations.FS, ".", logger))

	pool, err := db.Open(context.Background(), dsn, logger)
	require.NoError(t, err)
	ownerPool, err := db.OpenOwner(context.Background(), dsn, logger)
	require.NoError(t, err)

	// Generate real VAPID keys so the /me/push-public-key endpoint
	// returns something non-empty. The fan-out worker delivery path is
	// tested separately via a direct call on the push.Service so we
	// don't need a real browser push endpoint.
	priv, pub, err := push.GenerateVAPIDKeys()
	require.NoError(t, err)

	t.Setenv("SLIILS_JWT_SIGNING_KEY", "integration-test-signing-key-0123456789abcdef")
	t.Setenv("SLIILS_DATABASE_URL", dsn)
	t.Setenv("SLIILS_RESEND_API_KEY", "not-used-noop-sender")
	t.Setenv("SLIILS_SEARCH_ENABLED", "false")
	t.Setenv("SLIILS_CALLS_ENABLED", "false")
	t.Setenv("SLIILS_PAGES_ENABLED", "false")
	t.Setenv("SLIILS_PUSH_ENABLED", "true")
	t.Setenv("SLIILS_VAPID_PUBLIC_KEY", pub)
	t.Setenv("SLIILS_VAPID_PRIVATE_KEY", priv)

	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.PublicBaseURL = "http://testhost"

	pushSvc, err := push.New(push.Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@sliils.local",
		TTLSeconds:      3600,
		Logger:          logger,
	})
	require.NoError(t, err)

	emails := make(chan email.Message, 16)
	srv, err := server.New(cfg, logger, pool, server.Options{
		EmailSender:   email.NoopSender{Sent: emails},
		Limiter:       ratelimit.New(),
		SearchOwnerDB: ownerPool,
		Push:          pushSvc,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		ownerPool.Close()
		pool.Close()
	})

	return &testHarness{t: t, srv: srv, pool: pool, emails: emails, dsn: dsn}, pushSvc
}

// ---- tests -------------------------------------------------------------

func TestM11PushPublicKeyEndpoint(t *testing.T) {
	h, _ := newPushHarness(t)
	resp, _ := signup(t, h, "pushkey@m11.test", "correct-horse-battery-staple")
	drainEmails(h)
	rec := h.get("/api/v1/me/push-public-key", resp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "public-key: %s", rec.Body.String())
	var out struct {
		PublicKey string `json:"public_key"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.NotEmpty(t, out.PublicKey)
	// VAPID public keys decode to 65 bytes (uncompressed P-256 point).
	decoded, err := push.DecodeURLSafe(out.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, 65, len(decoded))
}

func TestM11RegisterAndListDevice(t *testing.T) {
	h, _ := newPushHarness(t)
	resp, _ := signup(t, h, "dev@m11.test", "correct-horse-battery-staple")
	drainEmails(h)

	rec := h.postAuth("/api/v1/me/devices",
		`{"platform":"web","endpoint":"https://fcm.googleapis.com/stub","p256dh":"BP256DHKEY","auth_secret":"AUTHSECRET","label":"Chrome"}`,
		resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "register: %s", rec.Body.String())
	var dto struct {
		ID       int64  `json:"id"`
		Platform string `json:"platform"`
		Label    string `json:"label"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "web", dto.Platform)
	assert.Equal(t, "Chrome", dto.Label)

	// Re-register same endpoint → upsert, same row
	rec = h.postAuth("/api/v1/me/devices",
		`{"platform":"web","endpoint":"https://fcm.googleapis.com/stub","p256dh":"NEWKEY","auth_secret":"NEWAUTH","label":"Chrome v2"}`,
		resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var dto2 struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto2))
	assert.Equal(t, dto.ID, dto2.ID, "idempotent upsert should keep same id")
	assert.Equal(t, "Chrome v2", dto2.Label)

	rec = h.get("/api/v1/me/devices", resp.AccessToken)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
}

func TestM11RegisterWebRequiresKeys(t *testing.T) {
	h, _ := newPushHarness(t)
	resp, _ := signup(t, h, "nokeys@m11.test", "correct-horse-battery-staple")
	drainEmails(h)

	rec := h.postAuth("/api/v1/me/devices",
		`{"platform":"web","endpoint":"https://stub","label":"Chrome"}`,
		resp.AccessToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "web without keys: %s", rec.Body.String())
}

func TestM11DNDPatch(t *testing.T) {
	h, _ := newPushHarness(t)
	resp, _ := signup(t, h, "dnd@m11.test", "correct-horse-battery-staple")
	drainEmails(h)

	// Valid quiet hours
	rec := h.patchAuth("/api/v1/me/dnd",
		`{"quiet_hours_start":1320,"quiet_hours_end":480,"quiet_hours_tz":"America/New_York"}`,
		resp.AccessToken)
	assert.Equal(t, http.StatusNoContent, rec.Code, "valid qh: %s", rec.Body.String())

	// Only one of start/end → 400
	rec = h.patchAuth("/api/v1/me/dnd",
		`{"quiet_hours_start":1320}`, resp.AccessToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Bad timezone → 400
	rec = h.patchAuth("/api/v1/me/dnd",
		`{"quiet_hours_start":1320,"quiet_hours_end":480,"quiet_hours_tz":"Mars/Olympus"}`,
		resp.AccessToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Snooze for an hour
	soon := time.Now().Add(1 * time.Hour).Format(time.RFC3339)
	rec = h.patchAuth("/api/v1/me/dnd",
		fmt.Sprintf(`{"snooze_until":%q}`, soon), resp.AccessToken)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Clear snooze
	rec = h.patchAuth("/api/v1/me/dnd", `{"snooze_until":""}`, resp.AccessToken)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestM11DirectWebPushDelivery(t *testing.T) {
	// Stand up a stub push-service endpoint that accepts POST /push and
	// records what arrived. Exercises the VAPID signing + request shape
	// without needing a real browser.
	var receivedHeader atomic.Value
	receivedHeader.Store("")
	recvMu := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader.Store(r.Header.Get("Authorization"))
		// Web Push spec: 201 Created on success.
		w.WriteHeader(http.StatusCreated)
		select {
		case recvMu <- struct{}{}:
		default:
		}
	}))
	defer receiver.Close()

	_, svc := newPushHarness(t)

	p256dh, authSecret := genFakeSubscriptionKeys(t)
	target := push.Target{
		DeviceID:   1,
		UserID:     1,
		Platform:   "web",
		Endpoint:   receiver.URL + "/push/endpoint",
		P256DH:     p256dh,
		AuthSecret: authSecret,
	}
	payload := push.Payload{MsgID: "42", Type: "mention", TenantURL: "http://sliils.test"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := svc.Deliver(ctx, target, payload)
	require.NoError(t, err, "deliver should succeed with 201 receiver")

	// Must have received a VAPID-signed request.
	select {
	case <-recvMu:
	default:
		t.Fatal("push receiver never got a request")
	}
	auth := receivedHeader.Load().(string)
	assert.True(t, strings.Contains(auth, "vapid") || strings.Contains(auth, "WebPush"),
		"expected VAPID/WebPush auth header, got %q", auth)
}

func TestM11EndpointGoneDisablesDevice(t *testing.T) {
	// Stub returns 410 Gone — our service should translate to push.ErrGone.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer receiver.Close()

	_, svc := newPushHarness(t)

	p256dh, authSecret := genFakeSubscriptionKeys(t)
	target := push.Target{
		Platform:   "web",
		Endpoint:   receiver.URL + "/gone",
		P256DH:     p256dh,
		AuthSecret: authSecret,
	}
	err := svc.Deliver(context.Background(), target, push.Payload{MsgID: "1", Type: "mention"})
	assert.ErrorIs(t, err, push.ErrGone)
}

// genFakeSubscriptionKeys produces a valid P-256 keypair in the
// uncompressed point + 16-byte auth-secret format that browsers hand
// back from PushManager.subscribe(). Lets us exercise the real encrypt
// path in webpush-go without a live browser.
func genFakeSubscriptionKeys(t *testing.T) (p256dh, authSecret string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	// Uncompressed: 0x04 || X || Y  (65 bytes for P-256).
	raw := elliptic.Marshal(priv.Curve, priv.X, priv.Y)
	p256dh = base64.RawURLEncoding.EncodeToString(raw)
	authRaw := make([]byte, 16)
	_, err = rand.Read(authRaw)
	require.NoError(t, err)
	authSecret = base64.RawURLEncoding.EncodeToString(authRaw)
	return
}
