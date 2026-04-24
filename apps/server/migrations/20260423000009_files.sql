-- +goose Up
-- Files + message attachments. Both tenant-scoped via RLS so one workspace
-- can never reach another workspace's objects, even with a crafted request.
--
-- Uploads store:
--   storage_backend  — which IStorage driver owns the bytes ("local" at M5,
--                      "s3" when SeaweedFS / S3-compat lands)
--   storage_key      — opaque path/key the driver uses to fetch/put bytes
--   sha256           — hex of the file content for idempotent re-uploads
--                      and attachment dedupe. UNIQUE per workspace.
--   mime / size      — what the server observed on ingest (not what the
--                      client claimed — verified during upload handler).
--   scan_status      — 'pending' until an AV scanner signs off. clamd
--                      integration is M5.1; for now the handler sets 'clean'
--                      immediately so downloads aren't gated.
--   derivatives      — {thumb_64, thumb_400, thumb_1600, hls_master,
--                      audio_waveform}. Populated by the media pipeline
--                      (River workers, M5.1). Empty at M5.

-- +goose StatementBegin
CREATE TABLE files (
    id                 BIGSERIAL PRIMARY KEY,
    workspace_id       BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    uploader_user_id   BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    storage_backend    TEXT        NOT NULL DEFAULT 'local',
    storage_key        TEXT        NOT NULL,
    filename           TEXT        NOT NULL,
    mime               TEXT        NOT NULL,
    size_bytes         BIGINT      NOT NULL CHECK (size_bytes >= 0),
    sha256             TEXT        NOT NULL CHECK (length(sha256) = 64),
    scan_status        TEXT        NOT NULL DEFAULT 'pending'
                                   CHECK (scan_status IN ('pending','clean','infected','failed')),
    derivatives        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    width              INT,                    -- populated for images after EXIF-strip/decode
    height             INT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    UNIQUE (workspace_id, sha256)
);

CREATE INDEX idx_files_workspace_created ON files (workspace_id, created_at DESC);
CREATE INDEX idx_files_uploader          ON files (uploader_user_id) WHERE uploader_user_id IS NOT NULL;

ALTER TABLE files ENABLE ROW LEVEL SECURITY;
ALTER TABLE files FORCE  ROW LEVEL SECURITY;

CREATE POLICY files_tenant ON files
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose StatementBegin
-- The tech-spec names a workspaces.logo_file_id FK on files. Add it now
-- that files exists. (Migration 003 left workspaces.logo_file_id as a
-- plain BIGINT because files didn't exist yet.)
ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_logo_file_id_fkey
    FOREIGN KEY (logo_file_id) REFERENCES files(id) ON DELETE SET NULL;

-- users.avatar_file_id gets the same treatment.
ALTER TABLE users
    ADD CONSTRAINT users_avatar_file_id_fkey
    FOREIGN KEY (avatar_file_id) REFERENCES files(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE message_attachments (
    id            BIGSERIAL PRIMARY KEY,
    workspace_id  BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id    BIGINT      NOT NULL REFERENCES channels(id)   ON DELETE CASCADE,
    message_id    BIGINT      NOT NULL,   -- messages is partitioned, FK impractical; enforced at app layer
    file_id       BIGINT      NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    position      INT         NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, file_id)
);

CREATE INDEX idx_attachments_message ON message_attachments (message_id);
CREATE INDEX idx_attachments_file    ON message_attachments (file_id);

ALTER TABLE message_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_attachments FORCE  ROW LEVEL SECURITY;

CREATE POLICY attachments_tenant ON message_attachments
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS attachments_tenant ON message_attachments;
DROP TABLE IF EXISTS message_attachments;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_avatar_file_id_fkey;
ALTER TABLE workspaces
    DROP CONSTRAINT IF EXISTS workspaces_logo_file_id_fkey;

DROP POLICY IF EXISTS files_tenant ON files;
DROP TABLE IF EXISTS files;
-- +goose StatementEnd
