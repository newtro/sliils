-- name: CreateMembership :one
INSERT INTO workspace_memberships (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetMembershipForUser :one
SELECT * FROM workspace_memberships
WHERE workspace_id = $1 AND user_id = $2 AND deactivated_at IS NULL;

-- name: ListWorkspaceMembers :many
SELECT m.*, u.email, u.display_name, u.email_verified_at
FROM workspace_memberships m
JOIN users u ON u.id = m.user_id
WHERE m.workspace_id = $1 AND m.deactivated_at IS NULL
ORDER BY m.joined_at;

-- name: DeactivateMembership :exec
UPDATE workspace_memberships
SET deactivated_at = now()
WHERE workspace_id = $1 AND user_id = $2;

-- name: UpdateMembershipRole :exec
UPDATE workspace_memberships
SET role = $3
WHERE workspace_id = $1 AND user_id = $2;

-- name: UpdateMembershipCustomStatus :one
-- Caller sets custom_status to arbitrary JSONB. Pass '{}'::jsonb to clear.
-- RLS on workspace_memberships allows this because the modify policy
-- lets a user update their own rows (user_id match), so app.workspace_id
-- does not need to be set on this path.
UPDATE workspace_memberships
SET custom_status = $3::jsonb
WHERE workspace_id = $1 AND user_id = $2
RETURNING *;

-- name: UpdateMembershipNotifyPref :one
-- Workspace-level notification default. Same RLS story as custom_status:
-- the user is updating their own row, which the wsm_modify policy permits.
UPDATE workspace_memberships
SET notify_pref = $3
WHERE workspace_id = $1 AND user_id = $2
RETURNING *;
