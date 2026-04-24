-- name: CreateMessage :one
INSERT INTO messages (workspace_id, channel_id, author_user_id, body_md, body_blocks, thread_root_id, parent_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetMessageByID :one
-- Bounded by (id, created_at) window so the query is partition-prunable.
-- Callers who only have the id should use a short recency window here.
SELECT * FROM messages
WHERE id = $1
  AND created_at >= $2
  AND deleted_at IS NULL;

-- name: ListChannelMessages :many
-- Reverse-chronological page through top-level channel messages (thread
-- replies are fetched separately via ListThreadReplies). Cursor is
-- (cursor_created_at, cursor_id) so the result is stable under concurrent
-- inserts at the head of the channel.
SELECT m.*,
       COALESCE(
         (SELECT count(*)::bigint
          FROM messages r
          WHERE r.thread_root_id = m.id
            AND r.deleted_at IS NULL),
         0::bigint
       )::bigint AS reply_count
FROM messages m
WHERE m.channel_id = $1
  AND m.deleted_at IS NULL
  AND m.thread_root_id IS NULL
  AND (
        $2::timestamptz IS NULL
     OR (m.created_at, m.id) < ($2::timestamptz, $3::bigint)
  )
ORDER BY m.created_at DESC, m.id DESC
LIMIT $4;

-- name: ListThreadReplies :many
-- Every reply under a thread root, oldest first. Root is NOT included.
SELECT * FROM messages
WHERE thread_root_id = $1
  AND deleted_at IS NULL
  AND created_at >= $2
ORDER BY created_at ASC, id ASC;

-- name: CountThreadReplies :one
SELECT count(*)::bigint AS n
FROM messages
WHERE thread_root_id = $1 AND deleted_at IS NULL;

-- name: UpdateMessageBody :one
UPDATE messages
SET body_md    = $3,
    body_blocks = $4,
    edited_at   = now()
WHERE id = $1
  AND created_at >= $2
RETURNING *;

-- name: SoftDeleteMessage :one
UPDATE messages
SET deleted_at = now()
WHERE id = $1
  AND created_at >= $2
  AND deleted_at IS NULL
RETURNING *;

-- name: AddReaction :exec
INSERT INTO message_reactions (message_id, user_id, emoji, workspace_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: RemoveReaction :exec
DELETE FROM message_reactions
WHERE message_id = $1 AND user_id = $2 AND emoji = $3;

-- name: ListReactionsForMessages :many
-- Aggregates reactions for a batch of messages so the list endpoint can
-- hydrate the page in a single round-trip. The explicit cast on array_agg
-- lets sqlc infer []int64 instead of interface{}.
SELECT message_id, emoji,
       array_agg(user_id ORDER BY user_id)::bigint[] AS user_ids
FROM message_reactions
WHERE message_id = ANY($1::bigint[])
GROUP BY message_id, emoji;

-- name: LookupWorkspaceMembersByIDs :many
-- Used to validate that a list of <@N> mention targets are actually in
-- the current workspace before persisting mention rows.
SELECT m.user_id, u.display_name, u.email
FROM workspace_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1
  AND m.user_id = ANY($2::bigint[])
  AND m.deactivated_at IS NULL;
