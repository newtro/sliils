package search

import (
	"fmt"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

// TokenIssuer turns (workspace, user, ttl) into a Meilisearch tenant token.
//
// Tenant tokens are HMAC-signed JWTs Meilisearch itself validates on every
// search request. The signing secret is the parent search API key; the token
// carries a `searchRules` claim scoping what the holder is allowed to see.
//
// We bake the following filter into every token:
//
//	workspace_id = <W>
//	AND ( channel_type = "public" OR channel_member_ids = <U> )
//
// Meilisearch enforces this at query time — even if the client forges its
// own `filter` parameter, the tenant-token filter is applied by AND-ing in
// the server-side rule. A compromised client can therefore never reach
// another tenant's workspace or a private channel they don't belong to.
//
// See threat #2 in docs/kickoff/tech-spec.md §3.1.
type TokenIssuer struct {
	svc         meilisearch.ServiceManager
	index       string
	apiKeyUID   string
	apiKeyValue string // used as the HMAC secret
}

// NewTokenIssuer wires the issuer to a client + parent search key. Callers
// typically fetch the key via Client.GetOrCreateSearchKey at startup and
// pass it here.
func NewTokenIssuer(c *Client, parentKey *meilisearch.Key) (*TokenIssuer, error) {
	if parentKey == nil {
		return nil, fmt.Errorf("parent key is nil")
	}
	if parentKey.UID == "" || parentKey.Key == "" {
		return nil, fmt.Errorf("parent key is missing UID or Key material")
	}
	return &TokenIssuer{
		svc:         c.svc,
		index:       c.messageIndex,
		apiKeyUID:   parentKey.UID,
		apiKeyValue: parentKey.Key,
	}, nil
}

// TenantToken is the issuer output. The client stores Token in memory for
// the remainder of the session and includes it in direct Meilisearch calls
// as a Bearer; ExpiresAt drives the client's refresh logic.
type TenantToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	IndexUID  string    `json:"index_uid"`
}

// Issue mints a tenant token for (workspaceID, userID) with the configured
// filter. TTL is clamped to [1 minute, 24 hours] so fat-fingered configs
// can't issue years-long tokens.
func (ti *TokenIssuer) Issue(workspaceID, userID int64, ttl time.Duration) (*TenantToken, error) {
	if ttl < time.Minute {
		ttl = time.Minute
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	expires := time.Now().Add(ttl).UTC()

	filter := BuildFilter(
		fmt.Sprintf("workspace_id = %d", workspaceID),
		fmt.Sprintf("(channel_type = public OR channel_member_ids = %d)", userID),
	)

	rules := map[string]interface{}{
		ti.index: map[string]interface{}{
			"filter": filter,
		},
	}

	// We sign with the parent key's raw secret so Meilisearch can verify
	// via the same key lookup on its side.
	token, err := ti.svc.GenerateTenantToken(ti.apiKeyUID, rules, &meilisearch.TenantTokenOptions{
		APIKey:    ti.apiKeyValue,
		ExpiresAt: expires,
	})
	if err != nil {
		return nil, fmt.Errorf("generate tenant token: %w", err)
	}

	return &TenantToken{
		Token:     token,
		ExpiresAt: expires,
		IndexUID:  ti.index,
	}, nil
}

// Filter returns the visibility filter that would be baked into a token for
// (workspaceID, userID). Exposed so the server-side search path can reuse
// the same rule when POST /search proxies the query instead of handing the
// client a token.
func (ti *TokenIssuer) Filter(workspaceID, userID int64) string {
	return BuildFilter(
		fmt.Sprintf("workspace_id = %d", workspaceID),
		fmt.Sprintf("(channel_type = public OR channel_member_ids = %d)", userID),
	)
}
