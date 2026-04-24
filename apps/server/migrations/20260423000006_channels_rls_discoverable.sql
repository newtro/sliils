-- +goose Up
-- Split the channels RLS policy so SELECTs don't require app.workspace_id.
--
-- Before: FOR ALL policy checked workspace_id = app.workspace_id on every
-- command. That made "look up a channel by id to discover which workspace
-- it belongs to" impossible without already knowing the workspace — a
-- chicken-and-egg for /messages handlers where the URL only carries a
-- channel_id.
--
-- After:
--   SELECT — visible in any workspace the current user is a member of.
--   INSERT / UPDATE / DELETE — still scoped to app.workspace_id so mutations
--                              are never accidentally cross-workspace.
--
-- Same data model as before; only the policy surface changes.

-- +goose StatementBegin
DROP POLICY IF EXISTS ch_all ON channels;

CREATE POLICY ch_select ON channels
    FOR SELECT
    USING (
        workspace_id IN (
            SELECT workspace_id FROM workspace_memberships
            WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::bigint
              AND deactivated_at IS NULL
        )
    );

CREATE POLICY ch_insert ON channels
    FOR INSERT
    WITH CHECK (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

CREATE POLICY ch_update ON channels
    FOR UPDATE
    USING (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    )
    WITH CHECK (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );

CREATE POLICY ch_delete ON channels
    FOR DELETE
    USING (
        workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS ch_delete ON channels;
DROP POLICY IF EXISTS ch_update ON channels;
DROP POLICY IF EXISTS ch_insert ON channels;
DROP POLICY IF EXISTS ch_select ON channels;

CREATE POLICY ch_all ON channels
    FOR ALL
    USING  (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
-- +goose StatementEnd
