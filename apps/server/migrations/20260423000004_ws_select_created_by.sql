-- +goose Up
-- Fix the classic Postgres RLS + RETURNING interaction.
--
-- Migration 3's ws_select policy only allowed reading a workspace when the
-- current user had an active workspace_membership row. CreateWorkspace uses
-- `INSERT ... RETURNING *`, and Postgres evaluates ws_select against the
-- freshly-inserted row during RETURNING. At that instant no membership row
-- exists yet (we create it in the next statement of the same tx), so the
-- RETURNING is rejected and the whole INSERT fails with
-- "new row violates row-level security policy."
--
-- Widen the policy so the creator can always see their own workspace. The
-- security impact is minimal: the creator becomes owner immediately after
-- creation, and the only edge case (a user removed from a workspace they
-- created) is not reachable at M2 since we have no "remove member" flow.
-- We can tighten this later if needed.

-- +goose StatementBegin
DROP POLICY IF EXISTS ws_select ON workspaces;

CREATE POLICY ws_select ON workspaces
    FOR SELECT
    USING (
        created_by = NULLIF(current_setting('app.user_id', true), '')::bigint
        OR id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND deactivated_at IS NULL
        )
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS ws_select ON workspaces;

CREATE POLICY ws_select ON workspaces
    FOR SELECT
    USING (
        id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND deactivated_at IS NULL
        )
    );
-- +goose StatementEnd
