-- +goose Up
-- Messages and reactions. messages is monthly-partitioned from day one so
-- we never have to backfill-partition a 100M-row table later.
--
-- Partitioning choice: native Postgres declarative partitioning
-- (PARTITION BY RANGE on created_at). The kickoff tech-spec names pg_partman
-- as the intended management layer, but the table structure is the same
-- either way — pg_partman is strictly a maintenance UX, not a data-model
-- change. We'll adopt it in a future milestone if monthly partition creation
-- becomes annoying; for now a small PL/pgSQL helper handles it.
--
-- RLS on messages:
--   SELECT / INSERT / UPDATE / DELETE all gated by app.workspace_id.
--   This is simpler than per-channel policies at M3 (no private channels
--   yet). When private channels land we'll add a subquery against
--   channel_memberships.

-- +goose StatementBegin
CREATE TABLE messages (
    id              BIGSERIAL   NOT NULL,
    workspace_id    BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id      BIGINT      NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    thread_root_id  BIGINT,     -- self-id for thread roots, NULL for non-thread messages (M4)
    parent_id       BIGINT,     -- direct parent inside a thread tree (M4)
    author_user_id  BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    author_bot_id   BIGINT,     -- FK added in M12 when bots table exists
    body_md         TEXT        NOT NULL DEFAULT '',
    body_blocks     JSONB       NOT NULL DEFAULT '[]'::jsonb,
    edited_at       TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite PK required by partitioned tables: the partition key must
    -- be part of every unique constraint.
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_messages_channel_created ON messages (channel_id, created_at DESC);
CREATE INDEX idx_messages_workspace       ON messages (workspace_id);
CREATE INDEX idx_messages_thread          ON messages (thread_root_id, created_at) WHERE thread_root_id IS NOT NULL;
CREATE INDEX idx_messages_author          ON messages (author_user_id) WHERE author_user_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Partition management helper. Idempotent per month. Callers pass the first
-- day of the month to create.
CREATE OR REPLACE FUNCTION create_messages_partition(month_start DATE)
RETURNS void AS $$
DECLARE
    part_name TEXT;
    next_month DATE;
BEGIN
    part_name := 'messages_' || to_char(month_start, 'YYYY_MM');
    next_month := (month_start + INTERVAL '1 month')::date;

    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF messages FOR VALUES FROM (%L) TO (%L)',
        part_name,
        month_start::timestamptz,
        next_month::timestamptz
    );

    -- Ensure the partition is readable by the runtime role. Partition child
    -- tables don't inherit grants from the parent by default on older
    -- Postgres versions; explicit GRANT is safest.
    EXECUTE format(
        'GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE %I TO sliils_app',
        part_name
    );
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
-- Bootstrap: create 4 months of partitions (current + 3 ahead).
-- Future milestones will run a River job monthly to extend the window.
SELECT create_messages_partition(date_trunc('month', now())::date);
SELECT create_messages_partition((date_trunc('month', now()) + INTERVAL '1 month')::date);
SELECT create_messages_partition((date_trunc('month', now()) + INTERVAL '2 month')::date);
SELECT create_messages_partition((date_trunc('month', now()) + INTERVAL '3 month')::date);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE  ROW LEVEL SECURITY;

CREATE POLICY messages_tenant ON messages
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE message_reactions (
    message_id    BIGINT      NOT NULL,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji         TEXT        NOT NULL CHECK (length(emoji) BETWEEN 1 AND 64),
    workspace_id  BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
-- No foreign key against messages.id (messages is partitioned and the FK
-- target would need the full composite (id, created_at)). Integrity is
-- enforced at the app layer (create on 404 returns an error).

CREATE INDEX idx_reactions_message ON message_reactions (message_id);
CREATE INDEX idx_reactions_user    ON message_reactions (user_id);

ALTER TABLE message_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_reactions FORCE  ROW LEVEL SECURITY;

CREATE POLICY reactions_tenant ON message_reactions
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS reactions_tenant ON message_reactions;
DROP TABLE IF EXISTS message_reactions;

DROP POLICY IF EXISTS messages_tenant ON messages;
-- DROPping the partitioned parent cascades to all partitions.
DROP TABLE IF EXISTS messages CASCADE;
DROP FUNCTION IF EXISTS create_messages_partition(DATE);
-- +goose StatementEnd
