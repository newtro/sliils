-- Apps platform (M12).

-- ---- apps (install-global) ---------------------------------------------

-- name: CreateApp :one
INSERT INTO apps (slug, name, description, owner_user_id, avatar_file_id,
                  manifest, client_id, client_secret_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAppBySlug :one
SELECT * FROM apps WHERE slug = $1 AND deleted_at IS NULL;

-- name: GetAppByClientID :one
SELECT * FROM apps WHERE client_id = $1 AND deleted_at IS NULL;

-- name: GetAppByID :one
SELECT * FROM apps WHERE id = $1 AND deleted_at IS NULL;

-- name: ListAppsForOwner :many
SELECT * FROM apps
WHERE  owner_user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: UpdateAppManifest :one
UPDATE apps
SET manifest    = sqlc.arg('manifest')::jsonb,
    name        = COALESCE(sqlc.narg('name')::text, name),
    description = COALESCE(sqlc.narg('description')::text, description)
WHERE id = sqlc.arg('id')::bigint
RETURNING *;

-- name: RotateAppSecret :exec
UPDATE apps
SET client_secret_hash = $2
WHERE id = $1;

-- name: SoftDeleteApp :exec
UPDATE apps SET deleted_at = now() WHERE id = $1;

-- ---- installations -----------------------------------------------------

-- name: CreateAppInstallation :one
INSERT INTO app_installations (app_id, workspace_id, installed_by, scopes, bot_user_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInstallation :one
SELECT * FROM app_installations
WHERE app_id = $1 AND workspace_id = $2 AND revoked_at IS NULL;

-- name: GetInstallationByID :one
SELECT * FROM app_installations WHERE id = $1;

-- name: ListInstallationsForWorkspace :many
SELECT ai.*, a.slug, a.name AS app_name, a.description AS app_description
FROM   app_installations ai
JOIN   apps a ON a.id = ai.app_id
WHERE  ai.workspace_id = $1 AND ai.revoked_at IS NULL
ORDER BY ai.created_at DESC;

-- name: RevokeInstallation :exec
UPDATE app_installations
SET revoked_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: SetInstallationBot :exec
UPDATE app_installations SET bot_user_id = $2 WHERE id = $1;

-- ---- bot users ---------------------------------------------------------

-- name: CreateBotUser :one
-- Bot users live in the users table with is_bot=true. Email is a
-- synthetic fake@bot.local sentinel so the UNIQUE constraint stays happy.
INSERT INTO users (email, password_hash, display_name, is_bot, bot_app_installation_id, email_verified_at)
VALUES ($1, NULL, $2, true, $3, now())
RETURNING *;

-- name: GetBotUserByInstallation :one
SELECT * FROM users
WHERE bot_app_installation_id = $1 AND is_bot = true AND deactivated_at IS NULL;

-- ---- oauth codes -------------------------------------------------------

-- name: CreateOAuthCode :exec
INSERT INTO app_oauth_codes
    (code, app_id, workspace_id, user_id, redirect_uri, scopes,
     code_challenge, code_challenge_method, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ConsumeOAuthCode :one
-- Single-use exchange: mark used atomically and return the row in one go.
-- Returning 0 rows on a used/expired code lets the handler 400 cleanly.
UPDATE app_oauth_codes
SET used_at = now()
WHERE code = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: PruneExpiredOAuthCodes :exec
DELETE FROM app_oauth_codes
WHERE expires_at < now() - interval '1 hour';

-- ---- app tokens --------------------------------------------------------

-- name: CreateAppToken :one
INSERT INTO app_tokens (app_installation_id, workspace_id, token_hash, label, scopes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAppTokenByID :one
-- Called during inbound bot API requests. Caller verifies the provided
-- secret matches the stored hash before trusting the row.
SELECT * FROM app_tokens
WHERE token_id = $1 AND revoked_at IS NULL;

-- name: ListTokensForInstallation :many
SELECT token_id, app_installation_id, workspace_id, label, scopes,
       created_at, last_used_at, revoked_at
FROM   app_tokens
WHERE  app_installation_id = $1
ORDER BY created_at DESC;

-- name: TouchAppToken :exec
UPDATE app_tokens SET last_used_at = now() WHERE token_id = $1;

-- name: RevokeAppToken :exec
UPDATE app_tokens SET revoked_at = now() WHERE token_id = $1;
