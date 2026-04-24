# SliilS — Architecture Decision Records

35 decisions made during the kickoff. Each entry follows the standard ADR shape: **Context · Decision · Alternatives · Consequences**. Status: all `Accepted` unless noted otherwise.

> Consolidated into a single file for v1 navigability. As decisions are revisited, split each into its own file (`0012-backend-go.md`, etc.) so changes can be tracked atomically.

---

## ADR-01 — Self-hosted alternative positioning
- **Context:** Initial direction for "Slack/Teams clone" idea — could be learning project, niche product, self-hosted alternative, or internal tool
- **Decision:** Position as a **self-hosted alternative** (Mattermost / Rocket.Chat space)
- **Alternatives:** Learning/portfolio, niche product (gaming/study/etc.), internal-org-only tool
- **Consequences:** Open-source license required; install/operations friendliness becomes a feature; competing primarily on cost and control

## ADR-02 — Target small teams (2–50)
- **Context:** Self-hosted chat market spans hobby groups to regulated enterprise
- **Decision:** Target **small teams (2–50 users)**
- **Alternatives:** Mid-size companies (50–500), regulated/enterprise (500+)
- **Consequences:** Slack-pricing pain is the wedge; complexity overhead of enterprise compliance can be deferred

## ADR-03 — Drive on license/subscription cost (not infra or ops cost)
- **Context:** "Cost" can mean three different things for self-hosted: per-seat license cost, infrastructure cost, operational/maintenance cost
- **Decision:** Eliminate **per-seat SaaS subscription cost** (Slack ≈ $5K/yr at 50 users) — primary value prop. Accept normal infra and ops cost.
- **Alternatives:** Lean infra story (RPi-friendly), zero-touch ops (auto-update everything)
- **Consequences:** Stack can be normal-weight (PG + Go + LiveKit on a 4 GB VM); install/ops effort is acceptable; differentiator is "no per-seat tax forever"

## ADR-04 — Slack-anchored UX
- **Context:** Slack, Teams, Discord all valid mental-model anchors with different IA
- **Decision:** **Slack-anchored** — channels-first, professional, threaded, single workspace per install (Slack-style multi-workspace via switcher)
- **Alternatives:** Teams-anchored (meeting-first ribbon), Discord-anchored (server + voice rooms), hybrid
- **Consequences:** IA, terminology, composer, sidebar, integrations all follow Slack patterns; Block Kit JSON shape adopted for messages

## ADR-05 — Full Teams parity at v1 (vs phasing)
- **Context:** No self-hosted chat project has shipped full Teams parity at v1; pattern is chat-first then add meetings
- **Decision:** Plan **full Teams parity at v1** — chat + meetings + calls + screen share + virtual backgrounds + file collab + calendar
- **Alternatives:** Phase chat-first (Mattermost path), embed OSS aggressively (Rocket.Chat with Jitsi)
- **Consequences:** Larger v1 scope; risk-noted in assumption A1; mitigated by milestone gating and willingness to declare v1 at M9 (chat + calls + calendar) if needed

## ADR-06 — Web + Desktop + Mobile clients
- **Context:** Self-hosted chat needs to compete with Slack/Teams/Discord across all surfaces
- **Decision:** Ship **all three** — web first, then desktop (Tauri), then mobile (Expo)
- **Alternatives:** Web only, web + desktop only
- **Consequences:** Mobile push notifications become a hard requirement (drives ADR-19); Tamagui chosen to maximize code reuse

## ADR-07 — Multi-tenant per install with Slack-style multi-workspace identity
- **Context:** Single-tenant per install (one workspace per Postgres) vs multi-tenant
- **Decision:** **Multi-tenant per install** with **Slack-style identity** — one user account spans many workspaces on the same install
- **Alternatives:** Single-tenant (one workspace per install), full federation (Matrix-style)
- **Consequences:** Every entity carries `workspace_id`; RLS isolation everywhere; one user identity table at install scope; workspace switcher core to IA; opens path to managed multi-tenant hosting later

## ADR-08 — Permissive license (MIT or Apache-2.0)
- **Context:** AGPL/BSL/open-core would protect against commercial cloning at the cost of adoption
- **Decision:** **MIT or Apache 2.0** — final pick deferred but in this family
- **Alternatives:** AGPLv3 (Mattermost CE / Rocket.Chat / Element), BSL/Elastic 2.0 (Sentry, MariaDB MaxScale), open-core (GitLab pattern)
- **Consequences:** Maximum adoption + contribution; explicitly not protecting a managed-hosting business via license; project velocity + brand + community become the moat

## ADR-09 — TLS only (no E2EE) at v1
- **Context:** E2EE breaks server-side search, bots, integrations, and most useful chat features
- **Decision:** **TLS only** — server can read messages (encrypted at rest)
- **Alternatives:** Optional E2EE per room (Rocket.Chat), E2EE everywhere (Matrix)
- **Consequences:** Search, bots, integrations all viable; no Signal-protocol key-management complexity; not a privacy-purist product

## ADR-10 — Email + OAuth at v1; SSO/SAML/OIDC deferred
- **Context:** Small-team auth needs vs enterprise SSO requirements
- **Decision:** **Email + password + OAuth (Google / GitHub / Apple)** at v1; SSO/SAML/OIDC deferred to v1.1+
- **Alternatives:** Email only at v1, SSO/SAML at v1
- **Consequences:** Covers most small teams (Google Workspace, GitHub-using shops); SSO comes when enterprise demand emerges

## ADR-11 — Standalone install (no federation, no live bridges)
- **Context:** Standalone vs Matrix-style federation vs Slack-Connect-style live bridges
- **Decision:** **Standalone only** — each install is its own island
- **Alternatives:** Import bridges (one-shot), live bridges (persistent), federation
- **Consequences:** Matches Slack/Teams/Discord mental model; no protocol-level federation work; cross-install user identity is out-of-scope

## ADR-12 — Backend: Go (Gin/Echo + gorilla/websocket + sqlc + River + Postgres)
- **Context:** Backend choice between Phoenix/Elixir (best chat fit), Go (best self-host story), Node/TS (largest LLM corpus), .NET (Windows-friendly), Rust (lowest memory)
- **Decision:** **Go** — Mattermost's exact stack; single static binary distributed to customers is the killer self-host story
- **Alternatives:** Phoenix/Elixir (smaller LLM corpus); Node/TS (worst WS memory ceiling); .NET (steers to Azure SignalR Service); Rust (boilerplate)
- **Consequences:** Two languages with React frontend (acceptable); presence/pub-sub plumbing in-house; large Claude Code corpus available

## ADR-13 — Database: PostgreSQL 17 + RLS hybrid multi-tenancy
- **Context:** Postgres vs MongoDB vs Cockroach/Yugabyte vs SQLite/LiteFS for chat workloads; multi-tenant isolation patterns
- **Decision:** **PostgreSQL 17** with **hybrid multi-tenancy** — shared metadata DB (users, workspaces, sessions, push tokens) + per-tenant tables with RLS using `workspace_id`
- **Alternatives:** MongoDB (Rocket.Chat's pain), Cockroach (license hostile), SQLite/LiteFS (single-writer + LiteFS uncertain future); pure schema-per-tenant; pure RLS without hybrid
- **Consequences:** OpenAI-scale precedent; pgvector + LISTEN/NOTIFY in-process; `FORCE ROW LEVEL SECURITY` and connect as non-owner role for defense-in-depth; Citus 12+ schema-based-sharding as scale escape hatch

## ADR-14 — Search: Meilisearch with tenant tokens
- **Context:** Postgres FTS vs Meilisearch vs Typesense vs OpenSearch/Elasticsearch
- **Decision:** **Meilisearch** with HMAC-baked tenant tokens; transactional outbox sync from Postgres
- **Alternatives:** Postgres FTS (sufficient at v1 scale, lower-quality UX); Typesense (RAM-resident floor heavier); ES (overkill)
- **Consequences:** Workspace + channel-visibility scoping baked into tokens — can't be overridden client-side; smaller ops footprint than ES; great DX

## ADR-15 — Voice/Video: LiveKit + Coturn
- **Context:** LiveKit vs Jitsi (embed) vs mediasoup vs Janus vs Pion
- **Decision:** Build on **LiveKit** (Apache 2.0 SFU) + Coturn for TURN
- **Alternatives:** Jitsi embed (faster Phase-0 demo, less UX control), mediasoup (too much glue), Janus (GPLv3 — license-hostile), Pion/ion-sfu (rebuild LiveKit)
- **Consequences:** License-clean; Element Call migrated from Jitsi → LiveKit (precedent); rich SDK suite in all client languages; LiveKit Egress for recording; Agents framework opens AI features later

## ADR-16 — Frontend: Vite + React 19 SPA + TanStack Query + Zustand + TipTap + react-virtuoso + shadcn/ui + Tailwind
- **Context:** Framework + meta-framework + state + editor + virtualization + components for a chat web app in 2026
- **Decision:** Vite SPA (no SSR — chat is behind auth) + React 19 + React Router 7 (data router, SPA mode) + TanStack Query (server cache) + Zustand (UI state) + TipTap (composer) + react-virtuoso (message list) + shadcn/ui + Radix + Tailwind
- **Alternatives:** Next.js (overkill); Svelte/Solid (smaller ecosystem for chat libs); Lexical (less mature mention/collab); TanStack Virtual (struggles with bidirectional infinite scroll); Replicache (BSL license disqualified)
- **Consequences:** Linear-style sync engine pattern — TanStack Query + custom WS + IndexedDB outbox; entire ecosystem aligns with React; static SPA bundle deploys anywhere

## ADR-17 — Object storage: SeaweedFS + S3-compatible abstraction
- **Context:** MinIO Community Edition was archived Feb 2026; CE web UI stripped in 2025; project pivoted to commercial AIStor at $96K/yr
- **Decision:** **SeaweedFS** (Apache 2.0) as recommended self-hosted default; ship `IStorage` abstraction with drivers for local disk + any S3-compatible (covers SeaweedFS, Garage, R2, B2, Wasabi)
- **Alternatives:** MinIO (EOL for community), Garage (AGPL bundling concerns), local-disk-only
- **Consequences:** Apache 2.0 license-clean default; tenants can swap to managed S3-compatible providers trivially; presigned URLs mandatory (never proxy bytes through app server)

## ADR-18 — Office docs: Collabora Online (MPL) + Yjs/Y-Sweet for native pages
- **Context:** OnlyOffice vs Collabora for embedded Office docs; native pages (Slack canvas / Notion-lite) implementation
- **Decision:** **Collabora Online** (MPL 2.0, single container, no concurrency cap) for embedded Office; **Yjs + TipTap + Y-Sweet** for native pages
- **Alternatives:** OnlyOffice CE (AGPLv3 + 20-conn cap), bespoke Office (OOXML spec is 6000 pages), only Yjs (no Office support)
- **Consequences:** MPL doesn't AGPL-contaminate downstream tenants; Y-Sweet handles Yjs sync + S3 persistence; OnlyOffice optional plug-in for tenants who need pixel-perfect DOCX

## ADR-19 — Push: VAPID + project-run hosted push proxy with Signal-style payloads
- **Context:** Self-hosted iOS/Android push genuinely requires APNs/FCM credentials bound to the project's signed app bundle
- **Decision:** **VAPID + Web Push** for web (project owns this); project-operated **MIT-licensed hosted push proxy** at `push.sliils.com` for mobile, with Signal-style **opaque payloads** (push proxy never sees plaintext)
- **Alternatives:** Per-tenant credentials (only works with white-labeled re-signed app), UnifiedPush-only (Android FOSS niche)
- **Consequences:** Project must operate hosted push proxy as long-term infrastructure; tenants who refuse proxy can re-sign apps with own bundle IDs and run own proxy; UnifiedPush as bonus secondary registration

## ADR-20 — Desktop: Tauri 2.0 (Electron fallback)
- **Context:** Tauri 2 vs Electron vs Wails 3 vs Flutter Desktop for cross-platform desktop
- **Decision:** **Tauri 2.0**; Electron as fallback if Chromium parity required
- **Alternatives:** Electron (10-20× larger), Wails 3 (alpha), Flutter (orphans React)
- **Consequences:** 10-20× smaller bundles (~8 MB vs 165 MB), ~5× lower battery impact; near-zero port from React web; v2 plugin suite covers tray, notifications, autostart, deep links, updater

## ADR-21 — Mobile: React Native + Expo (New Architecture)
- **Context:** RN vs Flutter vs Capacitor vs Tauri Mobile vs native (Swift+Kotlin)
- **Decision:** **React Native + Expo** with New Architecture (Fabric + TurboModules)
- **Alternatives:** Flutter (orphans React investment), Capacitor (chat scroll perf risk), Tauri Mobile (immature), native (2 codebases)
- **Consequences:** New Architecture mandatory in SDK 55 / RN 0.83 (2026) — 43% faster cold start, 39% faster renders, 25% less RAM; EAS removes ~80% of solo-dev pain; Tamagui enables ~70% UI reuse with web

## ADR-22 — Universal UI: Tamagui in monorepo
- **Context:** Maximize UI code reuse across web + desktop + mobile
- **Decision:** **Tamagui** universal components from day one in `packages/ui`; monorepo `apps/{web,desktop,mobile}` + `packages/{ui,api,schemas}`
- **Alternatives:** Plain RN+separate web (re-implement), Gluestack (smaller community), no universal lib
- **Consequences:** Compiles to atomic CSS on web, hoisted styles on native; ~70% UI reuse web↔mobile, ~95% desktop↔web; commits to a single design-system source

## ADR-23 — Integration platform: Slack-Block-Kit-clone, out-of-process HTTPS apps only
- **Context:** Mattermost is *deprecating* its in-process Apps Framework in v10; Block Kit's JSON UI is the dominant pattern
- **Decision:** **Block-Kit-style JSON UI** (section/actions/input/modal/carousel/card); webhooks/slash commands/bots/OAuth 2.0 PKCE; **out-of-process HTTPS apps only**; HMAC-signed events; manifest-based install
- **Alternatives:** In-process plugins (Mattermost's pain), bespoke UI framework
- **Consequences:** Language-agnostic for app developers; small core server; clear security boundaries; marketplace UI deferred to v1.1

## ADR-24 — Calendar: native + Google/Outlook 2-way sync (no Cal.com)
- **Context:** Cal.com integration vs native calendar build
- **Decision:** **Native first-class calendar** (events, RSVP, recurrence, time zones, meeting links auto-launching LiveKit) with **Google + Outlook 2-way sync**
- **Alternatives:** Embed Cal.com (AGPLv3 contamination), embed Cal.diy (MIT but uncertain maintenance), defer calendar to v1.1
- **Consequences:** License-clean; Teams-shaped use case (internal meetings); integrated UX (`/meet` from composer); Cal.com is wrong shape for this use case (it's external-booking, not internal-team-meetings)

## ADR-25 — v1 scale target: small (~100u/5ws/1M msg) on single VM
- **Context:** Initial deployment scale and architecture ceiling
- **Decision:** **Small** scale target — ~100 users / 5 workspaces / 1M messages on a single AWS VM (`t3.medium`, 2 vCPU + 4 GB RAM); architecture must allow scale-out to medium without rewrites
- **Alternatives:** Medium (1k/50/100M), Large (10k/500/1B+), graduated all-three
- **Consequences:** Single-node Postgres + single Go binary; no NATS/Redis at v1; HA path designed in via `--mode` process flagging

## ADR-26 — Browser support: evergreen last-2-versions
- **Context:** Browser support matrix
- **Decision:** Last 2 stable versions of Chrome, Firefox, Safari, Edge
- **Alternatives:** IE11/legacy, latest-only, all-back-to-2018
- **Consequences:** Modern web APIs available; chat is behind auth so SEO/legacy concerns are minimal; graceful degradation banner for older browsers

## ADR-27 — i18n English-only at v1 with full string externalization
- **Context:** Internationalization scope
- **Decision:** English-only at v1 but **all user-visible strings externalized** via `react-i18next` or `lingui` so translations addable without refactor
- **Alternatives:** i18n-from-day-one with N translations, no i18n infrastructure at all
- **Consequences:** Solo + Claude Code velocity; single language to maintain pre-launch; translations are a v1.1+ contributor opportunity

## ADR-28 — Accessibility: WCAG 2.1 AA target
- **Context:** A11y commitment level
- **Decision:** **WCAG 2.1 AA** target — keyboard nav, ARIA roles, screen reader tested (NVDA, VoiceOver), high-contrast theme, reduced-motion respect
- **Alternatives:** WCAG 2.2 AAA (overkill), no a11y commitment
- **Consequences:** Industry standard; required by many institutional buyers; right-thing-to-do baseline

## ADR-29 — Single Docker Compose ships full stack
- **Context:** Deployment story differentiation
- **Decision:** Single `docker-compose.yml` brings up the full stack (app + Postgres + LiveKit + Coturn + SeaweedFS + Meilisearch + Collabora + Y-Sweet + worker + Caddy)
- **Alternatives:** Per-service install, K8s-only, single mega-binary
- **Consequences:** "Just works" deploy is the strategic wedge against Mattermost/Zulip/Matrix complexity; tenant ops effort minimized

## ADR-30 — Project name SliilS (pronounced "slice")
- **Context:** Project naming
- **Decision:** **SliilS** (sliils.com), with logo wordmark featuring two communicating people as the interior letters
- **Alternatives:** Generic / unnamed / other
- **Consequences:** Brand identity anchored on collaboration motif; navy + green/blue/teal palette; friendly + accessible tone

## ADR-31 — Shell IA: Slack-classic + Linear minimalism
- **Context:** Main shell layout for the web/desktop client
- **Decision:** **Slack-classic shell + Linear minimalism** — 48px workspace rail + 240px customizable channel tree + main + 320px slide-in details
- **Alternatives:** Single-pane sidebar (loses fast-switch), Teams-style ribbon (enterprise vibe wrong for segment)
- **Consequences:** Stacked workspace icons proven for multi-tenant; Slack's user-customizable sections (vs Discord's locked); Linear's "minimalism = faster orientation"; quiet visual treatment

## ADR-32 — Visual identity sampled from logo
- **Context:** Color palette + typography + iconography
- **Decision:** Navy primary `#1A2D43` + green `#5BB85C` + blue `#3B7DD8` + teal `#27A8AC` (sampled from logo); **Inter** for UI; **JetBrains Mono** for code; **Lucide** icons; **shadcn/ui** + **Radix** primitives + **Tailwind**
- **Alternatives:** Material/Mantine kits, custom icon set
- **Consequences:** Sampled directly from supplied SliilS logo; matches "friendly + accessible" tone; ecosystem-mature font + icon picks

## ADR-33 — Single-binary, mode-flagged process model
- **Context:** v1 must run on one VM; v1.1+ may need worker isolation for HA
- **Decision:** Ship one Go binary with a `--mode` flag selecting `all` (default) / `app` / `worker`
- **Alternatives:** Separate binaries from day one (premature complexity); one mega-binary forever (blocks HA)
- **Consequences:** Zero-cost path to splitting workers; one build artifact; tests cover all combos

## ADR-34 — WebSocket envelope `{v, type, id, data}` with `since=<event_id>` reconnect replay
- **Context:** Mobile networks drop frequently; we need reconnect without lost-message anxiety
- **Decision:** Every push has a monotonic `event_id`; client persists `last_event_id` in IndexedDB; reconnect sends `?since=<id>`; server replays from a 5-min in-memory ring buffer or signals full resync
- **Alternatives:** Long-polling; persistent per-channel event log (heavier); Phoenix Channels (rejected with backend choice)
- **Consequences:** Simple pattern; bounded memory cost; full resync edge case must be cheap (Virtuoso + range query)

## ADR-35 — Messages partitioned monthly by `created_at` via pg_partman
- **Context:** Chat is append-heavy; without partitioning a 100M-row table will hurt vacuum and queries
- **Decision:** `pg_partman` time partitions on `messages.created_at`, monthly granularity; retention enforced by dropping old partitions per workspace policy
- **Alternatives:** Unpartitioned (works at v1 scale, fails later); per-channel partitioning (too many shards); per-workspace schemas (add later for compliance customers)
- **Consequences:** Cleaner vacuum; partition pruning on common queries; retention = `DROP PARTITION` instead of `DELETE` (massive perf win)
