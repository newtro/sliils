# SliilS — Project Plan

## Vision
A self-hosted, multi-tenant team collaboration platform that combines the best of Slack and Microsoft Teams, designed for small teams (2–50 users per workspace) whose primary motivation is eliminating the per-seat SaaS subscription tax. Slack-anchored UX, Teams-equivalent feature surface area, MIT/Apache licensed.

## Why this exists
Slack at 50 users costs roughly $5K/year. Mattermost, Rocket.Chat, Zulip, and Element all exist as self-hosted alternatives, but each has gaps for the small-team segment:

- **Mattermost** paywalls SSO, LDAP, HA-calls into Enterprise; Calls plugin fails over strict NAT
- **Rocket.Chat** has performance complaints rampant ("unusable" forum threads); no native A/V
- **Zulip** is fully Apache-2.0 but has no native voice/video and a 5-service operational footprint
- **Element/Matrix** is the heaviest stack — federation + E2EE first-class but complex to operate
- **Stoat (Revolt)** is Discord-style — wrong vibe for "work" positioning; video still in development

**SliilS's wedge:** an MIT/Apache, single-Docker-Compose stack with chat + meetings + screen share + virtual backgrounds + file collaboration + calendar, multi-tenant in one binary, no enterprise paywall on SSO/SCIM/compliance, targeted at dev shops, indie studios, and privacy-conscious SMBs.

## Goals (v1)

1. **Ship full Microsoft Teams parity at v1 launch** — chat + meetings + screen share + virtual backgrounds + file collab + calendar. Not phased.
2. **Single Docker Compose deploys the full stack** — bring up app + Postgres + LiveKit + Coturn + SeaweedFS + Meilisearch + Collabora + Y-Sweet + worker with one command.
3. **Multi-tenant per install** — one install hosts many workspaces; one user identity spans workspaces (Slack-style).
4. **Web + Desktop (Tauri) + Mobile (Expo) clients** — web first, desktop and mobile follow with majority code reuse via Tamagui.
5. **MIT/Apache licensing all the way through** — no open-core gating of SSO, SCIM, or compliance.
6. **Slack-anchored UX with Linear-style minimalism** — familiar mental model, quiet visual treatment.

## Success criteria

| Dimension | v1 target |
|---|---|
| Scale per install | ~100 users / 5 workspaces / 1M messages on a single AWS VM (`t3.medium`) |
| Message send latency | p95 < 200ms (local), < 500ms (cross-region) |
| Search latency | p95 < 300ms |
| Cold app load | < 2s to interactive on broadband |
| Voice/video call setup | < 3s to connected |
| Concurrent WebSocket connections per node | ≥ 10k sustained |
| Accessibility | WCAG 2.1 AA conformance |
| Browser support | Last 2 versions of Chrome, Firefox, Safari, Edge |
| Internationalization | English at v1 with full string externalization |

## Roadmap (v1)

12 demo-able milestones from M0 (repo bootstrap) to M12 (apps platform + admin + GA polish). Each milestone produces something shippable on screen. See [implementation-roadmap.md](implementation-roadmap.md) for full detail.

```
M0  Repo bootstrap & dev loop
M1  Auth & first user
M2  Workspace + first channel
M3  Realtime messaging core           ← critical path
M4  Mentions, threads, presence, typing
M5  Files + media pipeline
M6  Search
M7  Multi-workspace identity (Slack-style switcher)
M8  Voice/video calls (LiveKit + Coturn)
M9  Calendar + Google/Outlook 2-way sync
M10 Office docs (Collabora) + native pages (Yjs)
M11 Notifications + push (web + desktop + mobile push proxy)
M12 Apps platform + admin + GA polish
```

## Out of scope (v1)

- E2EE, federation, Slack Connect-style shared channels
- SSO / SAML / OIDC (deferred to v1.1+)
- Slack Workflows, Lists, Huddles, Loom-style clips
- AI features (channel summarize, daily recap, transcription) — strong v1.1 candidate
- App marketplace browseable UI (apps installable via manifest in v1)
- Compliance certifications (HIPAA, SOC 2, FedRAMP)
- Cal.com integration (build native calendar instead — license-clean and Teams-shaped)
- MinIO support (CE archived Feb 2026 — SeaweedFS is the path)

Full list with rationales in [requirements.md](requirements.md).

## Brand & identity

- **Name:** SliilS (pronounced "slice")
- **Domain:** sliils.com
- **Wordmark:** Two interior letters stylized as a green and blue person beneath green/teal/blue speech sparks; navy outer letters
- **Color palette:** navy primary (`#1A2D43`), green/blue/teal accents from logo
- **Brand tone:** friendly, inclusive, communication-first, accessible — never enterprise-sterile, never gamer-edgy

Detail in [ui-spec.md](ui-spec.md).

## Strategic risks (top 3)

1. **Full Teams parity at v1 is unprecedented for a self-hosted product.** Mitigated by milestone gating — can declare v1 at M9 (chat + calls + calendar) and call meetings/files v1.1 if needed.
2. **Push proxy iOS/APNs operational reality.** Project must run a hosted push proxy bound to its signed app bundles. Not a v1 blocker (web + desktop work without it) but mobile launch depends on it.
3. **Solo + Claude Code velocity assumption.** Test-the-assumption gate is M3 (realtime messaging is the hardest single module).

Full risk register in [risk-register.md](risk-register.md). Open assumptions with validation gates in [assumptions.md](assumptions.md).

## Definition of Done (v1)

- All 12 milestones complete and acceptance criteria met
- Documentation site live (install guide, admin guide, user guide, app developer guide)
- Sample apps repo published (echo bot, poll bot, GitHub-events bot)
- Public push proxy operational at `push.sliils.com` with basic SLA terms
- WCAG 2.1 AA conformance report
- Security review checklist signed off
- LICENSE + NOTICE files in place
- README + landing page on `sliils.com` reflect the launch
