# @sliils/desktop

Tauri 2 wrapper around `apps/web`. Ships the same SPA as a native desktop app on macOS, Windows, and Linux.

## M0 status

Scaffold only — `tauri.conf.json` and Rust entrypoint exist, but the app is not yet wired to point at `apps/web` for production builds. That wiring happens in M1+ once the web app has routes worth shipping.

## Prerequisites

- Rust toolchain (`rustup` + stable channel)
- Platform build deps: https://tauri.app/start/prerequisites/

## Dev loop

```bash
# from the repo root, run both the web dev server and Tauri pointing at it:
pnpm dev                                # in one terminal (starts Vite on :5173)
pnpm --filter @sliils/desktop tauri:dev # in another
```

## Production build

```bash
pnpm --filter @sliils/web build
pnpm --filter @sliils/desktop tauri:build
```
