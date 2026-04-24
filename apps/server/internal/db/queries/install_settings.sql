-- Install-wide settings (single-tenant key/value).

-- name: GetInstallSetting :one
SELECT * FROM install_settings WHERE key = $1;

-- name: ListInstallSettings :many
SELECT * FROM install_settings ORDER BY key;

-- name: UpsertInstallSetting :exec
INSERT INTO install_settings (key, value, encrypted, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE
SET value      = EXCLUDED.value,
    encrypted  = EXCLUDED.encrypted,
    updated_at = now(),
    updated_by = EXCLUDED.updated_by;

-- name: SeedInstallSettingIfAbsent :exec
-- First-boot seeding: write the value only if the key isn't already
-- present. Lets us pull env defaults into the DB once without clobbering
-- values an admin has already edited through the UI.
INSERT INTO install_settings (key, value, encrypted)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO NOTHING;
