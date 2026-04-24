-- Calendar events (M9).

-- ---- events ------------------------------------------------------------

-- name: CreateEvent :one
INSERT INTO events (
    workspace_id, channel_id, title, description, location_url,
    start_at, end_at, time_zone, rrule,
    recording_enabled, video_enabled, created_by,
    external_provider, external_event_id, external_etag
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetEventByID :one
SELECT * FROM events WHERE id = $1;

-- name: UpdateEvent :one
-- Patch-style update: every field is COALESCE'd so the caller can null-out
-- channel_id / rrule / description / location_url etc. by passing an
-- explicit value.
UPDATE events
SET title             = COALESCE(sqlc.narg('title')::text,        title),
    description       = COALESCE(sqlc.narg('description')::text,  description),
    location_url      = COALESCE(sqlc.narg('location_url')::text, location_url),
    start_at          = COALESCE(sqlc.narg('start_at')::timestamptz, start_at),
    end_at            = COALESCE(sqlc.narg('end_at')::timestamptz,   end_at),
    time_zone         = COALESCE(sqlc.narg('time_zone')::text,    time_zone),
    rrule             = sqlc.narg('rrule')::text,
    recording_enabled = COALESCE(sqlc.narg('recording_enabled')::bool, recording_enabled),
    video_enabled     = COALESCE(sqlc.narg('video_enabled')::bool,    video_enabled)
WHERE id = sqlc.arg('id')::bigint
RETURNING *;

-- name: CancelEvent :exec
UPDATE events
SET canceled_at = now()
WHERE id = $1;

-- name: ListEventsInRange :many
-- Returns every non-canceled event whose "span" could intersect the
-- (from, to) window. RRULE expansion happens in Go — this just narrows
-- to candidate series by start_at.
--
-- Why the generous upper bound on start_at? For a daily recurring event
-- whose root is in January, the caller might be asking for a May window.
-- Including events whose start_at is <= $3 (the window end) keeps the
-- candidate set tight enough while letting the RRULE expander produce
-- valid May instances.
SELECT e.*, u.display_name AS creator_display_name
FROM events e
LEFT JOIN users u ON u.id = e.created_by
WHERE e.workspace_id = $1
  AND e.canceled_at IS NULL
  AND e.start_at <= $3
  AND (
      -- single instance: end_at inside window
      (e.rrule IS NULL AND e.end_at >= $2)
      -- recurring: included unconditionally; Go-side expander handles
      -- the real range check once RRULE is evaluated.
      OR (e.rrule IS NOT NULL)
  )
ORDER BY e.start_at;

-- name: ListEventsForChannel :many
SELECT * FROM events
WHERE channel_id = $1
  AND canceled_at IS NULL
ORDER BY start_at;

-- name: GetEventByExternalID :one
-- Dedupe key during pull-from-Google sync. NULL provider/event_id never
-- matches (all the fields are enforced non-null by the UNIQUE index).
SELECT * FROM events
WHERE external_provider = $1 AND external_event_id = $2;

-- name: SetEventExternalRef :exec
-- Stamp the external id + etag after a successful push or on import.
UPDATE events
SET external_provider = $2,
    external_event_id = $3,
    external_etag     = $4
WHERE id = $1;

-- ---- event_attendees ---------------------------------------------------

-- name: UpsertInternalAttendee :one
-- Idempotent attendee add for an internal user. If they're already on
-- the invite list, we keep their existing RSVP. If this is an import
-- from an external calendar, the caller passes rsvp='pending' and the
-- next sync tick will reconcile.
INSERT INTO event_attendees (event_id, user_id, rsvp)
VALUES ($1, $2, COALESCE($3, 'pending'))
ON CONFLICT (event_id, user_id) WHERE user_id IS NOT NULL
DO UPDATE SET user_id = event_attendees.user_id  -- no-op
RETURNING *;

-- name: UpsertExternalAttendee :one
-- Email-only attendees (external guests). Same pattern.
INSERT INTO event_attendees (event_id, external_email, rsvp)
VALUES ($1, $2, COALESCE($3, 'pending'))
ON CONFLICT (event_id, external_email) WHERE external_email IS NOT NULL
DO UPDATE SET external_email = event_attendees.external_email
RETURNING *;

-- name: UpdateAttendeeRSVP :one
-- Internal users update their own row. The handler enforces ownership;
-- the query itself is keyed on (event, user) so crafting a request for
-- someone else's row requires writing past the handler's checks.
UPDATE event_attendees
SET rsvp = $3, responded_at = now()
WHERE event_id = $1 AND user_id = $2
RETURNING *;

-- name: ListAttendeesForEvent :many
SELECT a.*, u.display_name, u.email AS user_email
FROM event_attendees a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.event_id = $1
ORDER BY a.invited_at;

-- name: RemoveAttendee :exec
DELETE FROM event_attendees
WHERE event_id = $1 AND (
    (user_id IS NOT NULL AND user_id = $2)
    OR (external_email IS NOT NULL AND external_email = $3)
);

-- name: ListUpcomingEventsForReminders :many
-- Drives the River reminder job. Returns events starting in the
-- (now + lead_min, now + lead_max) window for users who RSVPd yes.
-- The job emits one `event.upcoming` realtime event per (event, user)
-- and stamps a reminder_sent sentinel elsewhere (not yet — future).
SELECT e.id AS event_id, e.workspace_id, e.channel_id, e.title, e.start_at,
       e.video_enabled, a.user_id
FROM events e
JOIN event_attendees a ON a.event_id = e.id
WHERE e.canceled_at IS NULL
  AND e.start_at BETWEEN (now() + sqlc.arg('lead_min')::interval)
                     AND (now() + sqlc.arg('lead_max')::interval)
  AND a.user_id IS NOT NULL
  AND a.rsvp = 'yes';
