-- +goose Up
-- Super-admin flag on users.
--
-- A super-admin is the operator of the SliilS install itself. They can
-- edit install_settings (signup policy, the default email provider,
-- infrastructure endpoints) — which a workspace owner can't, even if
-- they're an owner of every workspace on the install.
--
-- Seeding:
--   - The first-run wizard flips is_super_admin=true on the first user
--     it creates.
--   - An existing install (upgrade path) can set the flag via a server
--     flag: `sliils-app promote-super-admin <email>` (added in cmd/).
--
-- Why a boolean column instead of a separate super_admins table?
--   One flag on an existing row, no join needed for gating middleware.
--   The multi-row design is a v1.1 concern if we ever need per-area
--   super-admin scopes.

-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN is_super_admin BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX idx_users_super_admin
    ON users (id)
    WHERE is_super_admin = true;
-- +goose StatementEnd

-- +goose StatementBegin
-- If the install is already populated (has any users) we assume the
-- operator is self-hosting and promote the earliest-created active
-- user to super-admin automatically. This is safe because:
--   1) a fresh install has zero users, so this is a no-op there;
--   2) for an upgrade-from-older install, the first user IS the
--      operator — every existing install at this point has one human
--      who ran the installer.
-- If that's wrong for your deployment, demote via the CLI tool.
UPDATE users
SET    is_super_admin = true
WHERE  id = (
    SELECT id FROM users
    WHERE deactivated_at IS NULL
    ORDER BY id ASC
    LIMIT 1
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_super_admin;
ALTER TABLE users DROP COLUMN IF EXISTS is_super_admin;
-- +goose StatementEnd
