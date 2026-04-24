# Database schema

The authoritative source is the set of migrations under `apps/server/migrations/`. This page summarises tables grouped by feature area.

## Conventions

- **Primary keys:** `BIGSERIAL` for in-app ids, opaque tokens for externally-shared ids
- **Tenant scoping:** every tenant-scoped table has `FORCE ROW LEVEL SECURITY` + a policy that reads `current_setting('app.workspace_id')`
- **Timestamps:** `TIMESTAMPTZ` everywhere. Soft-delete via `deleted_at` or `archived_at`
- **Cascades:** workspace-scoped rows `ON DELETE CASCADE` from their workspace so workspace delete is one UPDATE away from a clean tombstone

## Auth + users

- `users`, `sessions`, `auth_tokens`, `oauth_identities`, `audit_log` (from `20260423000001_init_auth.sql`)
- `is_bot` + `bot_app_installation_id` columns added in M12 for synthetic bot users
- `dnd_enabled_until` + `quiet_hours_*` columns added in M11

## Workspaces + channels

- `workspaces` (slug, name, brand_color, logo_file_id, retention_days, archived_at)
- `workspace_memberships` (role enum, custom_status JSONB, notify_pref, joined_at, deactivated_at)
- `channels` (type ∈ public/private/dm/group_dm)
- `channel_memberships` (last_read_message_id, muted_until, notify_pref)

## Messages + files

- `messages` — monthly range-partitioned. Base table PK is `(id, created_at)` to satisfy partition constraints
- `mentions`, `reactions`
- `files` (storage_backend, storage_key, sha256, scan_status, derivatives JSONB)
- `message_attachments`

## Search

- `search_outbox` — transactional outbox drained into Meilisearch. Row per event; River picks them up via FOR UPDATE SKIP LOCKED

## Calendar + meetings

- `events` (RRULE text, external_provider/id/etag for Google sync dedupe)
- `event_attendees` (user_id XOR external_email — CHECK constraint enforces exclusivity)
- `external_calendars` (oauth_refresh_token encrypted via internal/secretbox)
- `meetings`, `dm_pairs` (canonical (LEAST, GREATEST) unique index for find-or-create DMs)

## Pages

- `pages` (title, doc_id, icon, archived_at)
- `page_snapshots` (snapshot_data BYTEA, reason ∈ periodic/manual/restore)
- `page_comments` (anchor TEXT for editor RelativePosition, parent_id for threads)

## Push

- `user_devices` (platform ∈ web/tauri/apns/fcm/unifiedpush, endpoint, p256dh, auth_secret, disabled_at)

## Apps platform (M12)

- `apps` (install-global — NOT workspace-scoped)
- `app_installations`, `app_oauth_codes` (single-use, 10-min TTL), `app_tokens` (SHA-256 hashed)
- `webhooks_incoming`, `webhooks_outgoing`, `slash_commands` — all workspace-scoped with RLS

## RLS policy pattern

```sql
ALTER TABLE foo ENABLE ROW LEVEL SECURITY;
ALTER TABLE foo FORCE  ROW LEVEL SECURITY;

CREATE POLICY foo_tenant ON foo
    FOR ALL
    USING      (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint)
    WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::bigint);
```

The `FORCE` clause ensures the owner role doesn't bypass RLS; only the `sliils_app` runtime role is subject to policies by default, but `FORCE` applies them to every role including owners. Workers that need cross-tenant access connect as the original DSN owner via `db.OpenOwner`.
