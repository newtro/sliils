-- +goose Up
-- Push notification devices (M11).
--
-- One row per (user, device, install) tuple. The subscription shape
-- covers the Web Push API (endpoint + p256dh + auth) plus generic
-- device tokens for APNs / FCM / UnifiedPush which we register but
-- dispatch through an external push-proxy (sliils-push-proxy).
--
-- Why one table for all platforms?
--   The fan-out worker iterates user_devices once and dispatches each
--   to the right driver based on `platform`. A web push goes direct
--   (VAPID → endpoint). APNs / FCM / UnifiedPush go through the
--   proxy. Unifying them keeps the fan-out loop simple.

-- +goose StatementBegin
CREATE TABLE user_devices (
    id            BIGSERIAL   PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    platform      TEXT        NOT NULL CHECK (platform IN ('web','tauri','apns','fcm','unifiedpush')),
    -- Web Push: endpoint is the push service URL (Google, Mozilla, Apple, etc.).
    -- APNs/FCM: endpoint is the device token.
    endpoint      TEXT        NOT NULL,
    -- Only populated for Web Push. Base64url-encoded P-256 public key
    -- and auth secret. Ignored for other platforms.
    p256dh        TEXT        NOT NULL DEFAULT '',
    auth_secret   TEXT        NOT NULL DEFAULT '',

    user_agent    TEXT        NOT NULL DEFAULT '',
    label         TEXT        NOT NULL DEFAULT '',           -- "Scott's MacBook"

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Disabled when the push service tells us the subscription is
    -- gone (HTTP 404/410). We keep the row around for audit but skip
    -- delivery.
    disabled_at   TIMESTAMPTZ,
    disabled_reason TEXT      NOT NULL DEFAULT '',

    UNIQUE (user_id, endpoint)
);

CREATE INDEX idx_user_devices_user  ON user_devices (user_id) WHERE disabled_at IS NULL;
CREATE INDEX idx_user_devices_stale ON user_devices (last_seen_at) WHERE disabled_at IS NULL;

-- user_devices is NOT workspace-scoped (a single user can receive
-- pushes on one device across any workspace they're in), so no RLS
-- policy. The fan-out worker uses the owner pool.
-- +goose StatementEnd

-- +goose StatementBegin
-- Do-not-disturb / quiet hours.
--
-- dnd_enabled_until   "mute everything until this time" (snooze style)
-- quiet_hours_start   minutes since midnight local (0..1440); NULL = disabled
-- quiet_hours_end     minutes since midnight local; may wrap past midnight
--                     (start=1320 end=480 = 22:00 → 08:00)
-- quiet_hours_tz      IANA timezone ('America/New_York'); UTC if NULL
ALTER TABLE users
    ADD COLUMN dnd_enabled_until   TIMESTAMPTZ,
    ADD COLUMN quiet_hours_start   INT,
    ADD COLUMN quiet_hours_end     INT,
    ADD COLUMN quiet_hours_tz      TEXT;

ALTER TABLE users
    ADD CONSTRAINT quiet_hours_range_check
    CHECK (
        (quiet_hours_start IS NULL AND quiet_hours_end IS NULL)
     OR (quiet_hours_start BETWEEN 0 AND 1440 AND quiet_hours_end BETWEEN 0 AND 1440)
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS quiet_hours_range_check,
    DROP COLUMN IF EXISTS quiet_hours_tz,
    DROP COLUMN IF EXISTS quiet_hours_end,
    DROP COLUMN IF EXISTS quiet_hours_start,
    DROP COLUMN IF EXISTS dnd_enabled_until;

DROP TABLE IF EXISTS user_devices;
-- +goose StatementEnd
