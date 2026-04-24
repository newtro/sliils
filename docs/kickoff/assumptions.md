# SliilS — Assumptions & Validation Gates

Every assumption made during the kickoff, with a concrete validation gate (when/how to confirm).

| # | Assumption | Validation gate | If invalid |
|---|---|---|---|
| A1 | Solo + Claude Code can deliver full Teams parity in tractable time | After M3 actuals | Drop scope at next milestone gate |
| A2 | Small teams (2–50) will install/operate self-hosted infra to escape Slack | Post-v1 install-vs-SaaS-trial conversion | Re-position toward MSP-hosted offering |
| A3 | Permissive license won't get hyperscaled-and-resold in damaging way | Quarterly post-v1 | Accept; or move to BSL for new code |
| A4 | Push notifications can be solved without Apple/Google blocking | M11 spike | Web push only; mobile via tenant SaaS partner |
| A5 | Embedded LiveKit can deliver Teams-quality meetings | M8 5-person 30-min soak | Reduce target call size; defer recording |
| A6 | Native calendar + Google/Outlook 2-way sync feasible without weeks of OAuth plumbing | M9 spike (1-week timebox) | Read-only one-way sync at v1 |
| A7 | LiveKit + Coturn on same VM as app handles 5–50 person meetings | M8 load test | Move SFU/TURN to dedicated VM |
| A8 | Single Postgres on 4 GB VM handles 100u/5ws/1M msgs | M3 + M6 soak | Vertical scale; add read replica earlier |
| A9 | Tamagui gives ~70% web↔mobile UI reuse without web perf regressions | M0 message-bubble bench | Diverge web from RN; lose reuse |
| A10 | Hosted push proxy can run on single small VM for dozens of installs | M11 estimate | Provision larger; add CDN-style regional proxies |

## How to use this list

- Each assumption has a **gate** — a milestone or specific test that confirms or invalidates it
- Implementing agents must **flag the assumption owner** when reaching its gate, not silently proceed
- A failing assumption triggers the **"If invalid" column** as the fallback plan, not an open-ended re-plan
- New assumptions added during implementation should be appended here with the same structure
