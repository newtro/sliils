-- Native Pages (M10).

-- ---- pages --------------------------------------------------------------

-- name: CreatePage :one
INSERT INTO pages (workspace_id, channel_id, title, doc_id, icon, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPageByID :one
SELECT * FROM pages WHERE id = $1;

-- name: GetPageByDocID :one
SELECT * FROM pages WHERE doc_id = $1;

-- name: ListPagesForWorkspace :many
SELECT p.*, u.display_name AS creator_display_name
FROM   pages p
LEFT JOIN users u ON u.id = p.created_by
WHERE  p.workspace_id = $1
  AND  p.archived_at IS NULL
ORDER BY p.updated_at DESC
LIMIT  $2 OFFSET $3;

-- name: ListPagesForChannel :many
SELECT p.*, u.display_name AS creator_display_name
FROM   pages p
LEFT JOIN users u ON u.id = p.created_by
WHERE  p.channel_id = $1
  AND  p.archived_at IS NULL
ORDER BY p.updated_at DESC;

-- name: UpdatePage :one
-- Patch-style update. channel_id is always written: pass the current
-- value (from GetPageByID) to leave it unchanged, or NULL to detach.
UPDATE pages
SET title      = COALESCE(sqlc.narg('title')::text,   title),
    channel_id = sqlc.narg('channel_id')::bigint,
    icon       = COALESCE(sqlc.narg('icon')::text,    icon),
    updated_at = now()
WHERE id = sqlc.arg('id')::bigint
RETURNING *;

-- name: TouchPage :exec
UPDATE pages
SET updated_at = now()
WHERE id = $1;

-- name: ArchivePage :exec
UPDATE pages SET archived_at = now() WHERE id = $1;

-- name: UnarchivePage :exec
UPDATE pages SET archived_at = NULL WHERE id = $1;

-- ---- snapshots ----------------------------------------------------------

-- name: CreatePageSnapshot :one
INSERT INTO page_snapshots (page_id, workspace_id, snapshot_data, byte_size, created_by, reason)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListPageSnapshots :many
SELECT ps.id, ps.page_id, ps.byte_size, ps.reason, ps.created_at,
       ps.created_by, u.display_name AS creator_display_name
FROM   page_snapshots ps
LEFT JOIN users u ON u.id = ps.created_by
WHERE  ps.page_id = $1
ORDER BY ps.created_at DESC
LIMIT  $2 OFFSET $3;

-- name: GetPageSnapshot :one
SELECT * FROM page_snapshots WHERE id = $1 AND page_id = $2;

-- name: PruneOldSnapshots :exec
-- Retention: keep at most $2 snapshots per page (newest first).
DELETE FROM page_snapshots
WHERE id IN (
    SELECT ps.id
    FROM   page_snapshots ps
    WHERE  ps.page_id = $1
    ORDER BY ps.created_at DESC
    OFFSET $2
);

-- ---- comments -----------------------------------------------------------

-- name: CreatePageComment :one
INSERT INTO page_comments (page_id, workspace_id, parent_id, author_id, anchor, body_md)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListPageComments :many
SELECT pc.*, u.display_name AS author_display_name
FROM   page_comments pc
LEFT JOIN users u ON u.id = pc.author_id
WHERE  pc.page_id = $1
  AND  pc.deleted_at IS NULL
ORDER BY pc.created_at ASC;

-- name: UpdatePageComment :one
UPDATE page_comments
SET body_md    = COALESCE(sqlc.narg('body_md')::text, body_md),
    resolved_at = CASE
        WHEN sqlc.narg('resolved')::bool IS TRUE  THEN now()
        WHEN sqlc.narg('resolved')::bool IS FALSE THEN NULL
        ELSE resolved_at
    END,
    updated_at = now()
WHERE id = sqlc.arg('id')::bigint
RETURNING *;

-- name: DeletePageComment :exec
UPDATE page_comments SET deleted_at = now() WHERE id = $1;
