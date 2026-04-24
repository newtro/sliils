-- name: CreateChannelMembership :one
INSERT INTO channel_memberships (workspace_id, channel_id, user_id, notify_pref)
VALUES ($1, $2, $3, COALESCE($4, 'all'))
ON CONFLICT (channel_id, user_id) DO UPDATE SET notify_pref = EXCLUDED.notify_pref
RETURNING *;

-- name: ListUserChannelMemberships :many
-- Every row for the current user in the current workspace. Drives unread
-- badges for the sidebar.
SELECT * FROM channel_memberships
WHERE user_id = $1 AND workspace_id = $2;

-- name: UpdateLastRead :exec
UPDATE channel_memberships
SET last_read_message_id = $3
WHERE channel_id = $1 AND user_id = $2
  AND (last_read_message_id IS NULL OR last_read_message_id < $3);

-- name: CountMessagesAfter :one
-- Per-channel unread count. Partitioned messages; the created_at lower
-- bound is the message floor we care about.
SELECT count(*)::bigint AS n
FROM messages
WHERE channel_id = $1
  AND deleted_at IS NULL
  AND id > COALESCE($2, 0)
  AND created_at >= $3;

-- name: CountMentionsAfter :one
SELECT count(*)::bigint AS n
FROM mentions
WHERE mentioned_user_id = $1
  AND channel_id = $2
  AND message_id > COALESCE($3, 0);

-- name: GetChannelMembership :one
SELECT * FROM channel_memberships
WHERE channel_id = $1 AND user_id = $2;

-- name: ListChannelMembers :many
-- Lean projection — callers that need display_name can join users.
-- Used by the M11 DM push-fanout to enumerate the other participants.
SELECT user_id
FROM   channel_memberships
WHERE  channel_id = $1;
