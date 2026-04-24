-- +goose Up
-- channel_memberships tracks who is in which channel, their last-read
-- position, and their notification preference. Auto-populated for every
-- default-join channel; explicit insert for private channels (M5+).

-- +goose StatementBegin
CREATE TABLE channel_memberships (
    id                    BIGSERIAL PRIMARY KEY,
    workspace_id          BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id            BIGINT       NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    user_id               BIGINT       NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    notify_pref           TEXT         NOT NULL DEFAULT 'all'
                                       CHECK (notify_pref IN ('all','mentions','mute')),
    last_read_message_id  BIGINT,
    joined_at             TIMESTAMPTZ  NOT NULL DEFAULT now(),
    muted_until           TIMESTAMPTZ,
    UNIQUE (channel_id, user_id)
);

CREATE INDEX idx_channel_memberships_user       ON channel_memberships (user_id);
CREATE INDEX idx_channel_memberships_channel    ON channel_memberships (channel_id);
CREATE INDEX idx_channel_memberships_workspace  ON channel_memberships (workspace_id, user_id);

ALTER TABLE channel_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_memberships FORCE  ROW LEVEL SECURITY;

-- A user sees:
--   - their own rows (any workspace) — drives unread/preferences everywhere
--   - every row in the currently-selected workspace — for member listings
CREATE POLICY chm_select ON channel_memberships
    FOR SELECT
    USING (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

CREATE POLICY chm_modify ON channel_memberships
    FOR ALL
    USING (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    )
    WITH CHECK (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );
-- +goose StatementEnd

-- +goose StatementBegin
-- Auto-join trigger: whenever a user is added to a workspace_membership,
-- enroll them in every default_join channel in that workspace.
CREATE OR REPLACE FUNCTION enroll_user_in_default_channels() RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO channel_memberships (workspace_id, channel_id, user_id, notify_pref)
    SELECT NEW.workspace_id, c.id, NEW.user_id, 'all'
    FROM channels c
    WHERE c.workspace_id = NEW.workspace_id
      AND c.default_join = true
      AND c.archived_at IS NULL
    ON CONFLICT (channel_id, user_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER workspace_membership_enrolls_default_channels
    AFTER INSERT ON workspace_memberships
    FOR EACH ROW EXECUTE FUNCTION enroll_user_in_default_channels();
-- +goose StatementEnd

-- +goose StatementBegin
-- Mirror trigger: when a default_join channel is created, enroll every
-- existing workspace member. Guards against the race where #general is
-- created after an invitee has already joined the workspace.
CREATE OR REPLACE FUNCTION enroll_members_in_default_channel() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.default_join = true AND NEW.archived_at IS NULL THEN
        INSERT INTO channel_memberships (workspace_id, channel_id, user_id, notify_pref)
        SELECT NEW.workspace_id, NEW.id, m.user_id, 'all'
        FROM workspace_memberships m
        WHERE m.workspace_id = NEW.workspace_id
          AND m.deactivated_at IS NULL
        ON CONFLICT (channel_id, user_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER default_channel_enrolls_members
    AFTER INSERT ON channels
    FOR EACH ROW EXECUTE FUNCTION enroll_members_in_default_channel();
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill for any workspaces that already exist: insert missing rows for
-- every (existing owner, existing default-join channel) pair. Idempotent
-- via ON CONFLICT DO NOTHING.
INSERT INTO channel_memberships (workspace_id, channel_id, user_id, notify_pref)
SELECT m.workspace_id, c.id, m.user_id, 'all'
FROM workspace_memberships m
JOIN channels c ON c.workspace_id = m.workspace_id
WHERE m.deactivated_at IS NULL
  AND c.archived_at IS NULL
  AND c.default_join = true
ON CONFLICT (channel_id, user_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS default_channel_enrolls_members           ON channels;
DROP TRIGGER IF EXISTS workspace_membership_enrolls_default_channels ON workspace_memberships;
DROP FUNCTION IF EXISTS enroll_members_in_default_channel();
DROP FUNCTION IF EXISTS enroll_user_in_default_channels();
DROP TABLE IF EXISTS channel_memberships;
-- +goose StatementEnd
