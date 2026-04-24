-- name: CreateChannel :one
INSERT INTO channels (workspace_id, type, name, topic, description, default_join, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetChannelByID :one
SELECT * FROM channels
WHERE id = $1 AND archived_at IS NULL;

-- name: GetChannelByName :one
SELECT * FROM channels
WHERE workspace_id = $1 AND name = $2 AND archived_at IS NULL;

-- name: ListPublicChannels :many
-- Lists every non-archived public channel in the current workspace. Private
-- channels and DMs show up in M3 once channel_memberships exists.
SELECT * FROM channels
WHERE type = 'public' AND archived_at IS NULL
ORDER BY name;

-- name: ArchiveChannel :exec
UPDATE channels
SET archived_at = now()
WHERE id = $1;
