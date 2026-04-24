-- +goose Up
-- search_outbox: durable queue of indexing work that must happen transactionally
-- alongside the message write. The M6 search pipeline is:
--
--   1. Handler writes a message and (in the same tx) writes an outbox row.
--   2. A River periodic job drains pending rows in batches: pulls the current
--      document shape from the hydration query and pushes to Meilisearch.
--   3. Processed rows are marked (kept briefly for audit then pruned).
--
-- Using a Postgres-backed outbox (not just River jobs) lets us keep the source
-- of truth in the same database as the messages themselves — no dual-write
-- hazard. River is the runner, not the store.
--
-- kind/action are string enums kept flexible so the same outbox can ship
-- future indexable resources (channels, files) without a new table.

-- +goose StatementBegin
CREATE TABLE search_outbox (
    id              BIGSERIAL    PRIMARY KEY,
    workspace_id    BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind            TEXT         NOT NULL,                     -- 'message'
    action          TEXT         NOT NULL CHECK (action IN ('index','delete')),
    target_id       BIGINT       NOT NULL,                     -- message id (etc.)
    payload         JSONB        NOT NULL DEFAULT '{}'::jsonb, -- snapshot context
    enqueued_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    processed_at    TIMESTAMPTZ,
    attempts        INT          NOT NULL DEFAULT 0,
    last_error      TEXT
);

-- Hot path: "give me the next batch of pending work ordered by enqueue time".
-- Partial index keeps it tiny once the backlog drains — only pending rows.
CREATE INDEX idx_search_outbox_pending
    ON search_outbox (enqueued_at)
    WHERE processed_at IS NULL;

-- For catch-up scans after a failure storm: find rows with many attempts.
CREATE INDEX idx_search_outbox_errors
    ON search_outbox (attempts DESC, enqueued_at)
    WHERE processed_at IS NULL AND attempts > 0;

-- RLS enabled so handler code (running under sliils_app) cannot read or
-- write outbox rows belonging to another workspace. The drain worker does
-- not use this pool — it connects as the DSN owner (see internal/db.OpenOwner)
-- which bypasses RLS by design, so no worker policy is needed here.
ALTER TABLE search_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE search_outbox FORCE  ROW LEVEL SECURITY;

CREATE POLICY search_outbox_tenant ON search_outbox
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS search_outbox_tenant ON search_outbox;
DROP INDEX IF EXISTS idx_search_outbox_errors;
DROP INDEX IF EXISTS idx_search_outbox_pending;
DROP TABLE IF EXISTS search_outbox;
-- +goose StatementEnd
