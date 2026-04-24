-- name: CreateFile :one
INSERT INTO files (workspace_id, uploader_user_id, storage_backend, storage_key,
                   filename, mime, size_bytes, sha256, scan_status, width, height)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetFileBySHA256 :one
-- De-dupe lookup: if the same file content is uploaded twice to the same
-- workspace, point at the first record instead of creating a duplicate.
SELECT * FROM files
WHERE workspace_id = $1
  AND sha256       = $2
  AND deleted_at IS NULL;

-- name: GetFileByID :one
SELECT * FROM files
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateFileScanStatus :exec
UPDATE files
SET scan_status = $2
WHERE id = $1;

-- name: UpdateFileDerivatives :exec
UPDATE files
SET derivatives = $2
WHERE id = $1;

-- name: SoftDeleteFile :exec
UPDATE files
SET deleted_at = now()
WHERE id = $1;

-- name: UpdateFileBytes :exec
-- Used by the WOPI PutFile handler when Collabora saves a new version of
-- a document. sha256 + size_bytes change; the storage_key stays constant
-- so any existing attachments keep pointing at the same file id.
-- scan_status resets to 'pending' so the AV worker (M5.1) re-scans once
-- it's wired.
UPDATE files
SET sha256       = $2,
    size_bytes   = $3,
    mime         = $4,
    scan_status  = 'pending'
WHERE id = $1;

-- name: CreateAttachment :one
INSERT INTO message_attachments (workspace_id, channel_id, message_id, file_id, position)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (message_id, file_id) DO NOTHING
RETURNING *;

-- name: ListAttachmentsForMessages :many
-- Hydrates attachments for a batch of messages in a single query. Joined
-- against files so the client gets everything it needs to render a preview.
SELECT a.message_id, a.position,
       f.id AS file_id, f.filename, f.mime, f.size_bytes,
       f.width, f.height, f.scan_status, f.derivatives, f.created_at
FROM message_attachments a
JOIN files f ON f.id = a.file_id AND f.deleted_at IS NULL
WHERE a.message_id = ANY($1::bigint[])
ORDER BY a.message_id, a.position, a.id;
