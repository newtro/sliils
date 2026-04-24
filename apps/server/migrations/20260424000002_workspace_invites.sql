-- +goose Up
-- workspace_invites: per-workspace invitation tokens for the M7 invite flow.
--
-- Design notes:
--   - One row per invite. The `token` column is the unguessable lookup key
--     that goes into the email link; clients pass it back on accept.
--   - `email` is optional so a single row can serve both link-only invites
--     (share any URL) and email-targeted invites (where the email address
--     must match at accept time). CITEXT lets us match case-insensitively
--     without re-lowering at every site.
--   - `role` is baked into the invite so the admin decides the invited
--     user's role at send time, not at accept time. Default 'member'.
--   - `expires_at` is NOT NULL so every invite has a bounded lifetime.
--   - Audit columns: who created it (`created_by`), when it was accepted
--     (`accepted_at` + `accepted_by`), and whether it was revoked
--     (`revoked_at`). `accepted_at IS NULL AND revoked_at IS NULL` is the
--     "pending" state.
--
-- Why not store the token as a hash (like session refresh tokens)?
-- Invites are short-lived, low-entropy leakage risk (seeing the token
-- only lets someone join a tenant they could already verify their email
-- against), and the accept endpoint does a user-auth step on top. Plain
-- token lookup is enough — similar to password-reset tokens elsewhere.
-- Switch to hashed storage when the threat model expands.

-- +goose StatementBegin
CREATE TABLE workspace_invites (
    id              BIGSERIAL    PRIMARY KEY,
    workspace_id    BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token           TEXT         NOT NULL UNIQUE,
    email           CITEXT,                                   -- NULL = link-only, any signed-in user can claim
    role            TEXT         NOT NULL DEFAULT 'member'
                                   CHECK (role IN ('admin','member','guest')),
    created_by      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ  NOT NULL,
    accepted_at     TIMESTAMPTZ,
    accepted_by     BIGINT       REFERENCES users(id) ON DELETE SET NULL,
    revoked_at      TIMESTAMPTZ,
    revoked_by      BIGINT       REFERENCES users(id) ON DELETE SET NULL
);

-- Lookups happen by token (accept path) and by workspace_id + pending
-- (admin list). One index each; the token uniqueness constraint doubles
-- as the point-lookup index.
CREATE INDEX idx_invites_workspace_pending
    ON workspace_invites (workspace_id, created_at DESC)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE workspace_invites ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_invites FORCE  ROW LEVEL SECURITY;

-- Admins/owners see every invite in their workspace; regular members
-- can't list invites (they're operational state). The role check happens
-- via a subquery over workspace_memberships — which is itself RLS-scoped
-- to the current user, so a non-member never matches.
CREATE POLICY invites_select ON workspace_invites
    FOR SELECT
    USING (
        workspace_id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND role IN ('owner','admin')
              AND deactivated_at IS NULL
        )
    );

-- Create / update: same admin check, plus the INSERT must be inside the
-- currently-scoped workspace so an admin can't craft rows for a workspace
-- they're not actively targeting.
CREATE POLICY invites_modify ON workspace_invites
    FOR ALL
    USING (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
        AND workspace_id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND role IN ('owner','admin')
              AND deactivated_at IS NULL
        )
    )
    WITH CHECK (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
        AND workspace_id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND role IN ('owner','admin')
              AND deactivated_at IS NULL
        )
    );

-- The accept path needs to look up an invite by its token WITHOUT knowing
-- the workspace id (that's what the invite tells you). The server handler
-- uses the owner-pool (RLS-bypassed, same pool used by search-drain) for
-- the token lookup + accept, then switches back to the tenant pool to
-- insert the membership under the proper GUC. See internal/server/invites.go.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS invites_modify ON workspace_invites;
DROP POLICY IF EXISTS invites_select ON workspace_invites;
DROP INDEX IF EXISTS idx_invites_workspace_pending;
DROP TABLE IF EXISTS workspace_invites;
-- +goose StatementEnd
