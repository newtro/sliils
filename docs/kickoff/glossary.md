# SliilS — Glossary

Project-specific vocabulary. When in doubt during implementation, this is the source of truth.

## Core entities

- **Install** — one running deployment of the SliilS server (one Docker Compose stack). Hosts many workspaces.
- **Workspace** — a tenant-isolated collaboration space (e.g., "Acme Inc"). Contains channels, members, files, settings. Identified by a slug (`acme`).
- **User** — a person account on an install. Lives at install scope, not workspace scope. One user can be a member of many workspaces on the same install.
- **Workspace membership** — a (user × workspace) row with a role (owner, admin, member, guest) and per-workspace settings (notification prefs, custom status).
- **Channel** — a conversation surface within a workspace. Types: `public`, `private`, `dm` (1:1 direct message), `group_dm` (≤9 person group DM).
- **DM** / **group DM** — direct messages between specific users; lives in the `channels` table with `type='dm'` or `'group_dm'`.
- **Thread** — a child message conversation rooted at a parent message. Stored as messages with `thread_root_id` and `parent_id`.
- **Message** — a single chat message. Carries Block-Kit-style structured content in `body_blocks` plus a markdown fallback in `body_md`.
- **Reaction** — an emoji response to a message; (`message_id`, `user_id`, `emoji`) primary key.
- **File** — an uploaded artifact (image, video, audio, PDF, Office doc) with derivatives (thumbnails, preview formats). Stored in object storage; metadata in Postgres.
- **Page** — a native first-class collaborative document, edited with TipTap, synced with Yjs via Y-Sweet. Distinct from an uploaded Office doc edited in Collabora.
- **Event** — a calendar event; can be tied to a channel, has invitees, optional recurrence (RFC 5545 RRULE), optional LiveKit meeting room.
- **Meeting** — an active or recorded LiveKit room session. May be tied to an event or be ad-hoc from a channel.
- **App** — a 3rd-party integration registered with SliilS. Has scopes, OAuth credentials, manifest. Installed into a workspace creates an `app_installation` and optional `bot`.
- **Bot** — a workspace-scoped programmatic account, owned by an app installation. Can post messages, react, etc.
- **Webhook (incoming)** — a per-channel HTTPS endpoint that accepts `POST` to add a message; secured by HMAC.
- **Webhook (outgoing)** — an HTTPS endpoint registered by an app to receive event subscriptions; payload is HMAC-signed.

## Architecture & infrastructure

- **App server** — the single Go binary that handles HTTP API + WebSocket gateway + worker. Runs as `--mode=all` on the v1 single-VM deploy.
- **Worker** — background job processor (River, Postgres-backed); runs media pipeline, search indexing, calendar sync, push fan-out.
- **WS gateway** — the WebSocket sub-component of the app server; multiplexes per-workspace topics over one connection per client.
- **Push proxy** — a separate small Go service operated by the SliilS project at `push.sliils.com`. Holds APNs/FCM credentials bound to the project's signed mobile app bundles. Receives signed wake-up requests from any tenant install and forwards opaque pings to APNs/FCM.
- **Tenant** — same as install for hosted scenarios; sometimes used to disambiguate from the SliilS project itself (which operates the push proxy).
- **SFU** — Selective Forwarding Unit (LiveKit). The media server that routes audio/video tracks between meeting participants.
- **TURN** — Traversal Using Relays around NAT. Coturn server for WebRTC media relay when direct peer connections fail.
- **WOPI** — Web Application Open Platform Interface; the protocol Collabora uses to fetch/save Office documents from SliilS.
- **Y-Sweet** — Open-source Yjs sync server; persists collaborative document state for native pages.
- **Tenant token** (Meilisearch) — a short-lived JWT issued per session that HMAC-bakes the user's permitted workspace + visible channels into a filter the user cannot override.

## Multi-tenancy

- **Tenant boundary** — the workspace; every tenant-scoped table carries `workspace_id`.
- **RLS** — Postgres Row-Level Security; enforces per-row tenant isolation in the database engine, using `current_setting('app.workspace_id')`.
- **Hybrid multi-tenancy** — shared metadata DB (users, workspaces, sessions) + RLS on per-tenant data tables. The pattern that supports Slack-style identity-spans-workspaces.
- **Identity-spans-workspaces** — one user account can be a member of many workspaces on the same install; the workspace switcher (left rail) is how the user navigates.

## UI & UX

- **Shell** — the persistent application chrome (workspace rail + channel pane + main content + slide-in details).
- **Composer** — the message input area (TipTap editor) at the bottom of the channel view.
- **Block Kit** — Slack's JSON UI framework, cloned in shape for SliilS. Messages and modals are JSON arrays of blocks (`section`, `actions`, `input`, `divider`, `image`, `context`, `modal`, `carousel`, `card`).
- **Slash command** — a message starting with `/` that triggers a command (built-in like `/topic` or registered by an app like `/giphy`).
- **Mention** — `@user`, `@channel`, `@here`, `@everyone`; or `#channel-name` reference.
- **Workspace switcher** — the left 48px rail of stacked workspace icons; clicking switches the current workspace context.
- **Slide-in details** — the 320px right panel that hosts the thread view, channel members, file details, etc.

## Roles & permissions

- **Owner** — workspace creator or transferred-to user; can do everything including delete the workspace.
- **Admin** — promoted member; can manage members, channels, retention, audit, exports.
- **Member** — regular user.
- **Guest** — limited-access user (channel-restricted); used for cross-org guest invites in v1.1+; not in v1 scope.
- **Bot** — programmatic account; not a person.

## Operational terms

- **Milestone (M0–M12)** — a demo-able implementation unit; see [implementation-roadmap.md](implementation-roadmap.md).
- **Acceptance criteria** — the conditions a milestone must meet before it's "done."
- **ADR** — Architecture Decision Record; see [adr/](adr/).
- **Decision Log** — the consolidated table in `.claude/kickoffs/teams-slack-clone/index.md` (working notes); each ADR maps to one entry.
- **Living spec mode** — post-launch state where the kickoff documents are kept current as the codebase evolves; `/kickoff` can be re-invoked to update specific sections.

## Brand

- **SliilS** — pronounced "slice"; the name and the wordmark. The two interior letters of "SliilS" are stylized as a green person + blue person, with green/teal/blue speech sparks above.
- **Navy / green / blue / teal** — brand palette. Navy (`#1A2D43`) for primary text and dark theme; green/blue/teal for accents.
