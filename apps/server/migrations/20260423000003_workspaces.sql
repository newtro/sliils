-- +goose Up
-- Workspace + membership + channel schema with row-level security enforced
-- against the non-owner runtime role created in migration 2.
--
-- GUCs (session / transaction variables) the app sets per request:
--   app.user_id       — the authenticated user's id (always set for any
--                        authed request, so RLS on *_memberships works)
--   app.workspace_id  — the currently-selected workspace (set inside a
--                        transaction when a request is scoped to one)
--
-- `current_setting(name, true)` returns NULL when the GUC is unset, instead
-- of raising — that's what lets unauthenticated public routes run without
-- tripping policies.

-- +goose StatementBegin
CREATE TABLE workspaces (
    id             BIGSERIAL PRIMARY KEY,
    slug           CITEXT       NOT NULL UNIQUE,
    name           TEXT         NOT NULL,
    description    TEXT         NOT NULL DEFAULT '',
    brand_color    TEXT,                              -- hex, nullable (uses default blue)
    logo_file_id   BIGINT,                            -- FK added in M5
    retention_days INT,                               -- NULL = keep forever
    created_by     BIGINT       NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    archived_at    TIMESTAMPTZ
);

CREATE INDEX idx_workspaces_created_by  ON workspaces (created_by);
CREATE INDEX idx_workspaces_active      ON workspaces (id) WHERE archived_at IS NULL;

CREATE TRIGGER workspaces_set_updated_at
    BEFORE UPDATE ON workspaces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE workspace_memberships (
    id              BIGSERIAL PRIMARY KEY,
    workspace_id    BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id         BIGINT       NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    role            TEXT         NOT NULL CHECK (role IN ('owner','admin','member','guest')),
    custom_status   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    joined_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deactivated_at  TIMESTAMPTZ,
    UNIQUE (workspace_id, user_id)
);

CREATE INDEX idx_memberships_user_active
    ON workspace_memberships (user_id)
    WHERE deactivated_at IS NULL;

CREATE TABLE channels (
    id             BIGSERIAL PRIMARY KEY,
    workspace_id   BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    type           TEXT         NOT NULL CHECK (type IN ('public','private','dm','group_dm')),
    name           CITEXT,                            -- null for DMs / group DMs
    topic          TEXT         NOT NULL DEFAULT '',
    description    TEXT         NOT NULL DEFAULT '',
    default_join   BOOL         NOT NULL DEFAULT false,
    created_by     BIGINT       REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    archived_at    TIMESTAMPTZ
);

-- Channel names are unique within a workspace. DMs have NULL names so the
-- partial unique index only covers named channels.
CREATE UNIQUE INDEX ux_channels_workspace_name
    ON channels (workspace_id, name)
    WHERE name IS NOT NULL;

CREATE INDEX idx_channels_workspace_active
    ON channels (workspace_id)
    WHERE archived_at IS NULL;

CREATE TRIGGER channels_set_updated_at
    BEFORE UPDATE ON channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- Row level security --------------------------------------------------

ALTER TABLE workspaces             ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces             FORCE  ROW LEVEL SECURITY;
ALTER TABLE workspace_memberships  ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_memberships  FORCE  ROW LEVEL SECURITY;
ALTER TABLE channels               ENABLE ROW LEVEL SECURITY;
ALTER TABLE channels               FORCE  ROW LEVEL SECURITY;

-- workspace_memberships: a user sees their own memberships (for the
-- workspace switcher) OR every membership in the currently-selected
-- workspace (for listing members inside a workspace).
CREATE POLICY wsm_select ON workspace_memberships
    FOR SELECT
    USING (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

-- Inserts/updates must always be scoped — either the actor inserting their
-- own row, or an op inside the currently-selected workspace.
CREATE POLICY wsm_modify ON workspace_memberships
    FOR ALL
    USING (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    )
    WITH CHECK (
        user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

-- workspaces: a user may see a workspace row iff they have a membership in
-- it. Implicit via a subquery against workspace_memberships (which itself
-- is RLS-protected so only *this user's* memberships are visible).
CREATE POLICY ws_select ON workspaces
    FOR SELECT
    USING (
        id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND deactivated_at IS NULL
        )
    );

-- Workspace creation is a cross-workspace operation (no ws exists yet), so
-- the INSERT policy is open; the app layer verifies the authenticated user
-- is creating a workspace for themselves. After creation, SELECT/UPDATE/
-- DELETE are membership-scoped.
CREATE POLICY ws_insert ON workspaces
    FOR INSERT
    WITH CHECK (
        created_by = NULLIF(current_setting('app.user_id', true), '')::bigint
    );

CREATE POLICY ws_update ON workspaces
    FOR UPDATE
    USING (
        id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND role IN ('owner','admin')
              AND deactivated_at IS NULL
        )
    );

-- channels: straight tenant scoping on app.workspace_id.
CREATE POLICY ch_all ON channels
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);

-- ---- Audit log now carries workspace_id -----------------------------------

-- audit_log was created without RLS (install-level events are NULL-workspace).
-- Add RLS so tenant-scoped entries can't leak across workspaces when an admin
-- queries them.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE  ROW LEVEL SECURITY;

CREATE POLICY audit_select ON audit_log
    FOR SELECT
    USING (
        workspace_id IS NULL
        OR workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

CREATE POLICY audit_insert ON audit_log
    FOR INSERT
    WITH CHECK (true);  -- Auth events write NULL workspace_id; ws-scoped events
                        -- rely on the setter having set app.workspace_id.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS audit_insert  ON audit_log;
DROP POLICY IF EXISTS audit_select  ON audit_log;
ALTER TABLE audit_log NO FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_log DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS ch_all         ON channels;
DROP POLICY IF EXISTS ws_update      ON workspaces;
DROP POLICY IF EXISTS ws_insert      ON workspaces;
DROP POLICY IF EXISTS ws_select      ON workspaces;
DROP POLICY IF EXISTS wsm_modify     ON workspace_memberships;
DROP POLICY IF EXISTS wsm_select     ON workspace_memberships;

DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS workspace_memberships;
DROP TABLE IF EXISTS workspaces;
-- +goose StatementEnd
