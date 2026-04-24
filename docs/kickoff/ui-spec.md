# SliilS — UI & Experience Specification

## Brand & Visual Identity

- **Wordmark:** SliilS — navy outer letters, two interior letters stylized as a green person + blue person beneath green/teal/blue speech sparks
- **Primary palette (sampled from logo):**
  - `--sl-navy`: `#1A2D43` — primary text, brand wordmark, dark theme background
  - `--sl-green`: `#5BB85C` — primary accent (left person, success, "sent" indicators)
  - `--sl-blue`: `#3B7DD8` — secondary accent (right person, links, focus rings)
  - `--sl-teal`: `#27A8AC` — tertiary accent (sparks, callouts, subtle highlights)
- **Neutral scale:** 11-step gray ramp (50–950) tuned to be slightly cool to harmonize with navy
- **Typography:**
  - UI: **Inter** (variable) — 12/13/14/16/20/24/32 ramp, weights 400/500/600/700
  - Code: **JetBrains Mono** — 13/14/15
- **Brand tone:** friendly, inclusive, communication-first, accessible. The "two people communicating" motif means we stay warm and human — never enterprise-sterile, never gamer-edgy.

## Design System Foundations

| Token | Value | Notes |
|---|---|---|
| Spacing scale | 4 / 8 / 12 / 16 / 24 / 32 / 48 / 64 px | shadcn/Tailwind defaults |
| Radius scale | 4 / 6 / 8 / 12 / 16 / 9999 (pill) | Slightly soft; 8 default |
| Shadow elevations | sm / md / lg / xl | Used sparingly; flat-with-borders dominates |
| Z-index | base / sticky / overlay / modal / toast / tooltip | Documented, not magic numbers |
| Animation curves | ease-out (200ms) for entrances, ease-in (120ms) for exits | Respect `prefers-reduced-motion` |
| Focus ring | 2px `--sl-blue` w/ 2px offset | Always-visible on keyboard nav |
| Hit-target min | 36×36 px (mobile 44×44) | WCAG 2.1 AA |

**Component approach:** shadcn/ui as the source-of-truth (own the source), Radix primitives for a11y, Tailwind for styling. Every primitive (Button, Input, Dialog, Popover, DropdownMenu, Tooltip, Toast, Tabs, etc.) lives in `packages/ui` for monorepo reuse via Tamagui.

**Iconography:** Lucide (MIT, ~1500 icons, consistent stroke). Custom marks only for SliilS-specific concepts (workspace, huddle).

## Information Architecture (Shell)

**Slack-classic shell + Linear-style minimalism.**

```
+----+------------------+--------------------------------------------+----------+
| WS | Workspace pane   | Main content                               | Details  |
| 48 | 240-280           | (channels, DMs, calendar, settings, etc.) | 320      |
| px | (collapsible)    |                                            | (slide)  |
+----+------------------+--------------------------------------------+----------+
```

**Left rail (48px):** stacked workspace icons (avatar / 1-letter mark), top-aligned. Active workspace gets the `--sl-blue` left-edge bar + brighter icon. Bottom: settings cog + user avatar.

**Workspace pane (240–280px, collapsible to 56px icon-only):**
- Header: workspace name + dropdown (settings, invite, switch, sign-out)
- Compose button (prominent, `--sl-green` accent)
- Search trigger (cmd+K visible on hover/focus)
- **Sections** (user-customizable, drag-reorder, collapsible):
  - Channels (default; star/unstar, add channel)
  - Direct Messages (with presence dot)
  - Apps (installed integrations)
  - User-created sections (e.g., "Clients", "Side Projects")
- Hide rules: muted channels collapse into "More" by default (Linear-minimalism pattern)

**Main content (flexible, min 640px):**
- Channel header: name, topic, member count, ⋯ menu
- Message list (Virtuoso, bidirectional infinite scroll)
- Composer (TipTap, sticky bottom, expands to multi-line, slash-command popover, mention popover)

**Details slide (320px, slide-in from right):** Thread sidebar (most common), channel members, channel info, file details, pinned messages, bookmarks.

## Key Screens

### 1. Setup (first-install bootstrap)
3 steps: owner account → first workspace → first channel + invites. Centered card layout with progress dots.

### 2. Login
Email + password, OAuth buttons (Google / GitHub / Apple), magic-link toggle, "remember device" checkbox. Centered card on subtle gradient from brand palette.

### 3. Workspace shell — Channel view (the home screen)

```
+----+------------------+------------------------------------------+
| Ⓐ  | ▾ Acme Inc       | # general              · 23 members · ☰ |
| Ⓑ  | + Compose         | topic: company-wide announcements         |
| Ⓒ  | 🔍 Search ⌘K      | ----------------------------------------- |
|----| ▾ Channels      + |  Today                                   |
| ＋ | # general        |                                          |
|    | # design  •      |  alice  10:32                            |
|    | # eng            |  hey team 👋                              |
|    | ▸ More (3)       |    ❤️ 3   👋 1   🚀 1                     |
|    |                  |                                          |
|    | ▾ Direct        + |  bob  10:34   ↳ 2 replies (last 10:36) |
|    | ● alice         | |  morning! coffee chat in 5?            |
|    | ● bob           | |                                          |
|    | ∘ carol         | |  Yesterday                               |
|    |                  |  ...                                     |
|    | ▾ Apps         + |                                          |
|    | 🤖 jenkins-bot   | ----------------------------------------- |
|    | 📅 calendar      | [📎] [Aa] [😊] [/]                        |
|    | ▸ More           |  Type a message in #general...          |
|    |                  |                                          |
|----| ▾ Clients      + |                                          |
| ⚙  | # acme-internal  |                                          |
| 🟢 | [you]  [DND]    |                                          |
+----+------------------+------------------------------------------+
```

Notes: `•` marks channels with unread mentions; bold = unread; `●`/`∘` = online/offline; composer floats above content with slash-command + mention popovers; time formatting respects user TZ.

### 4. Thread view (slide-in details panel)
Right 320px panel slides over content. Thread root pinned at top, replies stack chronologically. Composer at bottom with optional "also send to channel" checkbox.

### 5. Search overlay (cmd+K)
Full-screen overlay with operator chips. Categorized results: Messages, Files, People. ESC dismisses.

### 6. Calendar view
Week + day views; left pane shows user's calendars (SliilS native + Google + Outlook + subscribed channels). Schedule from `/meet` slash command in any channel. Native calendar surface for review/management.

### 7. Meeting in-call
Tile grid auto-balances. Active speaker gets `--sl-green` border. Bottom controls: mic, camera, screen share, hand raise, blur, record. Screen share switches to "presenter + tiles" layout.

### 8. Settings & admin
Tabbed within main pane:
- **Account:** profile, email, password, 2FA, sessions, API tokens, language, time zone, notifications
- **Workspace (admin only):** general, members, roles, channels, retention, audit log, data export, branding, integrations
- **Install (server admin only):** version, license, smtp, push proxy config, storage backend

Standard 2-pane: left nav (groups) + right form. Save-on-change with toast; risky changes require confirm.

### 9. Mobile (Expo + RN)
Native bottom tab bar (3 tabs at v1):
- 💬 **Chats** — DMs + most active channels (single combined feed)
- # **Channels** — full channel tree
- ⋯ **More** — workspace switcher, calendar, search, settings

Composer behaves like iMessage — bottom-anchored, expanding. Threads open as full screens with back swipe. Tapping a meeting link opens the LiveKit RN SDK view.

## Responsive Strategy

- **Desktop-first** (chat is primarily a desktop tool). Breakpoints:
  - `≥ 1280` — full 4-column shell
  - `1024–1280` — details slide overlays, doesn't push
  - `768–1024` — workspace pane collapses to icon-only by default
  - `< 768` — mobile UI takes over (or PWA installable)
- **Mobile UX uses Expo + RN**, not the responsive web. The web app falls back to a "use the app" landing on tiny viewports.

## Motion & Interaction Principles

1. **Restraint** — no decorative animation; motion only signals state change
2. **Speed first** — 120-200ms easing; nothing slower than 250ms
3. **Optimistic feedback** — sent messages appear instantly with a subtle "pending" tick that resolves on ack
4. **Hover doesn't dictate** — every hover-only affordance also reveals via keyboard focus
5. **Reduced motion** — `prefers-reduced-motion: reduce` disables slide/fade; uses opacity-only transitions

## Accessibility (WCAG 2.1 AA)

- **Keyboard nav:** every action reachable; documented shortcut sheet (cmd+/)
- **Screen reader:** ARIA `role="log"` on message list with `aria-live="polite"` for new messages, `assertive` for mentions
- **Focus management:** focus moves into thread sidebar when opened, returns on close
- **Color:** all text meets 4.5:1 contrast; brand accents tested in colorblind simulations (Deuter, Prot, Trit)
- **Skip links:** "Skip to message list," "Skip to composer"
- **Reduced motion:** honored
- **High-contrast theme:** boosts borders + contrast for low-vision users

## Theming

| Theme | Background | Surface | Text | Brand accent |
|---|---|---|---|---|
| Light | `#FBFBFD` | `#FFFFFF` | `--sl-navy` | `--sl-blue` |
| Dark | darker (`#0F1B2A`) | `#1A2D43` | `#E6ECF4` | `--sl-blue` (slightly lighter) |
| High contrast | pure black/white | borders 2px | pure | `--sl-blue` undimmed |

Workspace owners can override `--sl-blue` with a custom hex via Workspace Settings → Branding (with WCAG contrast validator).

## Out of scope for v1 design

- Marketing site (separate effort)
- Onboarding tutorials beyond the setup wizard
- Custom themes beyond brand-color override
- Theme builder / Figma plugin
- Tablet-specific layouts (mobile RN handles iPad acceptably for v1)
