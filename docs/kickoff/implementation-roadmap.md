# SliilS — Implementation Roadmap

> Roadmap structured around **demo-able milestones** (each ends in something you can show on screen). Ships v1 = full Microsoft Teams parity (chat + meetings + calendar + file collab + admin platform).

## Milestone Sequence (M0 → M12)

Each milestone has: **Demo** (what you show), **In** (what's built), **Out** (explicitly deferred), **Acceptance criteria**.

### M0 — Repo bootstrap & dev loop
- **Demo:** `docker compose up`, see "SliilS is running" health page; `pnpm dev` boots the web app showing a placeholder shell at the SliilS brand.
- **In:** monorepo (pnpm workspaces); `apps/server` (Go), `apps/web` (Vite+React), `apps/desktop` (Tauri scaffold), `apps/mobile` (Expo scaffold), `packages/ui` (Tamagui primitives, shadcn copy), `packages/api` (TS types from OpenAPI), `packages/schemas` (Zod). CI (GitHub Actions): build + lint + test all packages. Caddy + Postgres + Meilisearch in compose.
- **Out:** any business logic
- **Acceptance:** `pnpm test` passes; CI green; web shell renders SliilS logo at `/`

### M1 — Auth & first user
- **Demo:** sign up with email+password, log in, hit /me, see your profile. Logout works. Bad credentials are rate-limited.
- **In:** users + sessions + oauth_identities tables (migrations); argon2id; JWT issuance; `/auth/signup`, `/auth/login`, `/auth/logout`, `/auth/refresh`, `/me`; CSRF + SameSite cookie config; Postgres logical migration tool (goose); audit_log basics
- **Out:** OAuth providers, 2FA, magic link
- **Acceptance:** integration test for full signup→login→logout; rate-limit test; password reset flow works via email

### M2 — Workspace + first channel
- **Demo:** sign up, get redirected to /setup wizard, create workspace, land in #general.
- **In:** workspaces + workspace_memberships tables with RLS; `/setup` wizard flow; `/workspaces`, `/workspaces/{slug}`; default-channel creation; workspace switcher UI in left rail (single workspace state); Postgres `app.workspace_id` GUC plumbed end-to-end on every request
- **Out:** invites, multi-workspace, channel settings beyond name/topic
- **Acceptance:** RLS verified by manual cross-workspace probe (logged in as user A, confirm cannot read workspace B even via crafted requests)

### M3 — Realtime messaging core
- **Demo:** open two browser windows logged in as different users, send message in #general from one, instantly appears in the other. React, edit, delete work.
- **In:** messages + message_reactions tables (with monthly partitions); `POST /channels/{id}/messages`, `PATCH`, `DELETE`; `POST /messages/{id}/reactions`; WebSocket gateway with `{v,type,id,data}` envelope; `subscribe`/`unsubscribe`; `message.created/.updated/.deleted` push; in-process pub/sub fan-out; client-side TanStack Query cache + Virtuoso list + optimistic-send + IndexedDB outbox + reconnect with `since=<event_id>`; TipTap composer (plain text only)
- **Out:** mentions, threads, file attachments, slash commands
- **Acceptance:** soak test — 2 clients, 10k msg sent, no drops, reconnect mid-stream replays cleanly

### M4 — Mentions, threads, presence, typing
- **Demo:** @-mention triggers a notification; reply in thread opens slide-in panel with proper count; presence dots reflect reality; typing indicators appear and disappear correctly.
- **In:** mentions parsing (frontend + server); thread_root_id + parent_id semantics; thread sidebar UI; presence service (in-memory, per workspace); typing.heartbeat + typing.started/stopped events; channel_memberships.last_read_message_id tracking; unread badges
- **Out:** push notifications, search
- **Acceptance:** thread roundtrip in <200ms locally; presence updates within 5s of last activity; typing indicators don't false-fire on edits

### M5 — Files + media pipeline
- **Demo:** drag-drop an image into composer, it uploads, shows inline preview. Drop a Word doc, see the file card. Click to download via presigned URL. Antivirus blocks an EICAR test file.
- **In:** `IStorage` interface + `LocalStorage` + `S3Storage` drivers; SeaweedFS in compose; `POST /files/presign-upload`; clamd in compose; River worker queue; libvips/sharp + ffmpeg pipeline (thumbs, audio waveform, video preview); message_attachments; EXIF strip on ingest; basic file picker UI
- **Out:** Office co-edit, Yjs pages, recording uploads
- **Acceptance:** EICAR file is quarantined; image gets 3 derivatives; large video transcodes to inline-playable preview; race-condition test on simultaneous uploads same name

### M6 — Search
- **Demo:** cmd+K opens search; type "from:@alice has:link in:#design"; results highlight matches; click jumps to message.
- **In:** Meilisearch in compose; transactional outbox in Postgres; River worker drains outbox to Meili; tenant-token issuance per session (HMAC-baked workspace + visible_to filters); `POST /search`; cmd+K UI overlay; operator parser; result hydration with RLS double-check
- **Out:** semantic search (pgvector — v1.1)
- **Acceptance:** private channel msgs not returned to non-members; deleted messages purged from index within 60s; soak test indexes 100k msgs without lag

### M7 — Multi-workspace identity (Slack-style switcher)
- **Demo:** sign in as sesmith2k@gmail.com; left rail shows multiple workspace icons; click another workspace; channel list and content swap; per-workspace presence + custom status persists.
- **In:** `GET /me/workspaces`; per-session current-workspace context; workspace switcher UI in 48px rail; per-workspace notification preferences; per-workspace status emoji; invite flow (link + email) so a single user can be added across many workspaces
- **Out:** federation (out of scope); guest accounts (deferred)
- **Acceptance:** RLS still bulletproof when current workspace changes mid-session; switching is <200ms with cached channels

### M8 — Voice/video calls (LiveKit + Coturn)
- **Demo:** in a DM, click call icon, other party gets ringing notification, both join 1:1 video call, screen share works, virtual background blur applied. Hang up posts a "Call ended, 7m12s" message into the DM.
- **In:** LiveKit + Coturn in compose; `POST /meetings/{id}/join` issuing JWTs; in-call UI (video tiles, controls, screen share, blur via @livekit/track-processors); 1:1 + group calls (≤25); `meetings` table; LiveKit Egress for opt-in recording → SeaweedFS
- **Out:** native calendar (separate milestone), transcripts, recording transcripts
- **Acceptance:** 5-person test call sustains 30 min without drops on consumer broadband; recording uploads + plays back from chat; mobile network reconnect works

### M9 — Calendar + Google/Outlook sync
- **Demo:** type `/meet` in #channel, schedule a 30-min meeting with @alice, see it on your calendar AND your Google Calendar; event reminder fires; click "Join" launches the LiveKit room.
- **In:** events + event_attendees + external_calendars tables; `/events` CRUD; calendar UI (week + day views); `/me/external-calendars/google/start` OAuth flow + refresh-token encryption; two-way sync worker (River jobs); Microsoft Outlook same flow; `event.upcoming` reminder events; meeting links auto-launch LiveKit rooms; iCal export
- **Out:** CalDAV (defer to v1.1)
- **Acceptance:** event created in SliilS appears in Google within 60s; updating in Google appears in SliilS within 60s; declined RSVP propagates; recurring events handled correctly

### M10 — Office docs (Collabora) + native pages (Yjs)
- **Demo:** upload a .docx, click "Open in Collabora," edit; second user joins same doc, see live cursors. Create a SliilS Page (`/page foo`), edit with TipTap, see another user's edits live.
- **In:** Collabora + Y-Sweet in compose; WOPI endpoints with signed access tokens; pages + page_snapshots; native Pages UI (TipTap + Yjs binding); doc comments; version history view
- **Out:** PowerPoint/Excel co-edit advanced features (delegated to Collabora); page templates
- **Acceptance:** 3-user concurrent doc edit produces consistent final state; WOPI token expiry blocks subsequent fetches cleanly

### M11 — Notifications + push (web + desktop + push proxy)
- **Demo:** mention @alice in a channel; alice (signed in on web) sees a desktop notification; alice (logged out, mobile app) gets a push that wakes the app and shows the message; per-channel mute respected.
- **In:** VAPID web push; service worker; per-channel notification preferences UI; DND/quiet-hours; Tauri native notifications; Expo Notifications client integration; push proxy reference implementation (separate small Go service in its own repo: `sliils-push-proxy`); `/me/devices` (register tokens); Signal-style opaque payload protocol; UnifiedPush optional secondary registration
- **Out:** Live Activities, Critical Alerts (out of scope)
- **Acceptance:** push delivered in <5s p95 from mention to lock-screen; opaque payload audited (no plaintext on the wire to APNs/FCM); muted channel suppresses push at the server side

### M12 — Apps platform + admin + GA polish
- **Demo:** workspace admin creates an Incoming Webhook, posts a JSON message from `curl`, sees it in #general with bot icon. A 3rd-party developer registers an OAuth app, installs it into a workspace, the bot user posts via `chat.postMessage`. Admin views audit log, runs a workspace data export.
- **In:** apps + app_installations + bots + webhooks_incoming + webhooks_outgoing + slash_commands + Block-Kit JSON renderer; OAuth 2.0 + PKCE flow; HMAC-signed events; dev portal UI (`/dev/apps`); admin dashboard (members, roles, retention, audit log, data export, branding); GDPR per-user export; full WCAG 2.1 AA pass; dark theme; i18n string externalization done across all surfaces; documentation site
- **Out:** marketplace browseable UI (v1.1); all expansion ideas
- **Acceptance:** sample app from documentation works end-to-end; admin exports complete and re-importable into a fresh install; Lighthouse a11y ≥ 95; security review checklist signed off

---

## Dependency Graph

```
M0 ── M1 ── M2 ── M3 ┬── M4 ── M11
                     │
                     ├── M5 ── M10 (Collabora needs files)
                     │
                     ├── M6 (search needs messages + RLS)
                     │
                     └── M7 ── M8 ── M9 (calendar uses meetings)
                                       │
                                       └── M11 (push respects events)

M12 polishes everything; touches every prior milestone
```

Critical path: **M0 → M1 → M2 → M3 → M4 → M11 → M12** is the minimum viable demo path.
M5–M10 can be built in parallel once M3 lands; ordering is the recommended priority sequence.

---

## Testing Strategy

| Layer | Tool | Target |
|---|---|---|
| Go unit | `go test`, table-driven | ≥ 80% coverage on `internal/`; 100% on auth + RLS plumbing |
| Go integration | `testcontainers-go` (real Postgres + Meili + clamd) | Every API endpoint, every WS event type |
| TS unit | Vitest | ≥ 70% on `packages/ui`, hooks |
| TS integration | Vitest + MSW | API client, store mutations, WS reconnect logic |
| E2E web | Playwright | Golden flows from [workflows.md](workflows.md) (A, B, C, D, E, F, G, K) |
| E2E mobile | Maestro / Detox | Workflows C (messaging), J (calls), H (push) on iOS + Android sims |
| Load | K6 (HTTP+WS) + custom LiveKit bots | M3 soak (10k msgs), M6 soak (100k indexed), M8 (5-person 30-min call) |
| Security | gosec, npm audit, Snyk; OWASP ZAP smoke; manual RLS bypass attempts | Pre-each-milestone gate |
| A11y | axe-core in Playwright; manual NVDA/VoiceOver pass | M12 sign-off |
| Visual regression | Playwright + Percy/Chromatic | Per-PR on key screens |

### Coverage gates
- M3 onward: every PR must pass unit + integration tests
- M6 onward: every PR must pass cross-tenant RLS bypass probe in CI
- M11 onward: every PR must pass mobile push proxy contract test

---

## Deployment & DevOps

### Environments
- **Dev:** developer laptop, `docker compose -f docker-compose.dev.yml up` with hot reload
- **Staging:** single AWS VM mirroring production reference deploy; auto-deploys from `main`
- **Production reference (sliils.com):** single AWS VM; tagged-release deploys; nightly backup to S3
- **Push proxy production:** dedicated small VM at `push.sliils.com`; separate from main install

### CI/CD
- GitHub Actions on every PR: lint + test + build all binaries + Docker images
- On `main` merge: push images to GHCR with `:main` tag; staging auto-deploys
- On tagged release: push `:v<x.y.z>` and `:latest` images; release notes auto-generated; documentation site rebuilds

### Release cadence
- **Pre-v1:** weekly demo cuts to staging; no public releases
- **v1 candidate:** hardening period after M12 — security review, accessibility pass, docs polish, end-to-end smoke
- **Post-v1:** monthly minor releases; emergency patch releases as needed

### Monitoring & observability
- Prometheus metrics scraped from app + LiveKit + Postgres exporter + node-exporter
- Grafana dashboards: chat throughput, WS connections, search QPS, call quality (LiveKit), push success rate, error rate, p95 latencies
- Loki for log aggregation (Caddy + app + worker)
- Alert rules: error rate > 1%, push proxy success < 95%, LiveKit packet loss > 5%, Postgres connection exhaustion, disk > 80%

### Rollback strategy
- Database migrations: only ever forward; reversible via known-good restore from nightly + WAL archive
- App version rollback: `docker compose pull <previous-tag> && docker compose up -d`
- Feature flags (in-process): every M5+ feature gated by a flag for quick disable without rollback

---

## Definition of Done for v1

- All 12 milestones complete and acceptance criteria met
- Documentation site live (install guide, admin guide, user guide, app developer guide)
- Sample apps repo published (echo bot, poll bot, GitHub-events bot)
- Migration from Slack export documented (even if the import tool itself is v1.1)
- Public push proxy operational at `push.sliils.com` with basic SLA terms
- WCAG 2.1 AA conformance report
- Security review checklist signed off
- License + NOTICE files in place
- README + landing page on `sliils.com` reflect the launch
- Hetzner-friendly + AWS-friendly deploy guides published
