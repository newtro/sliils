# SliilS

<p align="center">
  <img src="logo.png" alt="SliilS logo" width="180" />
</p>

**Self-hosted, multi-tenant Slack/Teams alternative for small teams.** One binary, one Postgres, your box. No per-seat tax.

## What's in this site

- **Self-hosting** — how to run SliilS locally and in production, plus the security checklist you should sign off before exposing it to the internet.
- **Apps platform** — the Slack-shaped developer surface: OAuth-installable apps, bots, webhooks, slash commands, Block-Kit messages.
- **Admin guide** — what a workspace owner sees: members, roles, retention, audit log, data export (GDPR).
- **Reference** — the REST API, the WebSocket protocol, the database schema.

## Status

**M11 of 12 shipped.** See the [milestone roadmap](https://github.com/newtro/sliils/blob/main/docs/kickoff/implementation-roadmap.md) for details.

| Milestone | State | Highlights |
|---|---|---|
| Core chat + realtime (M0–M4) | ✅ | Auth, workspaces, RLS-enforced multi-tenancy, threads, mentions, presence |
| Files (M5) | ✅ | Upload, EXIF strip, SHA-256 dedupe |
| Search (M6) | ✅ | Meilisearch + tenant tokens |
| Multi-workspace (M7) | ✅ | Invites, per-workspace status + notify pref |
| Voice/video (M8) | ✅ | LiveKit SFU, DMs, meetings |
| Calendar (M9) | ✅ | RRULE, RSVPs, Google sync |
| Pages + Collabora (M10) | ✅ | TipTap + Y-Sweet + WOPI |
| Push (M11) | ✅ | VAPID web push, DND |
| Apps + Admin + GA (M12) | 🚧 | Apps platform + admin live; final polish in flight |

## Why self-host?

1. **Own your data.** `pg_dump` is your backup strategy.
2. **No per-seat tax.** Add users without adding costs.
3. **Build private integrations.** An HR bot can see HR data without shipping anything to a SaaS vendor.
4. **Audit everything.** The audit log is visible in the admin dashboard; data export is one click.

## Get started

- Running locally? → [Local development](self-hosting/local-dev.md)
- Building an app? → [Apps quickstart](apps/quickstart.md)
- Operating a production deploy? → [Production deploy](self-hosting/production.md) and the [security checklist](self-hosting/security-checklist.md)

## License

MIT. See [LICENSE](https://github.com/newtro/sliils/blob/main/LICENSE).
