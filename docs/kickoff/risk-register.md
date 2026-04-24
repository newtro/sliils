# SliilS — Risk Register

12 identified risks for v1, with likelihood, impact, mitigation, and contingency. Reviewed at every milestone gate.

## Likelihood/impact legend
- **Likelihood:** Low (< 25%), Medium (25-60%), High (> 60%)
- **Impact:** Low (annoyance), Medium (rework or feature delay), High (milestone slip), Critical (project failure or security breach)

---

## R1 — Full Teams parity at v1 is too large to ship
| Field | Value |
|---|---|
| **Likelihood** | High |
| **Impact** | High |
| **Mitigation** | Milestone sequencing (M0–M12) produces demo-able units; every gate is a re-evaluation point. Can declare v1 at M9 (chat + calls + calendar) and ship meetings/files as v1.1. |
| **Contingency** | Re-plan scope at any milestone end. |

## R2 — Push proxy iOS approval / cert management blocks mobile launch
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | High |
| **Mitigation** | Reference push proxy ships first; mobile apps come later in M11. Document BYO-proxy + re-sign path early. |
| **Contingency** | Web push only at v1 if mobile APNs gets stuck. |

## R3 — LiveKit + Coturn TURN bandwidth costs balloon
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | Use Hetzner-class providers w/ free egress; surface TURN bytes in admin dashboard. |
| **Contingency** | Document "BYO TURN" config for ops-savvy tenants. |

## R4 — RLS performance regression at scale
| Field | Value |
|---|---|
| **Likelihood** | Low (at v1 scale) |
| **Impact** | High |
| **Mitigation** | RLS bypass + perf tests in CI from M2 onward. Partition pruning on `messages` from M3. |
| **Contingency** | Schema-per-tenant fallback for hot tenants (Citus path). |

## R5 — Tamagui universal stack performance regression on web
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | Bench message-bubble web vs native at M0. Gate Tamagui adoption on benchmarks. |
| **Contingency** | Diverge web from RN if needed; lose code reuse. |

## R6 — Collabora WOPI integration brittleness
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | Smoke-test on every release; pin Collabora version. |
| **Contingency** | Fall back to "download to edit" mode. |

## R7 — Google/Outlook OAuth token refresh complexity exceeds expectations
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | Spike early in M9; consider hosted token service later. |
| **Contingency** | Defer 2-way sync; ship one-way (read-only) initially. |

## R8 — Slack-style identity-spans-workspaces leaks data via app bug
| Field | Value |
|---|---|
| **Likelihood** | Low |
| **Impact** | Critical |
| **Mitigation** | RLS as defense-in-depth; bypass probe in CI; security review pre-v1; non-owner DB role. |
| **Contingency** | Hot-fix + audit log review; coordinated disclosure if applicable. |

## R9 — MIT license invites a competitor to fork-and-resell
| Field | Value |
|---|---|
| **Likelihood** | Low |
| **Impact** | Medium (acceptable per ADR-08) |
| **Mitigation** | Project velocity + brand + community as moat. |
| **Contingency** | Accepted by licensing decision. |

## R10 — MinIO replacement (SeaweedFS) has rough edges in production
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | S3-compatible abstraction lets us swap; keep eye on Garage and other options. |
| **Contingency** | Switch driver; data is portable. |

## R11 — "All-at-once full Teams parity" stalls before M11
| Field | Value |
|---|---|
| **Likelihood** | High |
| **Impact** | High |
| **Mitigation** | Milestone gates produce ship-able interim states; can declare v1 at M9 (chat + calls + calendar) and call meetings/files v1.1. |
| **Contingency** | Rescope publicly at M9–M11 if needed. |

## R12 — Solo + Claude Code velocity assumption proves wrong on complex modules
| Field | Value |
|---|---|
| **Likelihood** | Medium |
| **Impact** | Medium |
| **Mitigation** | Test the assumption at M3 (realtime messaging is the hardest single module). |
| **Contingency** | Re-estimate after M3; partner with collaborators if needed. |

---

## Risk review cadence
- **At each milestone gate:** review all rows; adjust likelihood/impact based on new data
- **Pre-v1 launch:** full re-review by an outside reader (peer, advisor, or independent reviewer)
- **Post-launch:** monthly until 6 months in; quarterly thereafter
