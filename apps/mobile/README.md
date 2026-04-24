# @sliils/mobile

Expo (React Native) mobile app. **New Architecture enabled** per the locked tech-spec (Hermes + Fabric + TurboModules).

## M0 status

Scaffold only — `app.json` exists with the SliilS brand, bundle IDs, and `newArchEnabled: true`. No source files yet; the real app surface is built starting at M1 (auth) via `expo-router`.

## Prerequisites

- Expo CLI: bundled via `pnpm install`
- For native builds: Xcode (iOS), Android Studio (Android), or use EAS cloud builds

## Initializing the source (one-time, when starting M1)

```bash
cd apps/mobile
pnpm dlx create-expo-app@latest --template tabs@latest .
```

Accept the template, then manually re-apply the M0 brand in `app.json`.

## Build & release

- Dev client: `pnpm --filter @sliils/mobile start`
- Cloud builds: `pnpm --filter @sliils/mobile eas build --platform ios|android`
