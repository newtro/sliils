# Local development

The same setup the README describes, reproduced here for docs-site completeness.

## Prerequisites

- **Node.js** ≥ 22 and **pnpm** ≥ 10
- **Go** ≥ 1.25
- **PostgreSQL** 17+ — native install
- **sqlc** (only if you edit `internal/db/queries/*.sql`)

Optional native-binary sidecars (all installed via `npm` wrappers or direct download — no Docker needed for dev):

| Service | Purpose | Install |
|---|---|---|
| Meilisearch | Full-text search | `scoop install meilisearch` (Windows) / download binary |
| LiveKit | Voice/video SFU | Download from [livekit.io](https://livekit.io) |
| Y-Sweet | Yjs collab server for Pages | `npm i y-sweet` auto-downloads the Rust binary |

## First-time setup

```bash
pnpm install

createdb -U postgres sliils_dev
psql -U postgres -d sliils_dev -c "CREATE EXTENSION citext;"

cp apps/server/.env.example apps/server/.env
# Fill in SLIILS_JWT_SIGNING_KEY, SLIILS_RESEND_API_KEY, and any
# sidecar URLs you want enabled.
```

### Generate VAPID keys for web push

```bash
cd apps/server
go run ./cmd/sliils-app genvapid
# Prints SLIILS_VAPID_PUBLIC_KEY + SLIILS_VAPID_PRIVATE_KEY — paste
# both into apps/server/.env.
```

## Run

```bash
# Terminal 1 — sidecars you want to enable
meilisearch --db-path ./data/meilisearch --master-key dev-master-key
livekit-server --config ./config/livekit-dev.yaml
y-sweet serve ./data/y-sweet --port 8787

# Terminal 2 — API server (auto-runs migrations)
pnpm dev:server

# Terminal 3 — web app
pnpm dev
```

- Web app → http://localhost:5173
- API → http://localhost:8080
- Readiness → http://localhost:8080/readyz

## Tests

```bash
# Unit (no external services)
pnpm test
pnpm go:test

# Integration (needs sliils_test DB)
createdb -U postgres sliils_test
psql -U postgres -d sliils_test -c "CREATE EXTENSION citext;"
cd apps/server && go test -tags=integration ./internal/server/...
```

Integration tests reset the schema every test, so re-run freely.
