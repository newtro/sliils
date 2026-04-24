-- +goose Up
-- mentions: one row per (message, mentioned user). Populated by the
-- CreateMessage handler after parsing <@N> tokens out of body_md.
--
-- Kept separate from messages so:
--   - "what mentions do I have unread?" is an indexed lookup rather than a
--     scan across JSON body_blocks.
--   - notifications, unread badges, and search all hit the same source.
--   - deleting a user cascades to their mention rows without touching
--     message history.

-- +goose StatementBegin
CREATE TABLE mentions (
    id                  BIGSERIAL PRIMARY KEY,
    workspace_id        BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id          BIGINT       NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    message_id          BIGINT       NOT NULL,   -- FK is to partitioned messages; enforced at app layer
    message_created_at  TIMESTAMPTZ  NOT NULL,   -- carried so clients can order without touching messages
    mentioned_user_id   BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_user_id      BIGINT       REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_mentions_user_unread
    ON mentions (mentioned_user_id, created_at DESC);
CREATE INDEX idx_mentions_message ON mentions (message_id);

ALTER TABLE mentions ENABLE ROW LEVEL SECURITY;
ALTER TABLE mentions FORCE  ROW LEVEL SECURITY;

CREATE POLICY mentions_select ON mentions
    FOR SELECT
    USING (
        mentioned_user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

CREATE POLICY mentions_insert ON mentions
    FOR INSERT
    WITH CHECK (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS mentions_insert ON mentions;
DROP POLICY IF EXISTS mentions_select ON mentions;
DROP TABLE IF EXISTS mentions;
-- +goose StatementEnd
