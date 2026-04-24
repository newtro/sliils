-- name: InsertAuditLog :exec
INSERT INTO audit_log (workspace_id, actor_user_id, actor_ip, action, target_kind, target_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditLogForWorkspace :many
-- Admin dashboard feed. Joins the actor's display_name for a readable
-- "who did what" view. ORDER BY id DESC is stable even when two events
-- share the same created_at millisecond.
SELECT al.id, al.workspace_id, al.actor_user_id, al.actor_ip,
       al.action, al.target_kind, al.target_id, al.metadata, al.created_at,
       u.display_name AS actor_display_name,
       u.email AS actor_email
FROM   audit_log al
LEFT JOIN users u ON u.id = al.actor_user_id
WHERE  al.workspace_id = $1
ORDER BY al.id DESC
LIMIT  $2 OFFSET $3;
