-- Push notification devices (M11).

-- name: RegisterDevice :one
-- Idempotent by (user_id, endpoint): re-registering the same endpoint
-- updates its keys + label and clears disabled state. That matches the
-- browser's "I already have a subscription — here it is again" behavior
-- where the endpoint stays stable across page reloads.
INSERT INTO user_devices (user_id, platform, endpoint, p256dh, auth_secret, user_agent, label)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, endpoint) DO UPDATE
SET platform        = EXCLUDED.platform,
    p256dh          = EXCLUDED.p256dh,
    auth_secret     = EXCLUDED.auth_secret,
    user_agent      = EXCLUDED.user_agent,
    label           = EXCLUDED.label,
    disabled_at     = NULL,
    disabled_reason = '',
    last_seen_at    = now()
RETURNING *;

-- name: ListMyDevices :many
SELECT * FROM user_devices
WHERE  user_id = $1
  AND  disabled_at IS NULL
ORDER BY last_seen_at DESC;

-- name: DeleteMyDevice :exec
DELETE FROM user_devices
WHERE id = $1 AND user_id = $2;

-- name: DisableDevice :exec
-- Called by the push worker when the endpoint returns 404/410.
UPDATE user_devices
SET disabled_at     = now(),
    disabled_reason = $2
WHERE id = $1;

-- name: TouchDevice :exec
UPDATE user_devices
SET last_seen_at = now()
WHERE id = $1;

-- name: ListDevicesForPush :many
-- Hydrates the fan-out worker: every active device for a user.
SELECT * FROM user_devices
WHERE  user_id = $1
  AND  disabled_at IS NULL;

-- name: UpdateUserQuietHours :exec
UPDATE users
SET dnd_enabled_until = $2,
    quiet_hours_start = $3,
    quiet_hours_end   = $4,
    quiet_hours_tz    = $5,
    updated_at        = now()
WHERE id = $1;

-- name: GetUserDNDState :one
SELECT dnd_enabled_until, quiet_hours_start, quiet_hours_end, quiet_hours_tz
FROM users WHERE id = $1;
