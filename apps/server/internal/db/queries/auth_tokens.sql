-- name: CreateAuthToken :one
INSERT INTO auth_tokens (user_id, purpose, token_hash, expires_at, ip)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAuthTokenByHash :one
SELECT * FROM auth_tokens
WHERE token_hash = $1
  AND purpose    = $2
  AND consumed_at IS NULL
  AND expires_at > now();

-- name: ConsumeAuthToken :exec
UPDATE auth_tokens
SET consumed_at = now()
WHERE id = $1 AND consumed_at IS NULL;

-- name: InvalidateActiveAuthTokens :exec
UPDATE auth_tokens
SET consumed_at = now()
WHERE user_id = $1
  AND purpose = $2
  AND consumed_at IS NULL;

-- name: CountRecentAuthTokens :one
SELECT count(*) AS n
FROM auth_tokens
WHERE user_id = $1
  AND purpose = $2
  AND created_at > now() - ($3 || ' minutes')::interval;
