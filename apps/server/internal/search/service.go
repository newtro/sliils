package search

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/meilisearch/meilisearch-go"
)

// Service is the public surface of the search package used by HTTP handlers.
// It bundles the Meili client, the tenant-token issuer, and the Postgres
// pool needed to resolve operator arguments (channel names → ids) and to
// hydrate Meili hits against the DB with RLS enforced.
//
// Distinct from Indexer: the indexer cares only about pushing state INTO
// Meili; Service cares only about pulling results OUT for users. They share
// a Client but otherwise have separate lifecycles.
type Service struct {
	client *Client
	tokens *TokenIssuer

	// pool is the runtime pool (sliils_app role). Used for operator lookup
	// — converting `in:#design` to a channel id requires channel_memberships
	// so the user can't probe private channels they don't belong to.
	pool   *pgxpool.Pool
	logger *slog.Logger

	// partitionPruneBy is the max message age the hydration query will
	// consider. Keeps partition pruning effective for large backlogs.
	partitionPruneBy time.Duration
}

// NewService wires the service. tokenTTL is forwarded to TokenIssuer.Issue
// when callers request a fresh tenant token.
func NewService(client *Client, tokens *TokenIssuer, pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		client:           client,
		tokens:           tokens,
		pool:             pool,
		logger:           logger,
		partitionPruneBy: 365 * 24 * time.Hour,
	}
}

// Client returns the underlying Meili client. Exposed for /readyz health
// checks and for tests.
func (s *Service) Client() *Client { return s.client }

// Tokens returns the tenant-token issuer so a handler can mint a session
// token after a successful search. Nil if the install was started without
// tenant-token provisioning (rare — only tests).
func (s *Service) Tokens() *TokenIssuer { return s.tokens }

// ---- search path ---------------------------------------------------------

// SearchParams captures the handler's request. channelIDsUserCanSee is the
// pre-computed allowlist of channels the user is a member of within the
// workspace; public channels are added implicitly by the visibility filter.
type SearchParams struct {
	WorkspaceID int64
	UserID      int64
	RawQuery    string
	Limit       int
	Offset      int
}

// SearchResult is the normalized result shape returned to handlers, after
// Meili ranking and Postgres hydration (RLS double-check) are both done.
type SearchResult struct {
	Hits               []MessageHit `json:"hits"`
	EstimatedTotalHits int64        `json:"estimated_total_hits"`
	ProcessingTimeMS   int64        `json:"processing_time_ms"`
	Parsed             QuerySpec    `json:"parsed"`
}

// MessageHit is one entry in the result set. Carries enough for the web
// client to render the list without a second round trip.
type MessageHit struct {
	MessageID         int64     `json:"message_id"`
	ChannelID         int64     `json:"channel_id"`
	ChannelName       string    `json:"channel_name,omitempty"`
	ChannelType       string    `json:"channel_type"`
	WorkspaceID       int64     `json:"workspace_id"`
	AuthorUserID      int64     `json:"author_user_id,omitempty"`
	AuthorDisplayName string    `json:"author_display_name,omitempty"`
	Snippet           string    `json:"snippet"`           // Meili-highlighted body excerpt
	BodyMD            string    `json:"body_md"`           // raw body for the client to render
	CreatedAt         time.Time `json:"created_at"`
	ThreadRootID      int64     `json:"thread_root_id,omitempty"`
}

// Search runs the full search pipeline:
//  1. Parse the query into operators + free text.
//  2. Build a Meilisearch filter expression combining the tenant visibility
//     rule (workspace + membership) with any operator constraints.
//  3. Hit Meilisearch with highlighting enabled.
//  4. Re-hydrate the returned message ids against Postgres under the user's
//     RLS scope to drop any stale doc that slipped through.
//
// Hydration is where the security guarantee lives: Meili's filter is the
// fast path, but the DB's RLS is authoritative.
func (s *Service) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.Limit > 100 {
		params.Limit = 100
	}

	spec := ParseQuery(params.RawQuery)

	filter, err := s.buildFilter(ctx, params.WorkspaceID, params.UserID, spec)
	if err != nil {
		return nil, fmt.Errorf("build filter: %w", err)
	}

	req := &meilisearch.SearchRequest{
		Query:                 spec.Text,
		Filter:                filter,
		Limit:                 int64(params.Limit),
		Offset:                int64(params.Offset),
		AttributesToRetrieve:  []string{"message_id", "channel_id", "workspace_id"},
		AttributesToHighlight: []string{"body_md"},
		HighlightPreTag:       "<mark>",
		HighlightPostTag:      "</mark>",
		AttributesToCrop:      []string{"body_md"},
		CropLength:            30,
		Sort:                  []string{"created_at_unix:desc"},
	}

	meiliResp, err := s.client.svc.Index(s.client.messageIndex).SearchWithContext(ctx, spec.Text, req)
	if err != nil {
		return nil, fmt.Errorf("meili search: %w", err)
	}

	// Extract message ids + highlight snippets in Meili's ranked order.
	type hitCarry struct {
		id      int64
		snippet string
	}
	carries := make([]hitCarry, 0, len(meiliResp.Hits))
	for _, h := range meiliResp.Hits {
		var row struct {
			MessageID int64 `json:"message_id"`
		}
		if err := h.DecodeInto(&row); err != nil {
			continue
		}
		snippet := ""
		if fmt, ok := h["_formatted"]; ok {
			var f struct {
				BodyMD string `json:"body_md"`
			}
			_ = decodeRawInto(fmt, &f)
			snippet = f.BodyMD
		}
		carries = append(carries, hitCarry{id: row.MessageID, snippet: snippet})
	}

	if len(carries) == 0 {
		return &SearchResult{
			Hits:               []MessageHit{},
			EstimatedTotalHits: meiliResp.EstimatedTotalHits,
			ProcessingTimeMS:   meiliResp.ProcessingTimeMs,
			Parsed:             spec,
		}, nil
	}

	ids := make([]int64, 0, len(carries))
	for _, c := range carries {
		ids = append(ids, c.id)
	}

	rows, err := s.hydrate(ctx, params.WorkspaceID, params.UserID, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate: %w", err)
	}

	byID := make(map[int64]sqlcgen.GetMessagesByIDsForSearchRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	out := make([]MessageHit, 0, len(carries))
	for _, c := range carries {
		r, ok := byID[c.id]
		if !ok {
			continue // stale doc filtered by RLS; silently drop
		}
		hit := MessageHit{
			MessageID:   r.ID,
			ChannelID:   r.ChannelID,
			ChannelType: NormalizeChannelType(r.ChannelType),
			WorkspaceID: r.WorkspaceID,
			Snippet:     c.snippet,
			BodyMD:      r.BodyMd,
			CreatedAt:   r.CreatedAt.Time,
		}
		if r.ChannelName != nil {
			hit.ChannelName = *r.ChannelName
		}
		if r.AuthorUserID != nil {
			hit.AuthorUserID = *r.AuthorUserID
		}
		if r.AuthorDisplayName != nil {
			hit.AuthorDisplayName = *r.AuthorDisplayName
		}
		if r.ThreadRootID != nil {
			hit.ThreadRootID = *r.ThreadRootID
		}
		out = append(out, hit)
	}

	return &SearchResult{
		Hits:               out,
		EstimatedTotalHits: meiliResp.EstimatedTotalHits,
		ProcessingTimeMS:   meiliResp.ProcessingTimeMs,
		Parsed:             spec,
	}, nil
}

// IssueToken mints a tenant token scoped to (workspaceID, userID). Safe to
// call after every successful search so clients always hold a fresh token
// without a separate endpoint round-trip.
func (s *Service) IssueToken(workspaceID, userID int64, ttl time.Duration) (*TenantToken, error) {
	if s.tokens == nil {
		return nil, fmt.Errorf("tenant token issuer not initialized")
	}
	return s.tokens.Issue(workspaceID, userID, ttl)
}

// ---- internal helpers ----------------------------------------------------

// buildFilter turns (workspace, user, query) into a Meilisearch filter
// expression. The visibility rule is the same one baked into tenant tokens;
// operator filters ANDed on top narrow the result further.
func (s *Service) buildFilter(ctx context.Context, workspaceID, userID int64, spec QuerySpec) (string, error) {
	clauses := []string{
		fmt.Sprintf("workspace_id = %d", workspaceID),
		fmt.Sprintf("(channel_type = public OR channel_member_ids = %d)", userID),
	}

	if spec.HasLink {
		clauses = append(clauses, "has_link = true")
	}
	if spec.HasFile {
		clauses = append(clauses, "has_file = true")
	}

	if len(spec.InChannels) > 0 {
		channelIDs, err := s.resolveChannelNames(ctx, workspaceID, userID, spec.InChannels)
		if err != nil {
			return "", err
		}
		if len(channelIDs) == 0 {
			// User referenced a channel that doesn't exist or isn't visible.
			// An impossible clause returns zero hits rather than bypassing.
			clauses = append(clauses, "channel_id = -1")
		} else {
			parts := make([]string, 0, len(channelIDs))
			for _, id := range channelIDs {
				parts = append(parts, fmt.Sprintf("channel_id = %d", id))
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	if len(spec.From) > 0 {
		authorIDs, err := s.resolveUsernames(ctx, workspaceID, spec.From)
		if err != nil {
			return "", err
		}
		if len(authorIDs) == 0 {
			clauses = append(clauses, "author_user_id = -1")
		} else {
			parts := make([]string, 0, len(authorIDs))
			for _, id := range authorIDs {
				parts = append(parts, fmt.Sprintf("author_user_id = %d", id))
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	if len(spec.Mentions) > 0 {
		mentionIDs, err := s.resolveUsernames(ctx, workspaceID, spec.Mentions)
		if err != nil {
			return "", err
		}
		if len(mentionIDs) == 0 {
			clauses = append(clauses, "mention_user_ids = -1")
		} else {
			parts := make([]string, 0, len(mentionIDs))
			for _, id := range mentionIDs {
				parts = append(parts, fmt.Sprintf("mention_user_ids = %d", id))
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		}
	}

	return BuildFilter(clauses...), nil
}

// resolveChannelNames looks up channel ids for the given names, filtered to
// those the user can see. Runs inside a read-only tx with the user+workspace
// GUCs set so the channels RLS policy (SELECT-by-membership) applies:
// unknown or private-not-a-member channels drop silently.
func (s *Service) resolveChannelNames(ctx context.Context, workspaceID, userID int64, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	return s.queryIDsWithGUC(ctx, workspaceID, userID, `
		SELECT c.id
		FROM channels c
		WHERE c.workspace_id = $1
		  AND c.name = ANY($2::citext[])
		  AND c.archived_at IS NULL
		  AND (
		      c.type = 'public'
		      OR EXISTS (
		          SELECT 1 FROM channel_memberships m
		          WHERE m.channel_id = c.id AND m.user_id = $3
		      )
		  )
	`, workspaceID, names, userID)
}

// resolveUsernames maps display names / emails to user ids within the
// workspace. Users are NOT RLS-protected (cross-workspace table), but we
// still restrict via the workspace_memberships join so a stranger outside
// the workspace never leaks into operator results.
func (s *Service) resolveUsernames(ctx context.Context, workspaceID int64, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	return s.queryIDsWithGUC(ctx, workspaceID, 0, `
		SELECT DISTINCT u.id
		FROM users u
		JOIN workspace_memberships wm ON wm.user_id = u.id
		WHERE wm.workspace_id = $1
		  AND wm.deactivated_at IS NULL
		  AND (
		      u.email = ANY($2::citext[])
		      OR lower(u.display_name) = ANY($3::text[])
		  )
	`, workspaceID, names, lowercaseAll(names))
}

// queryIDsWithGUC runs a SELECT inside a read-only tx with app.user_id and
// app.workspace_id set so RLS policies match, returning the scanned bigints.
// Shared helper for operator resolution so we don't repeat the tx ceremony.
func (s *Service) queryIDsWithGUC(ctx context.Context, workspaceID, userID int64, query string, args ...any) ([]int64, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgxBeginReadOnly())
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if userID > 0 {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", fmt.Sprintf("%d", userID)); err != nil {
			return nil, fmt.Errorf("set user_id: %w", err)
		}
	}
	if workspaceID > 0 {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", fmt.Sprintf("%d", workspaceID)); err != nil {
			return nil, fmt.Errorf("set workspace_id: %w", err)
		}
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return ids, nil
}

// hydrate re-reads messages under the caller's workspace + user RLS scope.
// Any id that doesn't survive RLS (membership revoked since index-time, or
// soft-deleted since the claim tick) is dropped.
func (s *Service) hydrate(ctx context.Context, workspaceID, userID int64, ids []int64) ([]sqlcgen.GetMessagesByIDsForSearchRow, error) {
	lowerBound := pgtype.Timestamptz{Time: time.Now().Add(-s.partitionPruneBy), Valid: true}

	// Acquire a dedicated connection so we can SET LOCAL the workspace +
	// user GUCs inside a read-only tx. Matches the server's WithTx pattern
	// but without importing server/db/tx.go (avoid import cycle).
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgxBeginReadOnly())
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", fmt.Sprintf("%d", userID)); err != nil {
		return nil, fmt.Errorf("set user_id: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", fmt.Sprintf("%d", workspaceID)); err != nil {
		return nil, fmt.Errorf("set workspace_id: %w", err)
	}

	q := sqlcgen.New(tx)
	rows, err := q.GetMessagesByIDsForSearch(ctx, sqlcgen.GetMessagesByIDsForSearchParams{
		Ids:        ids,
		LowerBound: lowerBound,
	})
	if err != nil {
		return nil, fmt.Errorf("hydrate rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return rows, nil
}
