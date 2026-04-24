-- +goose Up
-- events + event_attendees (M9).
--
-- The calendar model:
--   events           : a single scheduled occurrence OR an RRULE-expanded
--                      series root. start_at + end_at define the first
--                      (or only) occurrence; rrule expands the rest at
--                      query time via the application layer.
--   event_attendees  : who's invited + their RSVP state. Internal users
--                      (user_id set) and external invitees (external_email
--                      set) coexist so an event can span beyond SliilS.
--
-- Why RRULE strings in a column instead of exploded occurrences?
--   Materializing every instance of a daily-for-five-years event blows
--   up storage AND makes edits painful ("cancel every Tuesday"). RRULE
--   is the same representation Google/Outlook/iCal use, so round-trips
--   through M9-P3 external sync stay lossless.
--
-- Why NOT a partitioned table?
--   Events are a much smaller volume than messages. A lookup by (workspace,
--   date-range) is the hot query; a single b-tree on (workspace_id,
--   start_at) gets us plenty for v1. Revisit at workspaces scale.

-- +goose StatementBegin
CREATE TABLE events (
    id                  BIGSERIAL    PRIMARY KEY,
    workspace_id        BIGINT       NOT NULL REFERENCES workspaces(id)  ON DELETE CASCADE,
    channel_id          BIGINT                REFERENCES channels(id)    ON DELETE SET NULL,
    title               TEXT         NOT NULL,
    description         TEXT         NOT NULL DEFAULT '',
    location_url        TEXT         NOT NULL DEFAULT '',
    start_at            TIMESTAMPTZ  NOT NULL,
    end_at              TIMESTAMPTZ  NOT NULL,
    time_zone           TEXT         NOT NULL DEFAULT 'UTC',  -- IANA name, e.g. 'America/New_York'
    rrule               TEXT,                                  -- RFC 5545 recurrence string; NULL = single instance
    recording_enabled   BOOLEAN      NOT NULL DEFAULT false,
    video_enabled       BOOLEAN      NOT NULL DEFAULT true,    -- creates a meeting on Join if true
    created_by          BIGINT                REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    canceled_at         TIMESTAMPTZ,
    -- External sync plumbing (M9-P3). Events imported from Google/Microsoft
    -- carry these so the sync worker can dedupe + avoid echo. Null for
    -- SliilS-native events; set once the push succeeds.
    external_provider   TEXT CHECK (external_provider IN ('google','microsoft','caldav')),
    external_event_id   TEXT,
    external_etag       TEXT,
    UNIQUE (external_provider, external_event_id)
);

CREATE INDEX idx_events_workspace_range
    ON events (workspace_id, start_at)
    WHERE canceled_at IS NULL;

CREATE INDEX idx_events_channel
    ON events (channel_id, start_at)
    WHERE channel_id IS NOT NULL AND canceled_at IS NULL;

CREATE TRIGGER events_set_updated_at
    BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE events ENABLE ROW LEVEL SECURITY;
ALTER TABLE events FORCE  ROW LEVEL SECURITY;

CREATE POLICY events_tenant ON events
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE event_attendees (
    id              BIGSERIAL    PRIMARY KEY,
    event_id        BIGINT       NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id         BIGINT                REFERENCES users(id)  ON DELETE CASCADE,
    external_email  CITEXT,
    rsvp            TEXT         NOT NULL DEFAULT 'pending'
                                   CHECK (rsvp IN ('pending','yes','no','maybe')),
    invited_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    responded_at    TIMESTAMPTZ,
    -- Exactly one of user_id or external_email must be set.
    CONSTRAINT attendee_identity CHECK (
        (user_id IS NOT NULL AND external_email IS NULL)
        OR (user_id IS NULL AND external_email IS NOT NULL)
    )
);

-- Internal attendees: unique per (event, user).
CREATE UNIQUE INDEX ux_event_attendees_user
    ON event_attendees (event_id, user_id)
    WHERE user_id IS NOT NULL;

-- External-only attendees: unique per (event, email).
CREATE UNIQUE INDEX ux_event_attendees_email
    ON event_attendees (event_id, external_email)
    WHERE external_email IS NOT NULL;

CREATE INDEX idx_event_attendees_user
    ON event_attendees (user_id, event_id)
    WHERE user_id IS NOT NULL;

ALTER TABLE event_attendees ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_attendees FORCE  ROW LEVEL SECURITY;

-- A user sees attendees for any event they can see (events RLS gates the
-- parent). We re-state the same check via a subquery so the policy is
-- self-contained; Postgres optimizes this cleanly.
CREATE POLICY event_attendees_visible ON event_attendees
    FOR ALL
    USING (
        event_id IN (
            SELECT id FROM events
            WHERE workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
        )
    )
    WITH CHECK (
        event_id IN (
            SELECT id FROM events
            WHERE workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
        )
    );
-- +goose StatementEnd

-- +goose StatementBegin
-- meetings.event_id became meaningful as of M9 — promote to a real FK so
-- deleting a cancelled event cascades to cleaning up the meeting row.
ALTER TABLE meetings
    ADD CONSTRAINT meetings_event_fk FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meetings DROP CONSTRAINT IF EXISTS meetings_event_fk;

DROP POLICY IF EXISTS event_attendees_visible ON event_attendees;
DROP INDEX IF EXISTS idx_event_attendees_user;
DROP INDEX IF EXISTS ux_event_attendees_email;
DROP INDEX IF EXISTS ux_event_attendees_user;
DROP TABLE IF EXISTS event_attendees;

DROP POLICY IF EXISTS events_tenant ON events;
DROP INDEX IF EXISTS idx_events_channel;
DROP INDEX IF EXISTS idx_events_workspace_range;
DROP TABLE IF EXISTS events;
-- +goose StatementEnd
