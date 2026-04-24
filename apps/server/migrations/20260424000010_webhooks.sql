-- +goose Up
-- Webhooks + slash commands (M12-P2).
--
--   webhooks_incoming      A per-channel URL that accepts arbitrary POSTed
--                          JSON and posts a message into the channel. The
--                          signing_secret_hash is optional — if set, the
--                          sender must HMAC the body with the secret.
--
--   webhooks_outgoing      Subscriptions that fire when workspace events
--                          match event_pattern. Delivery goes through
--                          the River queue (M12 adds a worker). Each
--                          delivery carries an HMAC signature so the
--                          receiver can verify authenticity + prevent
--                          replay (timestamp in the header).
--
--   webhook_deliveries     Audit trail. Keeps attempt count, last status
--                          code, last error. Rate-limits future retries.
--
--   slash_commands         One per (workspace, command). The invocation
--                          handler validates the token, POSTs to target_url.

-- +goose StatementBegin
CREATE TABLE webhooks_incoming (
    id                 BIGSERIAL   PRIMARY KEY,
    workspace_id       BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id         BIGINT      NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    name               TEXT        NOT NULL,
    -- token is the opaque random string inside the public URL. Never
    -- reissued — revoke by deleting the row.
    token              TEXT        NOT NULL UNIQUE,
    -- signing_secret_hash is the SHA-256 of an optional shared-secret.
    -- When non-empty, POSTs MUST carry a valid X-SliilS-Signature.
    signing_secret_hash TEXT       NOT NULL DEFAULT '',
    created_by         BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at       TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ
);

CREATE INDEX idx_wh_in_ws ON webhooks_incoming (workspace_id) WHERE deleted_at IS NULL;

ALTER TABLE webhooks_incoming ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhooks_incoming FORCE  ROW LEVEL SECURITY;

CREATE POLICY webhooks_incoming_tenant ON webhooks_incoming
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE webhooks_outgoing (
    id                  BIGSERIAL   PRIMARY KEY,
    workspace_id        BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    app_installation_id BIGINT      NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    -- Event pattern: "message.created", "reaction.added", "*" for all.
    -- v1 supports exact names and "*"; glob matching is deferred.
    event_pattern       TEXT        NOT NULL,
    target_url          TEXT        NOT NULL,
    signing_secret_hash TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,
    UNIQUE (app_installation_id, event_pattern, target_url)
);

CREATE INDEX idx_wh_out_ws ON webhooks_outgoing (workspace_id, event_pattern) WHERE deleted_at IS NULL;

ALTER TABLE webhooks_outgoing ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhooks_outgoing FORCE  ROW LEVEL SECURITY;

CREATE POLICY webhooks_outgoing_tenant ON webhooks_outgoing
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE slash_commands (
    id                  BIGSERIAL   PRIMARY KEY,
    workspace_id        BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    app_installation_id BIGINT      NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    command             TEXT        NOT NULL,         -- "/poll"
    target_url          TEXT        NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    usage_hint          TEXT        NOT NULL DEFAULT '',
    signing_secret_hash TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,
    UNIQUE (workspace_id, command)
);

CREATE INDEX idx_slash_commands_ws ON slash_commands (workspace_id) WHERE deleted_at IS NULL;

ALTER TABLE slash_commands ENABLE ROW LEVEL SECURITY;
ALTER TABLE slash_commands FORCE  ROW LEVEL SECURITY;

CREATE POLICY slash_commands_tenant ON slash_commands
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- Message body already carries body_blocks JSONB from M3. We keep it
-- as-is and use it for Block-Kit blocks too; server validates shape on
-- insert.
-- (No schema change needed.)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS slash_commands_tenant    ON slash_commands;
DROP TABLE IF EXISTS slash_commands;

DROP POLICY IF EXISTS webhooks_outgoing_tenant ON webhooks_outgoing;
DROP TABLE IF EXISTS webhooks_outgoing;

DROP POLICY IF EXISTS webhooks_incoming_tenant ON webhooks_incoming;
DROP TABLE IF EXISTS webhooks_incoming;
-- +goose StatementEnd
