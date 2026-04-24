-- +goose Up
-- +goose StatementBegin

-- Case-insensitive email storage.
CREATE EXTENSION IF NOT EXISTS citext;

-- Cross-workspace user identity. One row per human across the entire install.
-- RLS is NOT enabled here: this is a global table, protected at the app layer.
CREATE TABLE users (
    id                 BIGSERIAL PRIMARY KEY,
    email              CITEXT       NOT NULL UNIQUE,
    password_hash      TEXT,                       -- argon2id; NULL for passwordless (magic-link-only) users
    display_name       TEXT         NOT NULL DEFAULT '',
    avatar_file_id     BIGINT,                     -- FK added in M5 when files table exists
    email_verified_at  TIMESTAMPTZ,
    totp_secret        TEXT,                       -- populated when 2FA is enabled (v1.1)
    locked_until       TIMESTAMPTZ,                -- brute-force lockout
    failed_login_count INT          NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deactivated_at     TIMESTAMPTZ
);

CREATE INDEX idx_users_email_not_deactivated ON users (email) WHERE deactivated_at IS NULL;

-- OAuth identity links. Empty at M1 — populated starting v1.1 when we add Google/GitHub/Apple.
-- Carried in M1 so the data model is complete and forward-compatible.
CREATE TABLE oauth_identities (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider          TEXT        NOT NULL,        -- 'google' | 'github' | 'apple'
    provider_user_id  TEXT        NOT NULL,
    raw               JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX idx_oauth_identities_user_id ON oauth_identities (user_id);

-- Refresh-token-bearing sessions. Access tokens are short-lived JWTs derived from a session.
-- workspace_id is intentionally nullable: sessions exist before a workspace is selected,
-- and span the whole user identity across workspaces (Slack-style, per ADRs).
CREATE TABLE sessions (
    id                   BIGSERIAL PRIMARY KEY,
    user_id              BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id         BIGINT,                    -- FK added in M2
    refresh_token_hash   BYTEA        NOT NULL,     -- sha-256 of opaque refresh token
    user_agent           TEXT         NOT NULL DEFAULT '',
    ip                   INET,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ  NOT NULL,
    revoked_at           TIMESTAMPTZ
);

CREATE INDEX idx_sessions_user_id_active ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_sessions_refresh_hash   ON sessions (refresh_token_hash);
CREATE INDEX idx_sessions_expires_at     ON sessions (expires_at) WHERE revoked_at IS NULL;

-- Single-use tokens table used by email verification, password reset, and magic link flows.
-- Separate rows by 'purpose' so we can apply different TTLs and rate limits.
CREATE TABLE auth_tokens (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT         NOT NULL CHECK (purpose IN ('email_verify','password_reset','magic_link')),
    token_hash  BYTEA        NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ  NOT NULL,
    consumed_at TIMESTAMPTZ,
    ip          INET,
    UNIQUE (token_hash)
);

CREATE INDEX idx_auth_tokens_user_purpose_active
    ON auth_tokens (user_id, purpose)
    WHERE consumed_at IS NULL;

-- Minimal audit log. Tenant-scoped entries add workspace_id starting at M2.
CREATE TABLE audit_log (
    id             BIGSERIAL PRIMARY KEY,
    workspace_id   BIGINT,                       -- NULL for install-level events (auth at M1)
    actor_user_id  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    actor_ip       INET,
    action         TEXT         NOT NULL,       -- 'auth.signup', 'auth.login.success', etc.
    target_kind    TEXT,
    target_id      TEXT,
    metadata       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_created_at     ON audit_log (created_at DESC);
CREATE INDEX idx_audit_log_actor_user_id  ON audit_log (actor_user_id) WHERE actor_user_id IS NOT NULL;
CREATE INDEX idx_audit_log_action         ON audit_log (action);

-- updated_at auto-touch trigger.
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_set_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS oauth_identities;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
