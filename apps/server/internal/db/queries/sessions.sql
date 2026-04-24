-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSessionByRefreshHash :one
SELECT * FROM sessions
WHERE refresh_token_hash = $1
  AND revoked_at IS NULL
  AND expires_at > now();

-- name: RotateSessionRefresh :exec
UPDATE sessions
SET refresh_token_hash = $2,
    last_seen_at       = now(),
    expires_at         = $3
WHERE id = $1;

-- name: RevokeSession :exec
UPDATE sessions
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE sessions
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions
SET last_seen_at = now()
WHERE id = $1;
