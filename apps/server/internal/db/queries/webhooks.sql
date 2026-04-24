-- Webhooks + slash commands (M12-P2).

-- ---- webhooks_incoming --------------------------------------------------

-- name: CreateIncomingWebhook :one
INSERT INTO webhooks_incoming (workspace_id, channel_id, name, token, signing_secret_hash, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetIncomingWebhookByToken :one
-- NOTE: called unauthenticated from a public URL. Uses the owner pool in
-- the handler so RLS doesn't hide the row.
SELECT * FROM webhooks_incoming
WHERE token = $1 AND deleted_at IS NULL;

-- name: ListIncomingWebhooks :many
SELECT * FROM webhooks_incoming
WHERE workspace_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: TouchIncomingWebhook :exec
UPDATE webhooks_incoming SET last_used_at = now() WHERE id = $1;

-- name: DeleteIncomingWebhook :exec
UPDATE webhooks_incoming SET deleted_at = now() WHERE id = $1;

-- ---- webhooks_outgoing --------------------------------------------------

-- name: CreateOutgoingWebhook :one
INSERT INTO webhooks_outgoing
    (workspace_id, app_installation_id, event_pattern, target_url, signing_secret_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListOutgoingWebhooksForEvent :many
-- Matches any registered subscription where the pattern is exactly the
-- event OR a catch-all "*". Used by the fanout worker.
SELECT * FROM webhooks_outgoing
WHERE workspace_id = $1
  AND (event_pattern = $2 OR event_pattern = '*')
  AND deleted_at IS NULL;

-- name: DeleteOutgoingWebhook :exec
UPDATE webhooks_outgoing SET deleted_at = now() WHERE id = $1;

-- name: ListOutgoingWebhooksForInstallation :many
SELECT * FROM webhooks_outgoing
WHERE app_installation_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- ---- slash_commands -----------------------------------------------------

-- name: RegisterSlashCommand :one
INSERT INTO slash_commands (workspace_id, app_installation_id, command,
                            target_url, description, usage_hint, signing_secret_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSlashCommandForWorkspace :one
SELECT * FROM slash_commands
WHERE workspace_id = $1 AND command = $2 AND deleted_at IS NULL;

-- name: ListSlashCommandsForWorkspace :many
SELECT * FROM slash_commands
WHERE workspace_id = $1 AND deleted_at IS NULL
ORDER BY command;

-- name: DeleteSlashCommand :exec
UPDATE slash_commands SET deleted_at = now() WHERE id = $1;

-- ---- messages for bot API ----------------------------------------------

-- name: CreateBotMessage :one
INSERT INTO messages
    (workspace_id, channel_id, author_user_id, author_bot_installation_id,
     body_md, body_blocks, thread_root_id, parent_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
