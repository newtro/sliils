-- Per-workspace email provider config.

-- name: GetWorkspaceEmailSettings :one
SELECT * FROM workspace_email_settings
WHERE workspace_id = $1;

-- name: UpsertWorkspaceEmailSettings :one
INSERT INTO workspace_email_settings
    (workspace_id, provider, resend_api_key_enc, from_address, from_name, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id) DO UPDATE
SET provider           = EXCLUDED.provider,
    resend_api_key_enc = CASE
        -- Empty replacement means "keep existing key" — lets the UI omit
        -- the key when the admin only wants to change the from-address.
        WHEN EXCLUDED.resend_api_key_enc = '' THEN workspace_email_settings.resend_api_key_enc
        ELSE EXCLUDED.resend_api_key_enc
    END,
    from_address       = EXCLUDED.from_address,
    from_name          = EXCLUDED.from_name,
    updated_at         = now(),
    updated_by         = EXCLUDED.updated_by
RETURNING *;

-- name: DeleteWorkspaceEmailSettings :exec
DELETE FROM workspace_email_settings WHERE workspace_id = $1;
