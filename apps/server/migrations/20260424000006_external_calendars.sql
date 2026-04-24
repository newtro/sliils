-- +goose Up
-- external_calendars: per-user OAuth tokens + sync state for Google/
-- Microsoft/CalDAV calendar providers (M9-P3).
--
-- Key design choices:
--   - oauth_refresh_token is encrypted at rest with an app-managed key
--     (AES-256-GCM). The app never logs the plaintext. Provider access
--     tokens aren't stored — we fetch a fresh one each sync using the
--     refresh token.
--   - `sync_token` is the Google/Microsoft-issued incremental sync
--     cursor. After first full sync we only ever ask the provider for
--     changes since this token, which keeps the poll loop cheap.
--   - `last_synced_at` is a monitoring surface, not a gating field —
--     the pull worker runs every 60s regardless.
--
-- A user can connect at most one calendar per provider. That's enforced
-- by UNIQUE (user_id, provider); swap or multi-connect would need a
-- schema change. v1 is single-account-per-provider.

-- +goose StatementBegin
CREATE TABLE external_calendars (
    id                        BIGSERIAL    PRIMARY KEY,
    user_id                   BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider                  TEXT         NOT NULL CHECK (provider IN ('google','microsoft','caldav')),
    external_account_email    CITEXT       NOT NULL,   -- "which gmail?" identity, for display
    oauth_refresh_token       TEXT         NOT NULL,   -- AES-256-GCM ciphertext (base64)
    sync_token                TEXT,                     -- provider-issued cursor
    sync_status               JSONB        NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at            TIMESTAMPTZ,
    connected_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    disconnected_at           TIMESTAMPTZ,
    UNIQUE (user_id, provider)
);

CREATE INDEX idx_external_calendars_active
    ON external_calendars (user_id)
    WHERE disconnected_at IS NULL;

ALTER TABLE external_calendars ENABLE ROW LEVEL SECURITY;
ALTER TABLE external_calendars FORCE  ROW LEVEL SECURITY;

-- A user sees only their own rows. The sync worker runs off the owner
-- pool (RLS bypassed) so it can iterate every active connection.
CREATE POLICY external_cals_self ON external_calendars
    FOR ALL
    USING  (user_id = NULLIF(current_setting('app.user_id', true), '')::bigint)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS external_cals_self ON external_calendars;
DROP INDEX IF EXISTS idx_external_calendars_active;
DROP TABLE IF EXISTS external_calendars;
-- +goose StatementEnd
