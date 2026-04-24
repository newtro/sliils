-- Meeting + DM queries (M8).

-- ---- meetings -----------------------------------------------------------

-- name: CreateMeeting :one
-- A new call instance on a channel. RLS enforces workspace scope.
INSERT INTO meetings (workspace_id, channel_id, livekit_room, started_by, metadata)
VALUES ($1, $2, $3, $4, COALESCE($5::jsonb, '{}'::jsonb))
RETURNING *;

-- name: SetMeetingLiveKitRoom :exec
-- Stamp the canonical LiveKit room name after we know the meeting id.
-- Room names are derived from the id, so we assign sequentially.
UPDATE meetings SET livekit_room = $2 WHERE id = $1;

-- name: GetMeetingByID :one
SELECT * FROM meetings WHERE id = $1;

-- name: GetActiveMeetingForChannel :one
-- "Is there a call in progress on this channel?" Returns at most one row;
-- the partial index backs the lookup.
SELECT * FROM meetings
WHERE channel_id = $1 AND ended_at IS NULL
ORDER BY started_at DESC
LIMIT 1;

-- name: EndMeeting :one
-- Idempotent — re-ending a meeting is a no-op that returns the same row.
UPDATE meetings
SET ended_at = COALESCE(ended_at, now()),
    ended_by = COALESCE(ended_by, $2)
WHERE id = $1
RETURNING *;

-- name: BumpMeetingParticipantCount :exec
-- Called when a participant joins. Best-effort counter; actual truth is
-- in LiveKit. Used for analytics + "was this a 1:1 or a group call".
UPDATE meetings SET participant_count = participant_count + 1 WHERE id = $1;

-- ---- DMs ---------------------------------------------------------------

-- name: FindOrCreateDMChannel :one
-- Create-or-return the DM channel for (current_user, other_user) in the
-- current workspace. The handler wraps this in a tx that also:
--   - creates the channels row (type='dm', name=NULL)
--   - inserts channel_memberships rows for both users
-- and only then calls this query to record the pair index.
--
-- UPSERT semantics: if the pair already exists, we return the existing
-- row (the handler uses RETURNING to discover whether it was a hit).
INSERT INTO dm_pairs (workspace_id, user_a, user_b, channel_id)
VALUES (
    sqlc.arg('workspace_id')::bigint,
    LEAST(sqlc.arg('self_id')::bigint, sqlc.arg('other_id')::bigint),
    GREATEST(sqlc.arg('self_id')::bigint, sqlc.arg('other_id')::bigint),
    sqlc.arg('channel_id')::bigint
)
ON CONFLICT (workspace_id, user_a, user_b)
DO UPDATE SET channel_id = dm_pairs.channel_id    -- no-op, to force RETURNING
RETURNING *;

-- name: GetDMChannelForPair :one
-- Read-side lookup. Returns zero rows when no DM exists yet; the handler
-- treats that as "create one".
SELECT * FROM dm_pairs
WHERE workspace_id = sqlc.arg('workspace_id')::bigint
  AND user_a = LEAST(sqlc.arg('self_id')::bigint, sqlc.arg('other_id')::bigint)
  AND user_b = GREATEST(sqlc.arg('self_id')::bigint, sqlc.arg('other_id')::bigint);

-- name: ListDMChannelsForUser :many
-- Every DM this user is a part of in the workspace, plus the "other" user
-- id so the UI can render the counterpart's name without a second query.
-- The CASE outputs are explicitly cast so sqlc types them as bigint
-- rather than interface{}.
SELECT
    p.id AS pair_id,
    c.id AS channel_id,
    c.type AS channel_type,
    c.created_at AS channel_created_at,
    (CASE WHEN p.user_a = sqlc.arg('user_id')::bigint THEN p.user_b ELSE p.user_a END)::bigint AS other_user_id,
    u.display_name AS other_display_name,
    u.email        AS other_email
FROM dm_pairs p
JOIN channels  c ON c.id = p.channel_id
JOIN users     u ON u.id = (CASE WHEN p.user_a = sqlc.arg('user_id')::bigint THEN p.user_b ELSE p.user_a END)::bigint
WHERE p.workspace_id = sqlc.arg('workspace_id')::bigint
  AND (p.user_a = sqlc.arg('user_id')::bigint OR p.user_b = sqlc.arg('user_id')::bigint)
  AND c.archived_at IS NULL
ORDER BY c.created_at DESC;

-- name: CreateDMChannel :one
-- Helper for the find-or-create tx: creates the underlying channel row
-- with type='dm' and NULL name (DMs don't get human-readable names).
INSERT INTO channels (workspace_id, type, name, topic, description, default_join, created_by)
VALUES ($1, 'dm', NULL, '', '', false, $2)
RETURNING *;
