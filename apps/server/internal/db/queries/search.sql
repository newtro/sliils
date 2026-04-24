-- Search outbox + hydration queries (M6).
--
-- Writes to the outbox happen inside message-mutation transactions (with
-- app.workspace_id set, so the tenant RLS policy applies). Reads / updates
-- from the drain worker use a privileged owner-pool that bypasses RLS —
-- see internal/db.OpenOwner.

-- name: EnqueueSearchOutbox :exec
-- Enqueues one search-index event. Must run inside the same tx as the
-- originating write so indexing state can never diverge from DB state.
INSERT INTO search_outbox (workspace_id, kind, action, target_id, payload)
VALUES ($1, $2, $3, $4, $5);

-- name: ClaimSearchOutboxBatch :many
-- Locks up to `limit` pending rows for this worker to process. FOR UPDATE
-- SKIP LOCKED lets multiple workers share the queue without serializing
-- on a single row. Rows with too many failures are skipped so one poison
-- message can't block the backlog; poison rows stay visible via the
-- idx_search_outbox_errors index for manual inspection.
WITH claimed AS (
    SELECT id
    FROM search_outbox
    WHERE processed_at IS NULL
      AND attempts < sqlc.arg('max_attempts')::int
    ORDER BY enqueued_at
    LIMIT sqlc.arg('batch_limit')::int
    FOR UPDATE SKIP LOCKED
)
UPDATE search_outbox o
SET attempts = o.attempts + 1
FROM claimed
WHERE o.id = claimed.id
RETURNING o.id, o.workspace_id, o.kind, o.action, o.target_id, o.payload,
          o.enqueued_at, o.attempts;

-- name: MarkSearchOutboxProcessed :exec
-- Idempotent — reapplying is safe because processed_at stays set.
UPDATE search_outbox
SET processed_at = now(), last_error = NULL
WHERE id = ANY(sqlc.arg('ids')::bigint[]);

-- name: MarkSearchOutboxFailed :exec
-- Keeps the row pending (processed_at null) so another attempt will pick it
-- up after the claim's lock expires. last_error carries the message for
-- operators; attempts is already incremented by the claim itself.
UPDATE search_outbox
SET last_error = $2
WHERE id = $1;

-- name: PruneProcessedSearchOutbox :exec
-- Housekeeping: drop processed rows older than the retention window. Run
-- periodically by the worker.
DELETE FROM search_outbox
WHERE processed_at IS NOT NULL
  AND processed_at < now() - (sqlc.arg('retain')::text)::interval;

-- name: CountPendingSearchOutbox :one
SELECT count(*)::bigint AS n FROM search_outbox WHERE processed_at IS NULL;

-- ---- hydration queries ----------------------------------------------------

-- name: GetMessageForIndexing :one
-- Fetch the current state of a message plus the channel it lives in for
-- building a Meilisearch document. Runs on the owner pool — RLS bypassed.
-- The partition prune lower bound is expressed in months: the worker passes
-- a timestamp computed from the outbox enqueued_at minus a small slack
-- window so partition pruning still helps even if the indexer runs late.
SELECT m.id, m.workspace_id, m.channel_id, m.author_user_id, m.body_md,
       m.thread_root_id, m.parent_id, m.edited_at, m.deleted_at, m.created_at,
       c.type AS channel_type, c.name AS channel_name
FROM messages m
JOIN channels c ON c.id = m.channel_id
WHERE m.id = $1 AND m.created_at >= $2;

-- name: ListChannelMemberIDs :many
-- Returns every user id with an active channel membership. Used to bake
-- `channel_member_ids` into Meilisearch documents for private channels / DMs.
SELECT user_id FROM channel_memberships
WHERE channel_id = $1;

-- name: MessageHasAttachments :one
SELECT EXISTS (
    SELECT 1 FROM message_attachments WHERE message_id = $1
)::bool AS has_files;

-- ---- search-result hydration (runs under user's workspace GUC) ------------

-- name: GetMessagesByIDsForSearch :many
-- Called by POST /search after Meilisearch returns a ranked list of message
-- ids. Uses the user's workspace + user GUCs so RLS double-checks visibility
-- — any stale doc whose membership was revoked between index-time and query-
-- time is silently dropped. Soft-deleted messages are also filtered out.
SELECT m.id, m.workspace_id, m.channel_id, m.author_user_id, m.body_md,
       m.thread_root_id, m.parent_id, m.edited_at, m.created_at,
       c.name AS channel_name, c.type AS channel_type,
       u.display_name AS author_display_name
FROM messages m
JOIN channels c ON c.id = m.channel_id
LEFT JOIN users u ON u.id = m.author_user_id
WHERE m.id = ANY(sqlc.arg('ids')::bigint[])
  AND m.created_at >= sqlc.arg('lower_bound')::timestamptz
  AND m.deleted_at IS NULL;

-- name: ListUserChannelMembershipsForWorkspace :many
-- Every channel id the user belongs to inside a single workspace. Used to
-- validate that a user-supplied in:#channel operator resolves to a channel
-- they can see before we send it to Meilisearch as a filter.
SELECT channel_id FROM channel_memberships
WHERE user_id = $1 AND workspace_id = $2;

-- ---- bootstrap / full reindex --------------------------------------------

-- name: ListMessageIDsForBackfill :many
-- Streaming-style pagination for the initial / full reindex path. Returns
-- message ids newer than the cursor, capped at `limit`, so the caller can
-- iterate until empty. Excluded: soft-deleted messages (they'd be deleted
-- from the index anyway).
SELECT id, workspace_id, created_at
FROM messages
WHERE deleted_at IS NULL
  AND (created_at, id) > (sqlc.arg('cursor_created_at')::timestamptz, sqlc.arg('cursor_id')::bigint)
  AND created_at >= sqlc.arg('lower_bound')::timestamptz
ORDER BY created_at, id
LIMIT sqlc.arg('batch_limit')::int;
