-- External-calendar connections + sync state (M9-P3).

-- name: UpsertExternalCalendar :one
-- Connect or reconnect. When reconnecting after a prior disconnect we
-- reset sync_token so the next pull starts from scratch (otherwise a
-- stale token tied to a deleted Google connection will 400).
INSERT INTO external_calendars (user_id, provider, external_account_email, oauth_refresh_token)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, provider) DO UPDATE
SET external_account_email = EXCLUDED.external_account_email,
    oauth_refresh_token    = EXCLUDED.oauth_refresh_token,
    sync_token             = NULL,
    disconnected_at        = NULL,
    connected_at           = now()
RETURNING *;

-- name: GetExternalCalendar :one
SELECT * FROM external_calendars
WHERE user_id = $1 AND provider = $2 AND disconnected_at IS NULL;

-- name: ListExternalCalendarsForUser :many
SELECT * FROM external_calendars
WHERE user_id = $1 AND disconnected_at IS NULL;

-- name: ListActiveExternalCalendars :many
-- Used by the pull worker. Returns every active connection across every
-- user — owner pool only.
SELECT * FROM external_calendars WHERE disconnected_at IS NULL;

-- name: DisconnectExternalCalendar :exec
UPDATE external_calendars
SET disconnected_at = now()
WHERE user_id = $1 AND provider = $2;

-- name: UpdateExternalCalendarSyncState :exec
UPDATE external_calendars
SET sync_token     = $3,
    last_synced_at = now(),
    sync_status    = COALESCE($4::jsonb, sync_status)
WHERE user_id = $1 AND provider = $2;
