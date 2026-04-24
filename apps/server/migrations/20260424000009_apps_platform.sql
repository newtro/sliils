-- +goose Up
-- Apps platform (M12-P1).
--
-- Five related tables cover the Slack-shaped extensibility surface:
--
--   apps                   The package a developer publishes. Lives at
--                          the install-wide level — one apps row can be
--                          installed into many workspaces.
--   app_installations      A specific workspace's install of an app.
--                          Carries the granted scopes + bot user.
--   bots                   The synthetic user an app posts as. One per
--                          installation; attributed on messages via
--                          messages.author_bot_id.
--   app_oauth_codes        Short-lived authorization codes exchanged for
--                          access tokens during OAuth. Single-use.
--   app_tokens             Long-lived access tokens scoped to an install.
--                          Hashed at rest — we never store plaintext.
--
-- Why a `slug`? Apps get listed by slug in the install URL so admins
-- don't see opaque ids ("/apps/install/github-integration" beats "/apps/install/42").
-- Collision-free globally: apps is an install-level table.
--
-- Why not FORCE RLS on apps? Apps are install-global — a developer's own
-- workspace role doesn't decide whether their app exists. The /dev/apps
-- handlers gate by owner_user_id directly. Installations + bots + tokens
-- DO live under RLS since they're workspace-scoped.

-- +goose StatementBegin
CREATE TABLE apps (
    id                  BIGSERIAL   PRIMARY KEY,
    slug                TEXT        NOT NULL UNIQUE,       -- "github", "grafana-alerts"
    name                TEXT        NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    owner_user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    avatar_file_id      BIGINT      REFERENCES files(id) ON DELETE SET NULL,
    -- Manifest is the developer-declared capabilities surface: scopes
    -- the app asks for, redirect URIs for OAuth, event subscriptions,
    -- slash commands. JSONB so the shape can evolve without migrations.
    manifest            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- client_id is what a third-party integrator puts in their config.
    -- Generated on create, stable for the app's lifetime.
    client_id           TEXT        NOT NULL UNIQUE,
    -- client_secret is shown ONCE on create (and optional rotate). We
    -- store only the hash so a DB leak doesn't impersonate apps.
    client_secret_hash  TEXT        NOT NULL,
    is_public           BOOLEAN     NOT NULL DEFAULT false, -- visible to non-owners in a future directory UI; off for v1
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);

CREATE INDEX idx_apps_owner ON apps (owner_user_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE app_installations (
    id                  BIGSERIAL   PRIMARY KEY,
    app_id              BIGINT      NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    workspace_id        BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    installed_by        BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    -- Actual scopes granted at install time (subset of the manifest's
    -- requested scopes). Stored verbatim so future manifest changes
    -- don't silently widen permissions.
    scopes              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    -- bot_user_id points at the synthetic user this install posts as.
    -- Populated iff "bot" scope was granted.
    bot_user_id         BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    UNIQUE (app_id, workspace_id)
);

CREATE INDEX idx_app_installations_ws ON app_installations (workspace_id) WHERE revoked_at IS NULL;

ALTER TABLE app_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_installations FORCE  ROW LEVEL SECURITY;

CREATE POLICY app_installations_tenant ON app_installations
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- Bot users are a row in users with `is_bot=true`. That lets every existing
-- message/reaction/mention path keep working without a parallel bot_messages
-- table. The trade-off is that bots occupy id space in users — acceptable
-- at v1 scale, revisit if it becomes a problem.
ALTER TABLE users
    ADD COLUMN is_bot              BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN bot_app_installation_id BIGINT REFERENCES app_installations(id) ON DELETE CASCADE;

CREATE INDEX idx_users_bot_install ON users (bot_app_installation_id) WHERE is_bot = true;
-- +goose StatementEnd

-- +goose StatementBegin
-- OAuth authorization codes: single-use, 10-min TTL. Exchanged by the
-- app's backend at the token endpoint. PKCE `code_challenge` is stored
-- so the exchanging party must prove knowledge of the matching verifier.
CREATE TABLE app_oauth_codes (
    code                TEXT        PRIMARY KEY,        -- opaque, 32-byte base64url
    app_id              BIGINT      NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    workspace_id        BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id             BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri        TEXT        NOT NULL,
    scopes              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    code_challenge      TEXT        NOT NULL,           -- PKCE
    code_challenge_method TEXT      NOT NULL CHECK (code_challenge_method IN ('S256','plain')),
    expires_at          TIMESTAMPTZ NOT NULL,
    used_at             TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_app_oauth_codes_expires ON app_oauth_codes (expires_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Access tokens the installed app uses to call the Slack-shaped bot API.
-- Stored hashed so a DB leak doesn't hand the attacker a valid token.
-- token_id is embedded in the token itself so we can lookup the row
-- without scanning (token format: "slis-xat-{token_id}-{secret}").
CREATE TABLE app_tokens (
    token_id            BIGSERIAL   PRIMARY KEY,
    app_installation_id BIGINT      NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    workspace_id        BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token_hash          TEXT        NOT NULL,           -- sha256 hex of the secret part
    label               TEXT        NOT NULL DEFAULT '',
    scopes              JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at        TIMESTAMPTZ,
    revoked_at          TIMESTAMPTZ
);

CREATE INDEX idx_app_tokens_install ON app_tokens (app_installation_id) WHERE revoked_at IS NULL;

ALTER TABLE app_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_tokens FORCE  ROW LEVEL SECURITY;

CREATE POLICY app_tokens_tenant ON app_tokens
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- Hook bots up to messages: author_bot_id points at the bot user AND
-- the installation so we can audit-trail "which app posted this".
-- Messages is partitioned — alter the base table + let the declarative
-- partition system inherit.
ALTER TABLE messages
    ADD COLUMN author_bot_installation_id BIGINT REFERENCES app_installations(id) ON DELETE SET NULL;

-- Index is optional here; queries by installation are rare enough to
-- accept a seq scan at v1 scale.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE messages DROP COLUMN IF EXISTS author_bot_installation_id;

DROP POLICY IF EXISTS app_tokens_tenant ON app_tokens;
DROP TABLE IF EXISTS app_tokens;
DROP TABLE IF EXISTS app_oauth_codes;

ALTER TABLE users
    DROP COLUMN IF EXISTS bot_app_installation_id,
    DROP COLUMN IF EXISTS is_bot;

DROP POLICY IF EXISTS app_installations_tenant ON app_installations;
DROP TABLE IF EXISTS app_installations;

DROP TABLE IF EXISTS apps;
-- +goose StatementEnd
