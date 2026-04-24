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

-- name: ArchiveWorkspace :exec
UPDATE workspaces
SET archived_at = now()
WHERE id = $1;
