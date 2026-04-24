-- +goose Up
-- Install-wide + per-workspace runtime-configurable settings.
--
-- Two scopes:
--
--   install_settings          Single-install shared values. Holds the
--                             default email provider used for auth
--                             flows (magic-link, password reset,
--                             verify-email) and policy knobs like
--                             signup_mode. One row per key.
--
--   workspace_email_settings  Per-tenant email config for workspace-
--                             originated mail (invites, and future
--                             workspace-level notifications). Nullable
--                             fields; when empty the server falls back
--                             to install_settings. Each workspace has
--                             at most one row.
--
-- Sensitive values (API keys, signing secrets) are stored as ciphertext
-- produced by internal/secretbox with SLIILS_SETTINGS_ENCRYPTION_KEY.
-- The `encrypted` column flags which value columns need decrypting.

-- +goose StatementBegin
CREATE TABLE install_settings (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL DEFAULT '',
    encrypted  BOOLEAN     NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT      REFERENCES users(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Workspace-scoped email provider overrides. If resend_api_key_enc is
-- empty the workspace inherits the install default. Always storing the
-- plaintext from_address (it's public metadata) but the API key is
-- ciphertext.
CREATE TABLE workspace_email_settings (
    workspace_id         BIGINT      PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    provider             TEXT        NOT NULL DEFAULT 'resend',  -- 'resend' | 'smtp' (smtp v1.1)
    resend_api_key_enc   TEXT        NOT NULL DEFAULT '',        -- ciphertext via internal/secretbox
    from_address         TEXT        NOT NULL DEFAULT '',
    from_name            TEXT        NOT NULL DEFAULT '',
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by           BIGINT      REFERENCES users(id) ON DELETE SET NULL
);

-- Workspace owners + admins read/write their own row only. RLS policy
-- matches existing tenant tables.
ALTER TABLE workspace_email_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_email_settings FORCE  ROW LEVEL SECURITY;

CREATE POLICY workspace_email_settings_tenant ON workspace_email_settings
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS workspace_email_settings_tenant ON workspace_email_settings;
DROP TABLE IF EXISTS workspace_email_settings;
DROP TABLE IF EXISTS install_settings;
-- +goose StatementEnd
