-- +goose Up
-- Native Pages (M10-P1): rich-text collaborative docs backed by Yjs.
--
-- The Pages model:
--   pages              : the logical document (title, optional channel_id,
--                        created_by). Real content lives in Y-Sweet under
--                        `doc_id` and is streamed to clients directly via
--                        y-websocket; the Go server only holds metadata +
--                        issues short-lived client tokens.
--   page_snapshots     : periodic binary Yjs snapshots persisted so we can
--                        roll back a page or show a version-history view
--                        without asking Y-Sweet for its append-only log.
--                        snapshot_data is the raw `Y.encodeStateAsUpdate`
--                        byte string; applying it to a fresh Y.Doc yields
--                        the page content at snapshot time.
--
-- Why a separate doc_id vs. just using the pages.id?
--   pages.id is a BIGSERIAL that leaks "how many pages exist" to Y-Sweet.
--   A UUID doc_id is opaque and lets us reuse the same Y-Sweet instance
--   across multiple SliilS installs without collision.
--
-- RLS strategy: pages + page_snapshots are straight workspace_id-scoped,
-- like events. Channel association is a soft link used for navigation;
-- pages with channel_id = NULL are workspace-wide.

-- +goose StatementBegin
CREATE TABLE pages (
    id           BIGSERIAL    PRIMARY KEY,
    workspace_id BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id   BIGINT                REFERENCES channels(id)   ON DELETE SET NULL,
    title        TEXT         NOT NULL DEFAULT 'Untitled',
    doc_id       TEXT         NOT NULL UNIQUE,   -- opaque Y-Sweet document id
    icon         TEXT         NOT NULL DEFAULT '',   -- emoji for the sidebar entry; cosmetic
    created_by   BIGINT                REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    archived_at  TIMESTAMPTZ
);

CREATE INDEX idx_pages_workspace   ON pages (workspace_id, updated_at DESC) WHERE archived_at IS NULL;
CREATE INDEX idx_pages_channel     ON pages (channel_id)   WHERE channel_id IS NOT NULL AND archived_at IS NULL;

ALTER TABLE pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE pages FORCE  ROW LEVEL SECURITY;

CREATE POLICY pages_tenant ON pages
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE page_snapshots (
    id            BIGSERIAL    PRIMARY KEY,
    page_id       BIGINT       NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id  BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    snapshot_data BYTEA        NOT NULL,          -- Yjs state-as-update
    byte_size     INT          NOT NULL CHECK (byte_size >= 0),
    created_by    BIGINT                REFERENCES users(id) ON DELETE SET NULL,
    reason        TEXT         NOT NULL DEFAULT 'periodic', -- 'periodic' | 'manual' | 'restore'
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_snapshots_page_created ON page_snapshots (page_id, created_at DESC);

ALTER TABLE page_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_snapshots FORCE  ROW LEVEL SECURITY;

CREATE POLICY page_snapshots_tenant ON page_snapshots
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- Page comments. Anchor is an opaque string the editor owns (e.g. a Yjs
-- RelativePosition serialized as JSON). The server doesn't interpret it;
-- it just stores + round-trips. resolved_at lets us hide resolved threads
-- without deleting history.
CREATE TABLE page_comments (
    id           BIGSERIAL    PRIMARY KEY,
    page_id      BIGINT       NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    workspace_id BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    parent_id    BIGINT                REFERENCES page_comments(id) ON DELETE CASCADE,
    author_id    BIGINT                REFERENCES users(id) ON DELETE SET NULL,
    anchor       TEXT         NOT NULL DEFAULT '', -- editor-owned anchor string
    body_md      TEXT         NOT NULL,
    resolved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

CREATE INDEX idx_page_comments_page ON page_comments (page_id, created_at)
    WHERE deleted_at IS NULL;

ALTER TABLE page_comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_comments FORCE  ROW LEVEL SECURITY;

CREATE POLICY page_comments_tenant ON page_comments
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS page_comments_tenant   ON page_comments;
DROP TABLE IF EXISTS page_comments;

DROP POLICY IF EXISTS page_snapshots_tenant  ON page_snapshots;
DROP TABLE IF EXISTS page_snapshots;

DROP POLICY IF EXISTS pages_tenant           ON pages;
DROP TABLE IF EXISTS pages;
-- +goose StatementEnd
