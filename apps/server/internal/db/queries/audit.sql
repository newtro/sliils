-- name: InsertAuditLog :exec
INSERT INTO audit_log (workspace_id, actor_user_id, actor_ip, action, target_kind, target_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);
