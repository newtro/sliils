# SliilS — Technical Specification

> Implementation-ready. Every component, schema, endpoint, and security decision is named here so implementing agents can work without clarification. Companion to [architecture.md](architecture.md) and [adr/](adr/).

## 1. Data Model

PostgreSQL 17. All tenant tables carry `workspace_id BIGINT NOT NULL REFERENCES workspaces(id)` and have RLS enabled with policy:
```sql
USING (workspace_id = current_setting('app.workspace_id')::bigint)
```
The app sets `SET LOCAL app.workspace_id = ...` after auth on every transaction.

### 1.1 Shared (cross-workspace) schema

```
users
  id            BIGSERIAL PK
  email         CITEXT UNIQUE NOT NULL
  password_hash TEXT       -- argon2id; null for OAuth-only
  display_name  TEXT
  avatar_file_id BIGINT FK files
  totp_secret   TEXT       -- nullable
  created_at, updated_at, deactivated_at
  -- RLS: NOT enabled (global)

oauth_identities
  id, user_id FK users, provider TEXT, provider_user_id TEXT, raw JSONB
  UNIQUE (provider, provider_user_id)

sessions
  id, user_id FK users, workspace_id FK workspaces (NULLable for cross-ws ops)
  refresh_token_hash, last_seen_at, ip, user_agent, expires_at

api_tokens
  id, user_id FK, scopes JSONB, last_used_at, expires_at, revoked_at

push_devices
  id, user_id FK, platform (ios|android|web), token TEXT, app_bundle_id, created_at
```

### 1.2 Tenant boundary

```
workspaces
  id            BIGSERIAL PK
  slug          CITEXT UNIQUE NOT NULL
  name          TEXT
  brand_color   TEXT
  logo_file_id  BIGINT FK files
  retention_days INT
  created_at, updated_at, archived_at

workspace_memberships
  id, workspace_id FK workspaces, user_id FK users
  role  TEXT CHECK (role IN ('owner','admin','member','guest'))
  joined_at, deactivated_at, custom_status JSONB
  UNIQUE (workspace_id, user_id)
  -- RLS: USING (workspace_id = ...)
```

### 1.3 Messaging

```
channels
  id, workspace_id FK
  type TEXT CHECK (type IN ('public','private','dm','group_dm'))
  name CITEXT       -- null for DMs
  topic, description, archived_at
  default_join BOOL
  UNIQUE (workspace_id, name) WHERE name IS NOT NULL

channel_memberships
  id, workspace_id, channel_id FK, user_id FK
  notify_pref TEXT CHECK (in ('all','mentions','mute'))
  last_read_message_id BIGINT
  joined_at

messages
  id BIGSERIAL PK
  workspace_id FK
  channel_id FK
  thread_root_id BIGINT NULL  -- self for thread roots, NULL for non-thread
  parent_id BIGINT NULL       -- direct parent in a thread tree
  author_user_id FK users (nullable for system messages)
  author_bot_id FK bots (nullable)
  body_md TEXT
  body_blocks JSONB           -- Block-Kit-style structured content
  edited_at, deleted_at
  created_at TIMESTAMPTZ
  PARTITION BY RANGE (created_at)   -- monthly partitions via pg_partman

message_reactions
  message_id FK, user_id FK, emoji TEXT
  PK (message_id, user_id, emoji)

message_attachments
  id, message_id FK, file_id FK files, position INT

pinned_messages
  channel_id FK, message_id FK, pinned_by FK users, pinned_at
  PK (channel_id, message_id)

bookmarks
  user_id FK, message_id FK, created_at
  PK (user_id, message_id)
```

### 1.4 Files

```
files
  id, workspace_id FK, uploader_user_id FK
  storage_key TEXT             -- key in S3 / SeaweedFS
  filename, mime, size_bytes BIGINT
  scan_status TEXT CHECK (in ('pending','clean','infected','failed'))
  derivatives JSONB           -- {thumb_64, thumb_400, thumb_1600, hls_master, audio_waveform}
  created_at, deleted_at

file_versions
  id, file_id FK, storage_key, created_by FK users, created_at
```

### 1.5 Pages (Yjs / Y-Sweet)

```
pages
  id, workspace_id FK, channel_id FK (NULLable), title, doc_id (Y-Sweet docId)
  created_by FK users, created_at, updated_at

page_snapshots
  id, page_id FK, snapshot_data BYTEA, created_at
```

### 1.6 Calendar

```
events
  id, workspace_id FK, channel_id FK (NULLable, default channel for the event)
  title, description, location_url
  start_at, end_at TIMESTAMPTZ, time_zone TEXT
  rrule TEXT                 -- RFC 5545 recurrence
  recording_enabled BOOL
  created_by FK users, created_at, updated_at, canceled_at

event_attendees
  event_id FK, user_id FK (NULLable for external email-only)
  external_email CITEXT (NULLable)
  rsvp TEXT CHECK (in ('pending','yes','no','maybe'))
  PK (event_id, user_id) WHERE user_id IS NOT NULL

external_calendars
  id, user_id FK
  provider TEXT CHECK (in ('google','microsoft','caldav'))
  oauth_refresh_token TEXT (encrypted at rest via app-managed key)
  sync_status JSONB, last_synced_at
  UNIQUE (user_id, provider)

meetings
  id, workspace_id FK, event_id FK (NULLable for ad-hoc), channel_id FK
  livekit_room TEXT
  started_at, ended_at, recording_file_id FK files
```

### 1.7 Apps & integrations

```
apps
  id, slug, owner_user_id FK
  manifest JSONB              -- scopes, redirect_uris, event_subs, slash_cmds
  client_secret_hash TEXT
  created_at

app_installations
  id, app_id FK, workspace_id FK, installed_by FK users
  scopes JSONB, bot_user_id FK bots (NULL until granted)
  created_at, revoked_at

bots
  id, workspace_id FK, app_installation_id FK, display_name, avatar_file_id
  PK (id)

webhooks_incoming
  id, workspace_id FK, channel_id FK, name, signing_secret_hash
  created_by, created_at, last_used_at, deleted_at

webhooks_outgoing
  id, app_installation_id FK, event_pattern TEXT, target_url, signing_secret_hash
  created_at, deleted_at

slash_commands
  id, app_installation_id FK, command TEXT, target_url, description, usage_hint
  UNIQUE (workspace_id, command)
```

### 1.8 Audit & ops

```
audit_log
  id, workspace_id FK (NULLable for install-level events)
  actor_user_id FK (NULLable), actor_ip, action TEXT
  target_kind, target_id, metadata JSONB, created_at

custom_emoji
  id, workspace_id FK, name TEXT, file_id FK files, created_by FK
  UNIQUE (workspace_id, name)
```

### 1.9 RLS strategy summary

| Table category | RLS policy |
|---|---|
| Cross-workspace (`users`, `sessions`, `oauth_identities`, `api_tokens`, `push_devices`) | None — app-layer authorization only |
| Tenant-bounded | `USING (workspace_id = current_setting('app.workspace_id')::bigint)` |
| `messages` (private channels / DMs) | Adds `EXISTS (SELECT 1 FROM channel_memberships ...)` to enforce visibility |

App connects as a **non-owner role** (`sliils_app`) so RLS is enforced even on app bugs. `FORCE ROW LEVEL SECURITY` on every tenant table.

---

## 2. API Design

### 2.1 Conventions

- **Base URL:** `/api/v1`
- **Auth:** `Authorization: Bearer <jwt>` for HTTP; `?token=<jwt>` query param for WS upgrade (then drop on connection)
- **Errors:** RFC 7807 Problem Details (`application/problem+json`)
- **Idempotency:** `Idempotency-Key` header on POST/PATCH; replay-safe for 24h
- **Pagination:** cursor-based: `?cursor=<opaque>&limit=50`; `Link` header with `rel=next`
- **Versioning:** path-prefix major version; minor is additive only

### 2.2 Core endpoint families

| Resource | Endpoints |
|---|---|
| Auth | `POST /auth/signup`, `/auth/login`, `/auth/oauth/{provider}/start`, `/auth/oauth/{provider}/callback`, `/auth/2fa/verify`, `/auth/refresh`, `/auth/logout`, `/auth/magic-link` |
| Me | `GET /me`, `PATCH /me`, `GET /me/workspaces`, `POST /me/preferences`, `POST /me/devices` (register push token) |
| Workspaces | `GET /workspaces/{slug}`, `POST /workspaces`, `PATCH /workspaces/{id}`, `GET /workspaces/{id}/members`, `POST /workspaces/{id}/invites`, `DELETE /workspaces/{id}/members/{user_id}` |
| Channels | `GET /workspaces/{id}/channels`, `POST /channels`, `PATCH /channels/{id}`, `POST /channels/{id}/members`, `POST /channels/{id}/leave`, `POST /channels/{id}/archive` |
| Messages | `GET /channels/{id}/messages?cursor=...`, `POST /channels/{id}/messages`, `PATCH /messages/{id}`, `DELETE /messages/{id}`, `POST /messages/{id}/reactions`, `GET /messages/{id}/thread` |
| Files | `POST /files/presign-upload`, `POST /files/{id}/finalize`, `GET /files/{id}` (presigned download), `GET /files/wopi/...` (Collabora) |
| Pages | `POST /pages`, `GET /pages/{id}` (returns Y-Sweet doc handle) |
| Search | `POST /search` (returns scoped tenant token + initial results) |
| Calendar | `GET /events?from=...&to=...`, `POST /events`, `PATCH /events/{id}`, `POST /events/{id}/rsvp`, `DELETE /events/{id}` |
| External cal | `POST /me/external-calendars/google/start`, `/callback`, `DELETE /me/external-calendars/{provider}` |
| Meetings | `POST /meetings/{id}/join` (issues LiveKit JWT), `POST /meetings/{id}/end`, `POST /meetings/{id}/record/start|stop` |
| Apps | `GET /apps`, `POST /apps`, `PATCH /apps/{id}`, `POST /apps/{id}/install/{workspace}`, `POST /apps/{id}/uninstall` |
| Bots | `POST /bots`, `POST /chat.postMessage` (Slack-API-shaped), `POST /views.open`, `POST /views.update` |
| Webhooks | `POST /workspaces/{id}/webhooks/incoming`, `GET /workspaces/{id}/webhooks/outgoing` |
| Admin | `GET /admin/audit`, `POST /admin/data/export`, `GET /admin/users`, `PATCH /admin/users/{id}` |

### 2.3 WebSocket protocol

- **URL:** `wss://host/api/v1/socket?token=<jwt>`
- **Framing:** JSON; one envelope:
  ```json
  { "v": 1, "type": "...", "id": "client-msg-id (optional)", "data": {...} }
  ```
- **Outgoing message types** (server → client):
  - `hello` (initial; capabilities, ping interval, last server event)
  - `message.created`, `message.updated`, `message.deleted`
  - `reaction.added`, `reaction.removed`
  - `presence.changed`
  - `typing.started`, `typing.stopped`
  - `channel.created`, `channel.updated`, `channel.archived`
  - `member.added`, `member.removed`
  - `event.upcoming` (calendar reminder)
  - `meeting.started`, `meeting.ended`
  - `error`
- **Incoming** (client → server):
  - `subscribe` / `unsubscribe` (channel topics; only allowed for channels the user is a member of)
  - `typing.heartbeat`
  - `presence.set` (active / away / DND)
  - `ack` (with `last_event_id` for diff-sync replay)
  - `ping`
- **Reconnect:** client provides `?since=<event_id>` on reconnect; server replays missed events from a circular buffer (5 minute retention) or returns `must_full_resync: true`.

### 2.4 WOPI (Collabora) integration

- `GET /files/wopi/{file_id}/contents` — returns binary
- `POST /files/wopi/{file_id}/contents` — accepts new version
- `GET /files/wopi/{file_id}` — file info
- All WOPI calls authenticated via short-lived signed `access_token` issued by SliilS

### 2.5 Push proxy protocol

App server → push proxy:
```
POST https://push.sliils.com/v1/notify
Authorization: Bearer <install_jwt>   # server-attested install identity
Content-Type: application/json

{
  "tenant_id":   "uuid",
  "device_token": "...",
  "platform":     "apns" | "fcm",
  "payload": {
    "msg_id":  "opaque-id",
    "type":    "mention",
    "ttl":     86400
  }
}
```
Proxy verifies install JWT, never inspects payload beyond what's needed for routing, forwards as silent push to APNs/FCM. Recipient's app fetches the actual content from the tenant server using its own auth.

---

## 3. Security Design

### 3.1 Threat model (top 10)

| # | Threat | Mitigation |
|---|---|---|
| 1 | Cross-tenant data leak via app bug | Postgres RLS + `FORCE ROW LEVEL SECURITY`; non-owner DB role; per-request `SET LOCAL app.workspace_id` |
| 2 | Private channel exposure via search | Meilisearch tenant tokens HMAC-baked with `workspace_id` AND `visible_to_user_ids` filters |
| 3 | Webhook replay / forgery | HMAC-SHA256 over body + timestamp; reject if timestamp older than 5 min; per-route signing secrets |
| 4 | OAuth refresh token theft | At-rest encryption with app-managed key (KMS-style); rotate on refresh |
| 5 | Session hijack | Short-lived JWT (15 min) + refresh token; HttpOnly + Secure + SameSite=Strict cookies |
| 6 | Brute force login | Per-user + per-IP exponential backoff; lock after 10 failures with email notice |
| 7 | XSS via message content | TipTap → server normalizes Block-Kit JSON; render via React (no `dangerouslySetInnerHTML`); strict CSP `default-src 'self'` |
| 8 | CSRF | SameSite=Strict on session cookies; CSRF token on form posts that change auth |
| 9 | File upload exploits | clamd antivirus scan; MIME sniff vs declared; max-size enforced; presigned URLs scoped per upload |
| 10 | Push proxy plaintext leak | Opaque `msg_id` only in payload; client fetches actual content from tenant server (Signal-style) |

### 3.2 Auth flow

- **Password:** argon2id, params `m=64MB, t=3, p=4`
- **JWT:** HS256 with rotating signing key (stored in app config); claims `{sub, ws, exp, scopes}`
- **2FA:** TOTP (RFC 6238); recovery codes hashed
- **OAuth:** authorization code + PKCE; redirect URIs registered per app

### 3.3 Authorization

- **RBAC at workspace level:** owner → admin → member → guest
- **Per-channel ACL** for private channels (membership table)
- **App scopes:** Slack-style (`channels:read`, `chat:write`, `users:read`, etc.)
- **Bot identity:** every bot has a workspace-scoped identity; messages attributed to `(bot_id, app_installation_id)`

### 3.4 Data protection

- **In transit:** TLS 1.3 (Caddy); HSTS preload
- **At rest:** Postgres on encrypted volume (LUKS / AWS EBS encryption); SeaweedFS volumes encrypted; OAuth refresh tokens encrypted at the app level
- **Secrets management:** env vars at v1 (Docker Compose `.env`); document path to vault integration in v1.1+

### 3.5 Rate limiting

Token bucket per (user_id, route_class) and (ip, route_class):
- Login: 5/min/IP, 10/hr/user
- Send message: 60/min/user, 600/hr/user
- Webhook incoming: 1/sec sustained, 100 burst per webhook
- Search: 10/min/user
- API in general: 1000/hr/user (overrideable per role)

In v1 the bucket lives in Postgres (LISTEN/NOTIFY for invalidation); migrate to Redis when multi-node.

---

## 4. Coding Conventions (binding for implementing agents)

- Go: `golangci-lint` config in repo; `errors.Is/As`; `slog` not log; `context.Context` first arg; no global state; functional options for constructors
- TypeScript: `strict: true`; no `any` except at integration boundaries; explicit return types on exports; ESLint config in repo
- API responses: snake_case JSON keys; ISO 8601 timestamps with TZ; error responses are RFC 7807
- Migrations: `goose` or `migrate`; one direction per file; never edit a shipped migration
- Tests: table-driven in Go; integration tests against real Postgres + Meili in CI via testcontainers; e2e UI in Playwright
