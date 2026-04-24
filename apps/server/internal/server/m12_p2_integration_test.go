//go:build integration

package server_test

// M12-P2 integration tests: webhooks + bot API + slash commands.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installTestApp runs the full dev-portal + OAuth flow to produce an
// access token + installation for a freshly-created workspace.
// Returns (accessToken, installationID, workspaceSlug, adminAccessToken).
func installTestApp(t *testing.T, h *testHarness) (botToken string, installID int64, wsSlug, adminToken string) {
	t.Helper()
	// Developer creates app
	devResp, _ := signup(t, h, "dev-p2@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	body := `{"slug":"bot-app","name":"Bot App","manifest":{
		"scopes":["chat:write","channels:read","bot","commands"],
		"redirect_uris":["http://localhost:9999/cb"],
		"bot_user":{"display_name":"Testbot"}
	}}`
	rec := h.postAuth("/api/v1/dev/apps", body, devResp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create app: %s", rec.Body.String())
	var created struct {
		App struct {
			ClientID string `json:"client_id"`
		} `json:"app"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// Admin signs up + creates workspace
	adminResp, _ := signup(t, h, "admin-p2@m12.test", "correct-horse-battery-staple")
	drainEmails(h)
	createWorkspace(t, h, adminResp.AccessToken, "P2 Co", "p2co")

	// OAuth dance
	verifier := "a-long-pkce-verifier-for-rfc7636-minimum-length-ok-yes-thats-right"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authorizeURL := fmt.Sprintf(
		"/api/v1/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&workspace_slug=p2co",
		url.QueryEscape(created.App.ClientID),
		url.QueryEscape("http://localhost:9999/cb"),
		url.QueryEscape("chat:write channels:read bot commands"),
		url.QueryEscape(challenge),
	)
	rec = h.get(authorizeURL, adminResp.AccessToken)
	require.Equal(t, http.StatusOK, rec.Code, "authorize: %s", rec.Body.String())
	var authz struct {
		RedirectTo string `json:"redirect_to"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &authz))
	u, _ := url.Parse(authz.RedirectTo)
	code := u.Query().Get("code")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://localhost:9999/cb")
	form.Set("client_id", created.App.ClientID)
	form.Set("code_verifier", verifier)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(tokRec, req)
	require.Equal(t, http.StatusOK, tokRec.Code, "token: %s", tokRec.Body.String())
	var tok struct {
		AccessToken    string `json:"access_token"`
		InstallationID int64  `json:"installation_id"`
	}
	require.NoError(t, json.Unmarshal(tokRec.Body.Bytes(), &tok))
	return tok.AccessToken, tok.InstallationID, "p2co", adminResp.AccessToken
}

// ---- bot API -----------------------------------------------------------

func TestM12P2AuthTest(t *testing.T) {
	h := newAppsHarness(t)
	botToken, _, _, _ := installTestApp(t, h)

	rec := h.get("/api/v1/auth.test", botToken)
	require.Equal(t, http.StatusOK, rec.Code, "auth.test: %s", rec.Body.String())
	var out struct {
		OK     bool     `json:"ok"`
		Scopes []string `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, out.OK)
	assert.Contains(t, out.Scopes, "chat:write")
}

func TestM12P2AuthTestBadToken(t *testing.T) {
	h := newAppsHarness(t)
	_ = installTestApp // ensure server is wired

	rec := h.get("/api/v1/auth.test", "slis-xat-1-bogus")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = h.get("/api/v1/auth.test", "not-even-close")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestM12P2ChatPostMessage(t *testing.T) {
	h := newAppsHarness(t)
	botToken, _, wsSlug, adminToken := installTestApp(t, h)

	// Find the default channel
	rec := h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var channels []struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &channels))
	require.NotEmpty(t, channels)
	chID := channels[0].ID

	// Bot posts a message
	body := fmt.Sprintf(`{"channel":"%d","text":"hello from bot","blocks":[
		{"type":"section","text":{"type":"mrkdwn","text":"*hello* from bot"}}
	]}`, chID)
	rec = h.postAuth("/api/v1/chat.postMessage", body, botToken)
	require.Equal(t, http.StatusOK, rec.Code, "post: %s", rec.Body.String())
	var out struct {
		OK        bool  `json:"ok"`
		Channel   int64 `json:"channel"`
		MessageID int64 `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.True(t, out.OK)
	assert.Equal(t, chID, out.Channel)
	assert.Greater(t, out.MessageID, int64(0))
}

func TestM12P2ChatPostMessageRejectsBadBlocks(t *testing.T) {
	h := newAppsHarness(t)
	botToken, _, wsSlug, adminToken := installTestApp(t, h)
	rec := h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &channels)

	// Section block with no text or fields → 400
	body := fmt.Sprintf(`{"channel":"%d","text":"x","blocks":[{"type":"section"}]}`, channels[0].ID)
	rec = h.postAuth("/api/v1/chat.postMessage", body, botToken)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ---- incoming webhooks -------------------------------------------------

func TestM12P2IncomingWebhookLifecycle(t *testing.T) {
	h := newAppsHarness(t)
	_, _, wsSlug, adminToken := installTestApp(t, h)
	rec := h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &channels)
	chID := channels[0].ID

	// Admin creates a webhook
	body := fmt.Sprintf(`{"name":"grafana","channel_id":%d,"require_secret":false}`, chID)
	rec = h.postAuth("/api/v1/workspaces/"+wsSlug+"/webhooks/incoming", body, adminToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create: %s", rec.Body.String())
	var created struct {
		Webhook struct {
			ID  int64  `json:"id"`
			URL string `json:"url"`
		} `json:"webhook"`
		SigningSecret string `json:"signing_secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Webhook.URL)
	// Extract the path after /api/v1
	idx := strings.Index(created.Webhook.URL, "/api/v1/hooks/")
	require.GreaterOrEqual(t, idx, 0)
	publicPath := created.Webhook.URL[idx:]

	// Public POST arrives
	payload := `{"text":"Build #42 passed","blocks":[{"type":"header","text":{"type":"plain_text","text":"Build Report"}}]}`
	req := httptest.NewRequest(http.MethodPost, publicPath, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(postRec, req)
	require.Equal(t, http.StatusOK, postRec.Code, "post to webhook: %s", postRec.Body.String())

	// Admin lists — the webhook is there
	rec = h.get("/api/v1/workspaces/"+wsSlug+"/webhooks/incoming", adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	require.NotEmpty(t, list)

	// Delete
	delReq := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/webhooks/incoming/%d", created.Webhook.ID), nil)
	delReq.Header.Set("Authorization", "Bearer "+adminToken)
	delRec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(delRec, delReq)
	assert.Equal(t, http.StatusNoContent, delRec.Code)

	// Posting to the deleted webhook → 404
	req = httptest.NewRequest(http.MethodPost, publicPath, strings.NewReader(payload))
	postRec = httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(postRec, req)
	assert.Equal(t, http.StatusNotFound, postRec.Code)
}

func TestM12P2IncomingWebhookSecretRequired(t *testing.T) {
	h := newAppsHarness(t)
	_, _, wsSlug, adminToken := installTestApp(t, h)
	rec := h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &channels)

	body := fmt.Sprintf(`{"name":"secured","channel_id":%d,"require_secret":true}`, channels[0].ID)
	rec = h.postAuth("/api/v1/workspaces/"+wsSlug+"/webhooks/incoming", body, adminToken)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created struct {
		Webhook       struct{ URL string `json:"url"` } `json:"webhook"`
		SigningSecret string `json:"signing_secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.SigningSecret)
	path := created.Webhook.URL[strings.Index(created.Webhook.URL, "/api/v1/"):]

	// Without the secret header → 401
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec2, req)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)

	// With the wrong secret → 401
	req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SliilS-Request-Secret", "whsec_wrong")
	rec2 = httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec2, req)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)

	// With the right secret → 200
	req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SliilS-Request-Secret", created.SigningSecret)
	rec2 = httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec2, req)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

// ---- slash commands ----------------------------------------------------

func TestM12P2SlashCommandRoundtrip(t *testing.T) {
	h := newAppsHarness(t)
	_, installID, wsSlug, adminToken := installTestApp(t, h)

	// External target that echoes the invocation back
	var received atomic.Value
	received.Store("")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response_type":"ephemeral","text":"pong"}`))
	}))
	defer target.Close()

	// Admin registers the command
	body := fmt.Sprintf(`{"command":"/ping","target_url":%q,"description":"ping-pong"}`, target.URL+"/slash")
	rec := h.postAuth(fmt.Sprintf("/api/v1/installations/%d/slash-commands", installID), body, adminToken)
	require.Equal(t, http.StatusCreated, rec.Code, "register: %s", rec.Body.String())

	// User invokes
	rec = h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
	var channels []struct{ ID int64 `json:"id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &channels)

	body = fmt.Sprintf(`{"command":"/ping","text":"hello world","channel_id":%d}`, channels[0].ID)
	rec = h.postAuth("/api/v1/workspaces/"+wsSlug+"/slash-commands/invoke", body, adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "invoke: %s", rec.Body.String())
	var out struct {
		ResponseType string `json:"response_type"`
		Text         string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "ephemeral", out.ResponseType)
	assert.Equal(t, "pong", out.Text)

	// Target got the canonical Slack-shaped payload
	forwarded := received.Load().(string)
	assert.Contains(t, forwarded, `"command":"/ping"`)
	assert.Contains(t, forwarded, `"text":"hello world"`)
}

func TestM12P2SlashCommandUniquePerWorkspace(t *testing.T) {
	h := newAppsHarness(t)
	_, installID, _, adminToken := installTestApp(t, h)

	body := `{"command":"/dup","target_url":"https://example.com/a"}`
	rec := h.postAuth(fmt.Sprintf("/api/v1/installations/%d/slash-commands", installID), body, adminToken)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Second registration with the same command → 409
	rec = h.postAuth(fmt.Sprintf("/api/v1/installations/%d/slash-commands", installID), body, adminToken)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// ---- outgoing webhooks (CRUD only; delivery is P2.1) -------------------

func TestM12P2OutgoingWebhookCRUD(t *testing.T) {
	h := newAppsHarness(t)
	_, installID, _, adminToken := installTestApp(t, h)

	body := `{"event_pattern":"message.created","target_url":"https://example.com/hook"}`
	rec := h.postAuth(fmt.Sprintf("/api/v1/installations/%d/webhooks/outgoing", installID), body, adminToken)
	require.Equal(t, http.StatusCreated, rec.Code, "create: %s", rec.Body.String())
	var created struct {
		Webhook       struct{ ID int64 `json:"id"` } `json:"webhook"`
		SigningSecret string `json:"signing_secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Greater(t, created.Webhook.ID, int64(0))
	assert.NotEmpty(t, created.SigningSecret)

	rec = h.get(fmt.Sprintf("/api/v1/installations/%d/webhooks/outgoing", installID), adminToken)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	require.Len(t, list, 1)

	// Second identical → 409
	rec = h.postAuth(fmt.Sprintf("/api/v1/installations/%d/webhooks/outgoing", installID), body, adminToken)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// ---- Block-Kit validator stress tests ----------------------------------

func TestM12P2BlockKitValidation(t *testing.T) {
	cases := []struct {
		name     string
		blocks   string
		wantOK   bool
	}{
		{"empty array", `[]`, true},
		{"section mrkdwn", `[{"type":"section","text":{"type":"mrkdwn","text":"hi"}}]`, true},
		{"header plain", `[{"type":"header","text":{"type":"plain_text","text":"Title"}}]`, true},
		{"section no text", `[{"type":"section"}]`, false},
		{"header with mrkdwn", `[{"type":"header","text":{"type":"mrkdwn","text":"no"}}]`, false},
		{"unknown block accepted", `[{"type":"whatever","block_id":"x"}]`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Wrap in a bot-chat request to run through full validation.
			h := newAppsHarness(t)
			botToken, _, wsSlug, adminToken := installTestApp(t, h)
			chRec := h.get("/api/v1/workspaces/"+wsSlug+"/channels", adminToken)
			var channels []struct{ ID int64 `json:"id"` }
			_ = json.Unmarshal(chRec.Body.Bytes(), &channels)
			body := fmt.Sprintf(`{"channel":"%d","text":"x","blocks":%s}`, channels[0].ID, c.blocks)
			rec := h.postAuth("/api/v1/chat.postMessage", body, botToken)
			if c.wantOK {
				assert.Equal(t, http.StatusOK, rec.Code, "blocks %q: %s", c.blocks, rec.Body.String())
			} else {
				assert.Equal(t, http.StatusBadRequest, rec.Code, "blocks %q should reject: %s", c.blocks, rec.Body.String())
			}
		})
	}
	_ = bytes.NewReader
}
