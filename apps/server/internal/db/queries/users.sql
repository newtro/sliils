-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND deactivated_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND deactivated_at IS NULL;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash      = $2,
    failed_login_count = 0,
    locked_until       = NULL
WHERE id = $1;

-- name: IncrementFailedLogins :one
UPDATE users
SET failed_login_count = failed_login_count + 1,
    locked_until = CASE
                      WHEN failed_login_count + 1 >= $2 THEN now() + ($3 || ' minutes')::interval
                      ELSE locked_until
                   END
WHERE id = $1
RETURNING failed_login_count, locked_until;

-- name: ResetFailedLogins :exec
UPDATE users
SET failed_login_count = 0,
    locked_until       = NULL
WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, now())
WHERE id = $1;

-- name: UpdateUserDisplayName :exec
UPDATE users
SET display_name = $2
WHERE id = $1;

-- name: CountActiveUsers :one
-- Used by the first-run wizard to decide whether bootstrap is allowed.
-- Zero active users = fresh install; any existing user = already set up.
SELECT count(*) FROM users WHERE deactivated_at IS NULL;

-- name: PromoteToSuperAdmin :exec
UPDATE users SET is_super_admin = true WHERE id = $1;

-- name: DemoteFromSuperAdmin :exec
UPDATE users SET is_super_admin = false WHERE id = $1;
