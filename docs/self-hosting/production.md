# Production deploy

Single-VM reference deploy — the target for v1. Designed to fit comfortably on a $40/month box.

## Topology

```
                  ┌──────────────────────────────────┐
                  │        Single Linux VM           │
                  │                                   │
  Public DNS ────►│  Caddy (TLS, reverse proxy)       │
                  │    │                              │
                  │    ├──► sliils-app (Go binary)    │
                  │    ├──► LiveKit                   │
                  │    ├──► Collabora Online          │
                  │    └──► Meilisearch               │
                  │                                   │
                  │  PostgreSQL 17 on local disk      │
                  │  SeaweedFS (optional, for files)  │
                  │  Y-Sweet (Rust binary)            │
                  │                                   │
                  │  River worker (in-process)        │
                  └──────────────────────────────────┘
```

Docker Compose bundles all of it; see `docker-compose.yml` in the repo root.

## Minimum requirements

- 4 vCPU, 8 GB RAM, 100 GB SSD for a workspace of ~50 active users
- Ubuntu 22.04 or Debian 12; any modern Linux works
- A public DNS record (Caddy handles ACME cert issuance)

## Required config

Set in `.env`:

```bash
SLIILS_DATABASE_URL=postgres://sliils:...@localhost:5432/sliils?sslmode=require
SLIILS_JWT_SIGNING_KEY=$(openssl rand -hex 32)
SLIILS_RESEND_API_KEY=...                    # for outbound email
SLIILS_PUBLIC_BASE_URL=https://your-host
SLIILS_COOKIE_SECURE=true
SLIILS_COOKIE_SAMESITE=strict

# Enable the sidecars you want
SLIILS_SEARCH_ENABLED=true
SLIILS_MEILI_URL=http://localhost:7700
SLIILS_MEILI_MASTER_KEY=$(openssl rand -hex 32)

SLIILS_CALLS_ENABLED=true
SLIILS_LIVEKIT_URL=http://localhost:7880
SLIILS_LIVEKIT_WS_URL=wss://your-host/livekit
SLIILS_LIVEKIT_API_KEY=...
SLIILS_LIVEKIT_API_SECRET=$(openssl rand -hex 32)

SLIILS_PAGES_ENABLED=true
SLIILS_YSWEET_URL=http://localhost:8787

SLIILS_PUSH_ENABLED=true
SLIILS_VAPID_PUBLIC_KEY=...                  # generate: sliils-app genvapid
SLIILS_VAPID_PRIVATE_KEY=...
```

## Calls: Coturn

LAN-only calls work without a TURN server. For cross-NAT reachability run [Coturn](https://github.com/coturn/coturn) on the same box:

```
# /etc/turnserver.conf (minimal)
listening-port=3478
external-ip=your.public.ip
lt-cred-mech
user=sliils:<password>
realm=your-host
```

Wire LiveKit to it via the `turn_servers` section in `config/livekit-prod.yaml`.

## Backups

```bash
# Nightly
pg_dump -Fc sliils > /backup/sliils-$(date +%F).pgc
tar czf /backup/sliils-data-$(date +%F).tgz /var/sliils/data
```

Ship to S3 or a separate host. Test-restore monthly — an untested backup isn't a backup.

## Upgrades

```bash
docker compose pull
docker compose up -d
```

The app auto-runs DB migrations on startup. Migrations are append-only (no destructive drops without a follow-up major version).

## Health checks

- `GET /healthz` — liveness (always 200 if the process is up)
- `GET /readyz` — reachability to every configured sidecar

Configure your load balancer on `/readyz`.
