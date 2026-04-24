-- name: CreateWorkspace :one
INSERT INTO workspaces (slug, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces
WHERE id = $1 AND archived_at IS NULL;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspaces
WHERE slug = $1 AND archived_at IS NULL;

-- name: ListWorkspacesForUser :many
-- Returns every workspace the current user is an active member of, with
-- their role and joined_at included. Sorted by the name of the workspace
-- for stable switcher ordering. M7 adds custom_status + notify_pref so
-- the switcher can render status badges + per-workspace mute indicators
-- without a second round trip.
SELECT w.*,
       m.role          AS membership_role,
       m.joined_at     AS membership_joined_at,
       m.custom_status AS membership_custom_status,
       m.notify_pref   AS membership_notify_pref
FROM workspaces w
JOIN workspace_memberships m ON m.workspace_id = w.id
WHERE m.user_id = $1
  AND m.deactivated_at IS NULL
  AND w.archived_at  IS NULL
ORDER BY w.name;

-- name: CountWorkspacesForUser :one
SELECT count(*) AS n
FROM workspace_memberships m
WHERE m.user_id = $1 AND m.deactivated_at IS NULL;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name        = COALESCE($2, name),
    description = COALESCE($3, description),
    brand_color = COALESCE($4, brand_color)
WHERE id = $1
RETURNING *;

-- name: UpdateWorkspaceAdmin :one
-- Admin dashboard update — separately named so callers distinguish
-- brand-change PATCHes from retention-policy changes (different audit
-- events fire). retention_days is always written so the caller can
-- null it out ("keep forever") explicitly.
UPDATE workspaces
SET name           = COALESCE(sqlc.narg('name')::text,        name),
    description    = COALESCE(sqlc.narg('description')::text, description),
    brand_color    = COALESCE(sqlc.narg('brand_color')::text, brand_color),
    logo_file_id   = COALESCE(sqlc.narg('logo_file_id')::bigint, logo_file_id),
    retention_days = sqlc.narg('retention_days')::int
WHERE id = sqlc.arg('id')::bigint
RETURNING *;

-- name: ListWorkspacesWithRetention :many
-- Fed into the retention-sweep worker. Returns every workspace whose
-- admins have opted into automatic message purging.
SELECT id, retention_days
FROM   workspaces
WHERE  retention_days IS NOT NULL AND retention_days > 0
  AND  archived_at IS NULL;

-- name: PurgeOldMessages :exec
-- Soft-delete messages older than the retention window. Messages are
-- partitioned; the date bound is essential for partition pruning.
UPDATE messages
SET    deleted_at = now(),
       body_md    = '',
       body_blocks = '[]'::jsonb
WHERE  workspace_id = $1
  AND  created_at >= $2
  AND  created_at <  $3
  AND  deleted_at IS NULL;

-- name: ArchiveWorkspace :exec
UPDATE workspaces
SET archived_at = now()
WHERE id = $1;
