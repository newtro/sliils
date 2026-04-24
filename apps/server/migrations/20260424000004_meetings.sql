-- +goose Up
-- meetings + dm_pairs (M8).
--
-- Two tables land here because they're co-designed:
--
--   meetings
--     A call instance bound to a channel (the "room" in LiveKit terms).
--     Lifecycle: started (row created when first participant joins) →
--     ended (ended_at stamped by the end handler). A channel can host
--     serial meetings over time; each row is an independent call.
--
--   dm_pairs
--     Canonical index over (min(user_id), max(user_id)) pairs mapped to
--     the channel that serves them. Keeps "find-or-create DM with user X"
--     idempotent across concurrent requests — without this, two tabs
--     clicking "Message Bob" at the same time could create two DM
--     channels. UNIQUE on the ordered pair is the coordination point.
--
-- Design notes:
--   - `livekit_room` is unique so a crafted meeting row can't collide
--     with another active room. We derive it server-side from the
--     meeting id (see internal/calls.RoomNameForMeeting) — the column
--     stores the final canonical string for audit.
--   - `recording_file_id` is nullable; wired for when M5.1 lands
--     SeaweedFS + LiveKit Egress. Until then, recording endpoints 503.

-- +goose StatementBegin
CREATE TABLE meetings (
    id                  BIGSERIAL    PRIMARY KEY,
    workspace_id        BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id          BIGINT       NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    event_id            BIGINT,                                          -- FK added in M9 when events table exists
    livekit_room        TEXT         NOT NULL UNIQUE,
    started_by          BIGINT       REFERENCES users(id) ON DELETE SET NULL,
    started_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    ended_at            TIMESTAMPTZ,
    ended_by            BIGINT       REFERENCES users(id) ON DELETE SET NULL,
    recording_file_id   BIGINT       REFERENCES files(id) ON DELETE SET NULL,
    participant_count   INT          NOT NULL DEFAULT 0,
    metadata            JSONB        NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX idx_meetings_channel_active
    ON meetings (channel_id, started_at DESC)
    WHERE ended_at IS NULL;

CREATE INDEX idx_meetings_workspace
    ON meetings (workspace_id, started_at DESC);

ALTER TABLE meetings ENABLE ROW LEVEL SECURITY;
ALTER TABLE meetings FORCE  ROW LEVEL SECURITY;

CREATE POLICY meetings_tenant ON meetings
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- dm_pairs: indexes (least, greatest) of the two user ids so find-or-create
-- is a UPSERT. user_a < user_b by construction (enforced via CHECK).
CREATE TABLE dm_pairs (
    id           BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_a       BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    user_b       BIGINT      NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    channel_id   BIGINT      NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT dm_pair_ordering CHECK (user_a < user_b),
    UNIQUE (workspace_id, user_a, user_b)
);

CREATE INDEX idx_dm_pairs_channel ON dm_pairs (channel_id);
CREATE INDEX idx_dm_pairs_lookup_a ON dm_pairs (workspace_id, user_a);
CREATE INDEX idx_dm_pairs_lookup_b ON dm_pairs (workspace_id, user_b);

ALTER TABLE dm_pairs ENABLE ROW LEVEL SECURITY;
ALTER TABLE dm_pairs FORCE  ROW LEVEL SECURITY;

-- A user sees their own DM pairs (either end of the pair); the workspace
-- scope narrows further. Symmetric to channel_memberships.
CREATE POLICY dm_pairs_self ON dm_pairs
    FOR ALL
    USING (
        user_a = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR user_b = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    )
    WITH CHECK (
        user_a = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR user_b = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS dm_pairs_self ON dm_pairs;
DROP TABLE IF EXISTS dm_pairs;

DROP POLICY IF EXISTS meetings_tenant ON meetings;
DROP INDEX IF EXISTS idx_meetings_workspace;
DROP INDEX IF EXISTS idx_meetings_channel_active;
DROP TABLE IF EXISTS meetings;
-- +goose StatementEnd
