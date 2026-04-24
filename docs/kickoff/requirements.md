# SliilS — Requirements (PRD)

## Product identity
- **Name:** SliilS (pronounced "slice")
- **Domain:** sliils.com
- **Wordmark:** "SliilS" with the two interior letters stylized as a green and blue person beneath green/teal/blue speech sparks
- **Brand assets:**
  - Logo: `original-assests/sliis-logo.png`
  - Favicon: `original-assests/favicon.png`
- **Color palette:** navy primary (wordmark) · green + blue + teal accents (people + sparks)
- **Brand tone:** friendly, inclusive, communication-first, accessible

## Product positioning
A self-hosted, multi-tenant, MIT/Apache-licensed team collaboration platform combining the best of Slack and Microsoft Teams, designed for small teams (2–50 users per workspace) whose primary motivation is eliminating the per-seat SaaS subscription tax. Slack-anchored UX, Teams-equivalent feature surface area.

## Scale target (v1)
- **Per install:** ~100 users, ~5 workspaces, ~1M messages — single AWS VM with PostgreSQL installed locally
- **Architecture must run comfortably on:** 2 vCPU + 4 GB RAM single-VM
- **Architectural ceiling:** scale-out to medium (1k users / 50 workspaces / 100M msgs) via config + extra processes, no rewrites

---

## v1 Feature Catalog (MUST-HAVE)

### Messaging
- Public channels, private channels
- Direct messages: 1:1 and group (≤9 participants)
- Threads (per-message threading sidebar)
- Mentions: `@user`, `@channel`, `@here`, `@everyone`
- Channel mentions: `#channel-name`
- Emoji reactions (Unicode + custom workspace emoji)
- Message edit + delete (with edit history)
- Pin messages
- Save / bookmark items
- Drafts (per-channel auto-saved)
- Read state / unread counts (per-channel + per-thread)
- Typing indicators
- Presence: online / away / offline (auto + manual)
- Custom status (text + emoji + expiry)
- Do Not Disturb / quiet hours

### Rich content
- Markdown formatting (bold, italic, strikethrough, code, blockquote, lists, links)
- Fenced code blocks with syntax highlighting
- File attachments (image, video, audio, PDF, Office) with inline previews
- Link unfurls (OpenGraph)
- Custom workspace emoji
- Built-in slash commands (`/me`, `/topic`, `/invite`, `/leave`, etc.)

### Channels & workspaces
- Channel sections / sidebar grouping
- Channel topic + description
- Channel browse view
- Archive channel
- Default channels (auto-join for new members)
- Multi-workspace per user (Slack-style switcher in left rail)
- Workspace owners + admins + members (RBAC)
- Workspace invites: invite link, email invite
- Member directory
- Workspace branding: logo + accent color
- Light + dark theme

### Search
- Global message search across user-visible content
- Slack-style operators: `from:`, `in:`, `before:`, `after:`, `has:link`, `has:file`, `is:pinned`
- File search (filename, type, uploader)
- People search (name, email, role)
- Powered by Meilisearch with tenant tokens

### Voice / Video (Teams-parity)
- 1:1 voice calls
- 1:1 video calls
- Group voice/video calls (≤25 participants)
- Group video meetings (≤50 participants)
- Screen sharing (full screen, app window, browser tab)
- Virtual backgrounds + blur
- Noise suppression
- Server-side recording (output to S3-compatible storage)
- In-call chat
- Mute / camera off / hand-raise / participant list
- Powered by LiveKit + Coturn

### File collaboration
- Office docs (Word/Excel/PPT) embedded editing via Collabora Online
- Native pages / canvas via Yjs + TipTap + Y-Sweet
- Document comments
- Document version history
- File preview (image, video, audio, PDF inline)

### Calendar
- Native first-class events: title, description, time, time zone, recurrence, invitees, RSVP
- Schedule meetings from a channel composer (`/meet`, calendar sidebar)
- Meeting links auto-launch a LiveKit room scoped to the event
- Two-way sync with Google Calendar
- Two-way sync with Microsoft Outlook
- Free/busy lookup for invitees
- iCal export per user / per channel
- Reminders (in-app + push)

### Notifications
- Web push (VAPID)
- Desktop native notifications (Tauri + native APIs)
- Mobile push (via project-run open-source push proxy → APNs/FCM)
- Per-channel notification preferences (all / mentions / mute)
- @mention triggers
- Notification grouping (room-level)
- DND respect
- Signal-style opaque payloads (proxy never sees plaintext)

### Integrations / extensibility platform
- Incoming webhooks
- Outgoing webhooks
- Slash commands (3rd-party)
- Bot users (first-class account type with API tokens)
- OAuth 2.0 apps (PKCE) with per-scope grants
- Block-Kit-style JSON UI: `section`, `actions`, `input`, `divider`, `image`, `context`, `modal`, `carousel`, `card`
- HMAC-signed event subscriptions
- Manifest-based app install (YAML/JSON)
- *(Marketplace browseable UI deferred to v1.1)*

### Auth & identity
- Email + password (with verification + password reset)
- OAuth 2.0: Google, GitHub, Apple
- 2FA (TOTP)
- Session management (active sessions list, revoke)
- API tokens (per-user, per-bot, scoped)
- Email magic-link login (optional)
- *(SSO / SAML / OIDC deferred)*

### Admin & moderation
- User management: invite, deactivate, role change, transfer ownership
- Channel moderation: archive, mute, kick, ban
- Message retention policies (per-channel, per-workspace)
- Audit log (admin actions, auth events)
- Workspace data export (JSON / archive)
- Per-user data export (GDPR)
- Rate limiting (per-user, per-IP, per-workspace)
- Content moderation hooks (slur filter pluggable)

### Storage
- File storage abstraction (`IStorage` interface)
- Drivers: local disk, S3-compatible (covers SeaweedFS, Garage, R2, B2, Wasabi)
- SeaweedFS (Apache 2.0) recommended self-hosted default
- Presigned URLs for upload/download (never proxy bytes through app)
- Media pipeline in worker queue: libvips images, ffmpeg video/audio, clamd antivirus, EXIF strip on ingest

---

## Non-Functional Requirements (v1)

### Performance targets
- Message send → visible to room members: **p95 < 200ms** local, **< 500ms** cross-region
- Message search query → first result: **p95 < 300ms**
- Cold app load: **< 2s** to interactive on broadband
- Channel switch: **< 100ms** to first paint (cached)
- Voice/video call setup: **< 3s** to connected
- Concurrent WebSocket connections per node: **≥ 10k** sustained

### Availability
- Single-VM v1 deploy: best-effort, no HA promises
- Architecture must allow HA in v1.1+ (stateless app processes, Postgres replication path)

### Security
- All transport over TLS (HTTPS, WSS)
- Encryption at rest for Postgres (filesystem-level, not E2EE)
- HMAC signing for webhooks and integration events
- CSRF tokens, SameSite cookies, secure session cookies
- Argon2id for password hashing
- Rate limiting at the API gateway and per-route
- Audit log for sensitive admin actions
- OWASP Top 10 baseline
- Dependency scanning (Renovate / Dependabot)

### Browser support
- Last 2 stable versions of Chrome, Firefox, Safari, Edge
- iOS Safari + Android Chrome equivalent for PWA install
- Graceful degradation message on older browsers

### Accessibility
- WCAG 2.1 AA target
- Full keyboard navigation
- ARIA roles + live regions for chat updates
- Screen reader tested (NVDA, VoiceOver)
- High-contrast theme variant
- Reduced-motion respect (no auto-animations when `prefers-reduced-motion`)

### Internationalization
- English-only at v1
- All user-visible strings externalized (i18n infrastructure with `react-i18next` or `lingui`) so translations can be added without refactoring
- Date/time formatting via `Intl` API; user-configurable time zone

### Observability
- Structured logging (zap / zerolog in Go)
- Prometheus metrics endpoint
- Health + readiness endpoints
- OpenTelemetry tracing (optional, off by default)

### Operations
- Single Docker Compose deploys the full stack: app + Postgres + LiveKit + Coturn + SeaweedFS + Meilisearch + Collabora + Y-Sweet + worker
- Single-binary Go server (no daemon ecosystem)
- Database migrations versioned and runnable on startup or via CLI
- Backup/restore tooling (pg_dump + S3 sync)
- Auto-update checks (opt-in)

### Push proxy operational requirement
- Project must operate a free hosted push proxy (MIT-licensed, open source) to enable mobile push for any tenant using the project-signed mobile apps
- Tenants who want full self-hosting must re-sign mobile apps with their own bundle IDs and run their own proxy

---

## Explicitly OUT OF SCOPE for v1

| Excluded | Why / when |
|---|---|
| End-to-end encryption (any flavor) | Discovery decision — TLS only enables search/bots/integrations |
| Federation / cross-install bridges | Discovery decision — standalone only |
| Slack Connect-style shared channels | Discovery decision; revisit in v2 |
| SSO / SAML / OIDC | Deferred — Discovery decision; add in v1.1+ when enterprise demand emerges |
| Slack Workflows (no-code automation builder) | v1.1+ |
| Slack Lists (spreadsheet-style) | v1.1+ |
| Slack Huddles (always-on voice rooms) | v1.1+ — group calls cover v1 needs |
| Loom-style video clips / async audio clips | v1.1+ |
| AI features (channel summarize, daily recap) | v1.1+ — LiveKit Agents framework available later |
| App marketplace browseable UI | v1.1 — apps installable via manifest in v1, just no marketplace UI |
| Compliance certifications (HIPAA, SOC 2, FedRAMP) | Process work, not features; pursue when revenue/customer demand justifies |
| Internationalization (translations) | v1.1+ — infrastructure ready, no shipped translations |
| High availability / multi-node | v1.1+ — architecture designed to allow it; v1 ships single-node |
| Native mobile-only features (Live Activities, Critical Alerts) | Push research — entitlement bar too high |
| Cal.com integration | Decided against — native calendar instead |
| MinIO support | MinIO CE archived Feb 2026 — SeaweedFS is the path |

---

## Open Questions (resolved during implementation)

1. ~~**Branding / project name**~~ — **RESOLVED:** SliilS (pronounced "slice"), domain sliils.com.
2. **Hosted push proxy domain / governance** — `push.sliils.com` planned; funding model TBD (project sponsorship vs paid SLA tier)
3. **Free-tier hosted offering?** — defer; no managed cloud in v1
4. **Custom-emoji storage** — same `IStorage` abstraction as files (decide per-blob optimization later)
5. **Workspace deletion semantics** — soft-delete with 30-day reaper (default; configurable)
6. **Bot rate limits** — separate, more permissive than user rate limits (TBD specifics in M12)
7. **Admin invite flow** — both invite link AND email invite supported; admin chooses per invitation

---

## Assumptions (with validation gates)

See [assumptions.md](assumptions.md) for the full list with validation milestones.
