# SliilS — Architecture

## 1. System Architecture

### 1.1 Logical components

```
                       ┌──────────────────────────────────────────────────┐
                       │  Clients (web · Tauri desktop · Expo mobile)     │
                       │  React 19 + Tamagui + TanStack Query + WS        │
                       └──────────────────────────────────────────────────┘
                                            │ HTTPS · WSS
                                            ▼
                       ┌──────────────────────────────────────────────────┐
                       │  SliilS App Server (Go binary)                   │
                       │  ┌──────────────┬─────────────┬──────────────┐  │
                       │  │ HTTP API     │ WS Gateway  │ Worker (River)│  │
                       │  │ Gin/Echo     │ gorilla/ws  │ Postgres queue│  │
                       │  └──────────────┴─────────────┴──────────────┘  │
                       │     │                  │                  │      │
                       │     ▼                  ▼                  ▼      │
                       │  Auth · RBAC      WS broker         Media pipe   │
                       │  RLS context      (in-proc fan-out)  Search idx  │
                       └──────────────────────────────────────────────────┘
                                │      │      │      │      │      │
            ┌───────────────────┘      │      │      │      │      └─────────────────┐
            ▼                          ▼      ▼      ▼      ▼                        ▼
    ┌──────────────┐         ┌────────────┐  │      │      │              ┌──────────────────┐
    │ PostgreSQL 17│         │ Meilisearch│  │      │      │              │ Hosted Push Proxy│
    │  (RLS multi- │         │  (tenant   │  │      │      │              │  (sliils.com,    │
    │   tenant)    │         │   tokens)  │  │      │      │              │   APNs / FCM)    │
    └──────────────┘         └────────────┘  │      │      │              └──────────────────┘
                                             ▼      ▼      ▼
                                      ┌─────────┐ ┌─────────┐ ┌──────────┐
                                      │ LiveKit │ │SeaweedFS│ │ Collabora│
                                      │ + Coturn│ │  (S3)   │ │  +Y-Sweet│
                                      └─────────┘ └─────────┘ └──────────┘
                                                       ▲
                                                       │
                                                  ┌─────────┐
                                                  │  ClamAV │
                                                  │ (clamd) │
                                                  └─────────┘
```

### 1.2 Service inventory (single Docker Compose, v1)

| Service | Image | Purpose | Ports |
|---|---|---|---|
| `sliils-app` | sliils/app (single Go binary) | HTTP+WS+Worker | 8080 |
| `postgres` | postgres:17-alpine | Primary datastore | 5432 (internal) |
| `meilisearch` | getmeili/meilisearch:v1.x | Search index | 7700 (internal) |
| `livekit` | livekit/livekit-server | WebRTC SFU | 7880 (WS), 7881 (TCP), 50000-50100/udp |
| `coturn` | coturn/coturn | TURN server | 3478, 5349, 49152-65535/udp |
| `seaweedfs` | chrislusf/seaweedfs (master+volume+s3) | S3-compatible object storage | 8333 (S3 API, internal) |
| `collabora` | collabora/code | Office docs (WOPI) | 9980 (internal) |
| `y-sweet` | jamsocket/y-sweet | Yjs server for native pages | 8090 (internal) |
| `clamd` | clamav/clamav | Antivirus daemon | 3310 (internal) |
| `caddy` | caddy:2 | TLS termination + reverse proxy | 80, 443 |

**Outside compose, project-operated:** the **push proxy** runs on the SliilS project's own infra (`push.sliils.com`) and is not part of the tenant install.

### 1.3 Process model inside `sliils-app`

The single Go binary launches one or more goroutine pools depending on flags:
- `--mode=all` (default): HTTP server + WS gateway + worker in one process
- `--mode=app`: HTTP+WS only
- `--mode=worker`: River worker only (for HA later)

This keeps v1 simple (single process on one VM) while preserving the path to splitting workers off later without code changes.

### 1.4 Realtime fan-out

- **v1 (single-node):** in-process fan-out — `pubsub.Topic("ws:" + workspaceID + ":ch:" + channelID)`. No external broker.
- **v1.1 (multi-node):** add Redis Streams adapter behind the same `pubsub.Broker` interface. Or NATS JetStream if Redis becomes a bottleneck.
- **Sticky sessions** at the load balancer (cookie-based) when multi-node is added.

### 1.5 Deployment model

- **Reference deploy:** single AWS VM (e.g., `t3.medium`, 2 vCPU + 4 GB RAM), Postgres on the same box, Caddy fronting all services
- **Storage volumes:** `pgdata`, `seaweedfs-data`, `meili-data`, `livekit-recordings` mapped to host paths
- **Backups:** nightly `pg_dump` + `seaweedfs-cli` sync to off-VM S3 location (admin-configured)
- **Upgrade path:** `docker compose pull && docker compose up -d`; migrations run on container start
- **Health endpoints:** `/healthz` (live), `/readyz` (ready — checks PG + Meili + storage)
- **Observability:** Prometheus metrics on `/metrics`; structured JSON logs to stdout (zap); OpenTelemetry traces opt-in via `OTEL_EXPORTER_OTLP_ENDPOINT`

## 2. Failure Modes & Recovery

| Failure | Detection | Behavior |
|---|---|---|
| Postgres unavailable | `/readyz` fails | App returns 503; clients show banner + retry; WS holds open |
| Meilisearch down | `/readyz` flag | Search disabled with banner; messaging unaffected |
| LiveKit unavailable | join API check | Cannot start meetings; existing meetings continue (live media unaffected if SFU still up) |
| SeaweedFS down | upload presign fails | Uploads paused; reads from cache continue best-effort |
| Push proxy down | timeout on POST /push/notify | Gracefully degrade to "in-app + email digest"; queue for retry |
| Worker queue saturated | River metrics | Add capacity; River backpressures |

Backups: nightly logical (pg_dump) + continuous WAL archive to S3; restore RPO ≤ 1h, RTO ≤ 30min for v1 scale.

## 3. Performance & Capacity (v1 single VM, t3.medium-class)

| Metric | Target | Capacity headroom |
|---|---|---|
| Concurrent WS connections | 1k–5k | Single Go binary handles 100k+; bounded by 4 GB RAM |
| Message inserts/sec | ~50 sustained, 500 burst | Postgres on local SSD comfortably handles 1k+/sec |
| Search QPS | ~10/sec | Meilisearch handles 100+/sec on this hardware |
| Active LiveKit calls | ~5 simultaneous, ~20 participants total | Coturn TURN bandwidth becomes the bottleneck; ~2 Mbps/participant |
| Storage growth | ~10 GB/month at 100 users active | SeaweedFS local volumes; offload to S3 when > 100 GB |

Synthetic load test plan: K6 (HTTP+WS), Vegeta for raw HTTP burst, custom LiveKit bot for media participation.

## 4. Where to look next

- Detailed data model, API surface, security model: [tech-spec.md](tech-spec.md)
- All architecture decisions: [adr/](adr/)
- User-facing flows that exercise this architecture: [workflows.md](workflows.md)
- Milestones that build it: [implementation-roadmap.md](implementation-roadmap.md)
