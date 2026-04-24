-- name: CreateMention :exec
INSERT INTO mentions (workspace_id, channel_id, message_id, message_created_at, mentioned_user_id, author_user_id)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListMyMentions :many
-- Most-recent mentions for the current user, across all workspaces they
-- belong to. The RLS policy on mentions limits rows to those where
-- mentioned_user_id = app.user_id, so no explicit WHERE clause needed.
SELECT * FROM mentions
WHERE mentioned_user_id = $1
ORDER BY created_at DESC
LIMIT $2;
