-- +goose Up
-- M7C: workspace-level default notification preference.
--
-- Existing: channel_memberships.notify_pref sets per-channel overrides
-- (all | mentions | mute). That's great for muting #random while still
-- getting pings from #oncall. But we also want a workspace-wide default
-- so muting the whole of WorkspaceA (e.g. a client's workspace on the
-- weekend) is a single action — not one per channel.
--
-- The resolution order at notification time is:
--
--   channel override (if set) ▶ workspace default ▶ 'all'
--
-- which is standard Slack/Teams semantics.

-- +goose StatementBegin
ALTER TABLE workspace_memberships
    ADD COLUMN notify_pref TEXT NOT NULL DEFAULT 'all'
        CHECK (notify_pref IN ('all','mentions','mute'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workspace_memberships DROP COLUMN IF EXISTS notify_pref;
-- +goose StatementEnd
