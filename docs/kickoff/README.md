# SliilS — Kickoff Artifact Package

> **SliilS** (pronounced "slice") · sliils.com
> Self-hosted, multi-tenant team collaboration platform — Slack/Teams alternative for small teams (2–50 users) with no per-seat tax.

This directory is the implementation-ready output of a comprehensive kickoff process. Each artifact is self-contained and can be read independently.

## Quick orientation

| Read this if you want to... | Open |
|---|---|
| Pitch the project in 30 seconds | [project-plan.md](project-plan.md) |
| Know what we're building (PRD) | [requirements.md](requirements.md) |
| Understand the system architecture | [architecture.md](architecture.md) |
| Implement the backend / data model / APIs | [tech-spec.md](tech-spec.md) |
| Design or build the UI | [ui-spec.md](ui-spec.md) |
| See how users actually use the product | [workflows.md](workflows.md) |
| Start coding M0 today | [implementation-roadmap.md](implementation-roadmap.md) |
| See what could go wrong | [risk-register.md](risk-register.md) |
| Know what's still unproven | [assumptions.md](assumptions.md) |
| Understand a specific decision | [adr/](adr/) (35 ADRs) |
| See the research that backed every decision | [research-report.md](research-report.md) |
| Decode project-specific terms | [glossary.md](glossary.md) |

## Headline decisions

- **Positioning:** self-hosted alternative to Slack/Teams/Discord for small teams (2–50); core value prop = no per-seat SaaS tax
- **Scope at v1:** **full Microsoft Teams parity** (chat + meetings + screen share + virtual backgrounds + file collab + calendar) — all at once, not phased
- **License:** MIT or Apache 2.0 (permissive)
- **Tenancy:** multi-tenant per install; one user identity spans many workspaces (Slack-style)
- **Encryption:** TLS only — no E2EE in v1 (enables search, bots, integrations)
- **Backend:** Go (Gin/Echo + gorilla/websocket + sqlc + River + Postgres)
- **Database:** PostgreSQL 17 with row-level security multi-tenancy
- **Search:** Meilisearch with HMAC-baked tenant tokens
- **Voice/video:** LiveKit + Coturn
- **Frontend:** Vite + React 19 + TanStack Query + Zustand + TipTap + react-virtuoso + shadcn/ui + Tailwind
- **Desktop:** Tauri 2.0 (Electron fallback)
- **Mobile:** React Native + Expo (New Architecture)
- **Universal UI:** Tamagui in monorepo
- **Object storage:** SeaweedFS (MinIO Community Edition was archived Feb 2026)
- **File collab:** Collabora Online + Yjs/Y-Sweet for native pages
- **Push notifications:** VAPID web push + project-run open-source push proxy with Signal-style opaque payloads
- **Initial deployment:** single AWS VM with PostgreSQL installed locally
- **Brand:** SliilS wordmark (two communicating people anchor the name); navy + green/blue/teal palette

## v1 in 12 milestones

M0 Bootstrap → M1 Auth → M2 Workspace → M3 Realtime messaging → M4 Mentions/threads/presence → M5 Files/media → M6 Search → M7 Multi-workspace identity → M8 Calls (LiveKit) → M9 Calendar (+Google/Outlook sync) → M10 Office docs (Collabora) + Pages (Yjs) → M11 Notifications/push → M12 Apps platform + admin + GA polish.

Detailed acceptance criteria, dependencies, and parallel-work opportunities in [implementation-roadmap.md](implementation-roadmap.md).

## How this kickoff was produced

10 phases over a single working session: Seed → Discovery → Research Sprint (8 parallel research agents, avg score 7.75/10) → Requirements → Workflows → UI Design → Tech Design → Expansion → Implementation Planning → Crystallize. The full per-phase working notes live in `.claude/kickoffs/teams-slack-clone/` (kept for audit; not required reading).

## Currency note

Every package version, license claim, and competitive datapoint was verified for April 2026 currency during the Research Sprint. Re-verify package versions before starting implementation — the ecosystem moves fast.

## Status of this package

- **Implementation-ready** — every artifact is detailed enough for an implementing agent or developer to work from without clarification.
- **Assumption-aware** — see [assumptions.md](assumptions.md) for what's still unvalidated, with a validation gate per assumption.
- **License terms not yet chosen** — MIT vs Apache-2.0 decision is in flight; pick before first public push.

---

*Generated 2026-04-23 by `/kickoff` — Claude Code project kickoff workflow.*
