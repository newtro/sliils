# SliilS — Research Report

> Output of the kickoff Research Sprint — 8 parallel research agents executing 50+ web searches with explicit 2026-currency. Average research depth score: 7.75/10. All findings cited with source URLs.

## Research scores by topic

| Topic | Score | Confidence |
|---|---|---|
| Database & Search | 8.5 | High |
| Backend Stack | 8 | High |
| Frontend Stack | 8 | High |
| Push Notifications | 8 | High |
| Desktop & Mobile Frameworks | 8 | High |
| File Storage, Media & Integrations | 8 | High |
| Competitive Landscape | 7.5 | High |
| Voice/Video Architecture | 6 | Medium (partially verified by other agents) |

---

## 1. Competitive Landscape (7.5/10)

| Project | Stack | License | Standout | Critical Weakness |
|---|---|---|---|---|
| **Mattermost** v11.6.1 (Apr 2026) | Go + React + Postgres; Calls plugin (rtcd SFU) | MIT (CE) + proprietary EE | Closest Slack-UX clone; HIPAA/GDPR/SOC2; native voice/screenshare | SSO/LDAP/HA-calls all paywalled in Enterprise; Calls fails over strict NAT |
| **Rocket.Chat** | Node + Meteor 3.0 + MongoDB | MIT (CE) + proprietary EE | 200+ marketplace apps; omnichannel; widest integrations | Performance complaints rampant ("unusable" forum threads); no native A/V |
| **Zulip** v11.2 | Django + Tornado + Postgres + RabbitMQ + Memcached + Redis (5 services) | Apache 2.0 (no gating) | Best-in-class threading (streams + topics); fast web client | No native A/V at all; weak mobile; 5-service footprint heavy for small teams |
| **Element/Matrix** | Synapse (Python) + Postgres; Element Call on LiveKit | AGPLv3 (Synapse, Element apps) | Federation + E2EE first-class; gov adoption | Heaviest stack; complex to self-host; Element/New Vector funding crisis |
| **Stoat** (rebranded from Revolt, Oct 2025) | Rust microservices + MongoDB + Redis + LiveKit | AGPLv3 | Discord-style UX; lean Rust footprint; >600K users | Video/screen share still in development; not work-positioned |
| **Mumble** v1.6 RC | C++/Qt | BSD-3 | Lowest-latency self-hosted voice; positional audio | Voice-only; no text/video/files |

### Strategic wedge for SliilS
"MIT/Apache, single-compose stack — chat + meetings + screen share + virtual backgrounds, multi-tenant in one binary, no enterprise paywall on SSO/SCIM/compliance, 1 vCPU/1GB for 25 users." The 2-50 band is too small for Mattermost/Rocket.Chat's enterprise sales motion and too work-like for Stoat's gaming positioning.

### Universal gaps (every incumbent misses these)
1. One Docker Compose with A/V built-in
2. Native meetings + screen share without per-seat gating (Mattermost paywalls; Zulip/Rocket.Chat punt to Jitsi)
3. Multi-tenancy without K8s
4. Honest non-open-core licensing (only Zulip is fully permissive, but no A/V)
5. Modern file collab + virtual backgrounds (none ship by default)
6. Mobile that doesn't suck (universal complaint)

**Sources:** [Mattermost arch docs](https://docs.mattermost.com/deployment-guide/reference-architecture/application-architecture.html) · [Rocket.Chat is unusable forum thread](https://forums.rocket.chat/t/rocket-chat-is-unusable/14893) · [Zulip architectural overview](https://zulip.readthedocs.io/en/latest/overview/architecture-overview.html) · [Register: Matrix in EU government Feb 2026](https://www.theregister.com/2026/02/09/matrix_element_secure_chat/) · [AlternativeTo: Revolt → Stoat rebrand Oct 2025](https://alternativeto.net/news/2025/10/chat-app-revolt-rebrands-to-stoat-keeps-all-services-features-and-core-values-unchanged/) · [Mumble Docker guide Feb 2026](https://oneuptime.com/blog/post/2026-02-08-how-to-run-mumble-server-in-docker-for-voice-chat/view)

---

## 2. Backend Stack (8/10)

**Recommendation:** **Go + Gin/Echo + gorilla/websocket + sqlc + River + Postgres** (Mattermost's exact stack). Runner-up: Phoenix/Elixir.

| Stack | Strength for chat | Weakness |
|---|---|---|
| **Go** | Single static binary (best self-host story); 100k+ WS/binary; what Mattermost picks; large LLM corpus | More plumbing for presence/pub-sub |
| **Elixir/Phoenix** | Channels + Presence + PubSub eliminate 3 components; Discord at millions CCU | Smaller LLM corpus |
| **Node/TS** | Largest LLM corpus | Worst WS memory ceiling (~10-50k/process) |
| **.NET/SignalR** | Mature WS | MS recommends Azure SignalR Service for prod — hostile to self-host |
| **Rust** | Lowest memory; compiler verifies LLM output | Most boilerplate; smallest chat ecosystem |

**Sources:** [Phoenix 2M WS](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections) · [Discord tracing Elixir 2026](https://discord.com/blog/tracing-discords-elixir-systems-without-melting-everything) · [Mattermost app arch](https://docs.mattermost.com/deployment-guide/reference-architecture/application-architecture.html) · [SignalR scale 2026](https://learn.microsoft.com/en-us/aspnet/core/signalr/scale?view=aspnetcore-10.0)

---

## 3. Frontend Stack (8/10)

**Recommendation:** Vite + React 19 SPA, no Next.js.

| Concern | Pick | Why |
|---|---|---|
| Framework | React 19 | Every chat-adjacent lib (TipTap, Virtuoso, shadcn, Liveblocks, Tamagui) is React-first |
| Build | Vite 6 | Static bundle, no SSR tax (chat lives behind auth — no SEO need) |
| Router | React Router 7 (data router, SPA mode) | Loaders for channels; no Next App Router complexity |
| Server cache | TanStack Query v5 | WS handler → `setQueryData`, optimistic mutations |
| UI state | Zustand | Composer drafts, active channel, typing indicators |
| Editor | TipTap + Mention/Suggestion/SlashCommand/CodeBlock | Best Slack-style composer; ProseMirror under hood |
| Virtualization | react-virtuoso | Purpose-built for chat (TanStack Virtual struggles with bidirectional) |
| Components | shadcn/ui + Radix + Tailwind | Own-your-source, a11y built-in |
| Realtime client | Custom WS + Query cache + IndexedDB outbox | Linear's pattern; Replicache disqualified (BSL) |
| Offline | Workbox SW + IndexedDB | Mature, small footprint |

**Sources:** [React 19 vs Vue 3.6 vs Svelte 5 byteiota](https://byteiota.com/react-19-vs-vue-3-6-vs-svelte-5-2026-framework-convergence/) · [TipTap vs Lexical 2026 PkgPulse](https://www.pkgpulse.com/blog/tiptap-vs-lexical-vs-slate-vs-quill-rich-text-editor-2026) · [react-virtuoso](https://virtuoso.dev/) · [Linear Sync Engine](https://www.fujimon.com/blog/linear-sync-engine) · [shadcn/ui vs Base UI vs Radix 2026](https://www.pkgpulse.com/blog/shadcn-ui-vs-base-ui-vs-radix-components-2026) · [Vite vs Next.js 2026](https://designrevision.com/blog/vite-vs-nextjs)

---

## 4. Database & Search (8.5/10)

**Recommendation:** **PostgreSQL 17 + hybrid multi-tenancy (shared metadata DB + RLS-with-`workspace_id` per-tenant tables)** + **Meilisearch** with tenant tokens.

- **Postgres 17:** 2x write throughput, 20x less vacuum memory; OpenAI scaled single-primary Postgres to millions of QPS for ChatGPT
- **Multi-tenancy:** RLS using `current_setting('app.current_workspace')`; `FORCE ROW LEVEL SECURITY` and connect as non-owner role. Hybrid: `users` + `workspace_memberships` in shared schema, tenant data in RLS'd tables. Schema-per-tenant available as opt-in for regulated tenants (Citus 12+ schema-based sharding as scale escape hatch)
- **Search:** Meilisearch with tenant tokens — workspace + channel-visibility scoping HMAC-baked into token, can't be overridden client-side. Sync via transactional outbox from Postgres
- **Disqualified:** MongoDB (Rocket.Chat's pain), Cockroach (license hostile), SQLite/LiteFS (single-writer, LiteFS uncertain future)

**Sources:** [PG 17 release](https://www.postgresql.org/about/news/postgresql-17-released-2936/) · [OpenAI scales single-primary PG Feb 2026](https://www.infoq.com/news/2026/02/openai-runs-chatgpt-postgres/) · [AWS multi-tenant RLS](https://aws.amazon.com/blogs/database/multi-tenant-data-isolation-with-postgresql-row-level-security/) · [Crunchy RLS for tenants](https://www.crunchydata.com/blog/row-level-security-for-tenants-in-postgres) · [Meilisearch multi-tenancy](https://www.meilisearch.com/blog/multi-tenancy-guide) · [Meili tenant tokens spec](https://github.com/meilisearch/specifications/blob/main/text/0089-tenant-tokens.md) · [Citus 12 schema-based sharding](https://www.citusdata.com/blog/2023/07/18/citus-12-schema-based-sharding-for-postgres/)

---

## 5. Voice/Video Architecture (6/10)

**Recommendation:** Build on **LiveKit** (Apache 2.0 SFU) + Coturn for TURN.

- LiveKit gives you the SFU, signaling, simulcast, SVC, egress (recording), client SDKs (JS, Swift, Kotlin, Flutter, RN)
- **Element Call migrated from Jitsi to LiveKit** in 2023 — strongest precedent for a chat-anchored product
- **Optional Phase 0:** embed Jitsi Meet iframe behind a feature flag for instant demos
- **Disqualified:** Janus (GPLv3 — license-hostile), mediasoup (too much glue for solo), Pion/ion-sfu (you'd be rebuilding LiveKit)
- **TURN:** Coturn on Hetzner (€10-20/mo, 20TB free egress) handles dozens of small tenants

LiveKit Agents framework opens the door to AI features (live transcription, meeting summaries) — table stakes for a 2026 Teams competitor.

**Sources:** [Element Call announcement](https://element.io/blog/introducing-native-matrix-voip-with-element-call/) · [Mattermost Calls deployment](https://docs.mattermost.com/administration-guide/configure/calls-deployment.html) · LiveKit (livekit.io)

---

## 6. Push Notifications (8/10)

**Bottom line: you will run a hosted push proxy for mobile.** No way around it.

| Surface | Approach |
|---|---|
| **Web** | VAPID + Web Push directly. Safari 18.4 Declarative Web Push for iOS PWA reliability. |
| **Desktop** | In-app socket → native Notification API (Tauri). Web Push fallback when fully closed. |
| **Mobile** | MIT-licensed open-source push proxy (Mattermost's `mattermost-push-proxy` model). Holds project's APNs `.p8` + FCM HTTP v1 service-account credentials. Free hosted reference instance. Document BYO-proxy + re-sign-app path. |
| **Privacy** | Signal-style: payload is opaque wake-up ping with message ID; client fetches/decrypts content from tenant server. Proxy never sees plaintext. |
| **FOSS bonus** | Add UnifiedPush as secondary registration alongside FCM (Android). |
| **Skip** | iOS Live Activities (entitlement bar too high), Critical Alerts. |

Critical 2026 facts: FCM legacy server-key API shut down July 2024 — must use HTTP v1 + OAuth2. iOS PWA web push works since 16.4 but is "fragile" (no silent push, no background sync).

**Sources:** [Mattermost self-host push proxy](https://docs.mattermost.com/deploy/mobile/host-your-own-push-proxy-service.html) · [mattermost-push-proxy GitHub](https://github.com/mattermost/mattermost-push-proxy) · [Matrix Sygnal push gateway](https://github.com/matrix-org/sygnal) · [UnifiedPush 5-year milestone](https://f-droid.org/2026/01/08/unifiedpush-5-years.html) · [PWA on iOS 2026](https://www.mobiloud.com/blog/progressive-web-apps-ios) · [FCM HTTP v1 migration](https://firebase.google.com/docs/cloud-messaging/migrate-v1) · [APNs token-based auth](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns) · [RFC 8292 VAPID](https://datatracker.ietf.org/doc/html/rfc8292)

---

## 7. Desktop & Mobile Frameworks (8/10)

**Desktop:** **Tauri 2.0** (Electron as fallback)
- 10-20× smaller bundles (~8 MB vs 165 MB), ~5× lower battery impact
- v2 plugin suite covers tray, notifications, autostart, deep links, updater
- Near-zero port cost from React web client
- Electron fallback if Chromium parity required

**Mobile:** **React Native + Expo (New Architecture)**
- New Architecture mandatory in SDK 55 / RN 0.83 (2026); 43% faster cold start, 39% faster renders, 25% less RAM
- EAS Build/Submit/Update removes ~80% of solo-dev pain
- Shares React mental model with web; Tamagui enables ~70% UI reuse
- Disqualified: Flutter (orphans React investment), Tauri Mobile (immature), native (2 codebases)

**Universal code-sharing:** Tamagui from day one. Monorepo `apps/{web,desktop,mobile}` + `packages/{ui,api,schemas}`. ~70% UI reuse web↔mobile; ~95% desktop↔web.

**Sources:** [Tauri vs Electron 2026 PkgPulse](https://www.pkgpulse.com/blog/best-desktop-app-frameworks-2026) · [Tauri 2.0 Stable Release](https://v2.tauri.app/blog/tauri-20/) · [React Native New Architecture 2026](https://www.pkgpulse.com/blog/react-native-new-architecture-fabric-turbomodules-expo-2026) · [RN vs Expo vs Capacitor 2026](https://www.pkgpulse.com/blog/react-native-vs-expo-vs-capacitor-cross-platform-mobile-2026) · [Tamagui — Universal RN UI](https://tamagui.dev/)

---

## 8. File Storage, Media & Integrations (8/10)

**Critical 2026 update:** MinIO Community Edition was archived Feb 2026; web admin UI stripped in March 2025; project pivoted to commercial AIStor at $96K/yr. **Don't build on MinIO.**

| Layer | Recommendation |
|---|---|
| **Storage abstraction** | `IStorage` interface; drivers for local disk + S3 (covers SeaweedFS, Garage, R2, B2, Wasabi). **SeaweedFS** as recommended self-hosted default (Apache 2.0, production-validated). Presigned URLs mandatory. |
| **Media pipeline** | Dedicated worker queue. libvips/sharp for images (4-5× faster than ImageMagick), ffmpeg 6+ for video/audio (HLS + AV1), clamd daemon mode for AV, Poppler for PDF previews, EXIF strip on ingest. Never in request path. |
| **File collab** | **Collabora Online** (MPL 2.0, single container, no concurrency cap) embedded as default Office editor; **Yjs + TipTap + Y-Sweet** for native "pages"/canvas. OnlyOffice (AGPLv3) as optional pluggable alternative. |
| **Integrations** | Slack-Block-Kit-clone JSON UI with new April 2026 blocks (carousel, card, data table, streaming). Webhooks/slash commands/bots/OAuth 2.0. **Out-of-process HTTPS apps only** — Mattermost is *deprecating* in-process Apps Framework in v10. |

**Critical license guidance:** avoid bundling AGPL defaults to keep downstream tenants AGPL-free. SeaweedFS (Apache 2.0) and Collabora (MPL 2.0) are clean.

**Sources:** [MinIO Alternatives 2026 (Akmatori)](https://akmatori.com/blog/minio-alternatives-2026-comparison) · [MinIO Stripping Functions Futuriom](https://www.futuriom.com/articles/news/minio-faces-fallout-for-stripping-features-from-web-gui/2025/06) · [Sharp / libvips](https://sharp.pixelplumbing.com/) · [ClamAV On-Access 2026](https://copyprogramming.com/howto/how-to-scan-on-access-with-clamav-in-v14-04) · [OnlyOffice vs Collabora Elest.io](https://blog.elest.io/collabora-online-vs-onlyoffice-which-self-hosted-office-suite-after-the-euro-office-fork/) · [Y-Sweet Yjs server](https://jamsocket.com/y-sweet) · [Slack Block Kit New Blocks April 2026](https://docs.slack.dev/changelog/2026/04/16/block-kit-new-blocks/) · [Mattermost v10 Apps Framework deprecation](https://forum.mattermost.com/t/transition-from-apps-framework-to-plugin-framework-in-mattermost-v10/19330)

---

## 9. Cross-Cutting Tensions Surfaced

1. **Backend stack vs. LLM corpus.** Phoenix is technically best for chat; Go (chosen) gives the strongest single-binary self-host story; TS-everywhere maximizes LLM corpus but worst WS memory ceiling. **Decision recorded: Go.**
2. **MinIO is no longer viable** as of Feb 2026. SeaweedFS is the Apache-2.0 replacement.
3. **Push proxy reality.** Self-hosting the chat server doesn't mean self-hosting push delivery. Project must run a free hosted push proxy.
4. **Tamagui early commitment.** Universal RN+web stack only pays off if mobile actually ships. Worth the bet given mobile is on v1 roadmap.
5. **License hygiene.** Avoid bundling AGPL defaults (OnlyOffice, Garage, Janus). Recommended defaults are all MIT/Apache/MPL/BSD-friendly.

---

## Currency note

Every claim above was validated for April 2026 currency during the Research Sprint. Re-verify package versions and any "X is shipping/pricing/licensed Y" claims before starting implementation — the ecosystem moves fast.
