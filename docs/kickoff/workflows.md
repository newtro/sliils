# SliilS — Workflows & Use Cases

Detailed user journeys for v1. Each workflow shows actors, message flow, decision points, and edge cases. Implementing agents and writers should treat these as the source of truth for "how the product works."

## User Types

| Type | Who | Key concerns |
|---|---|---|
| **Install operator (sysadmin)** | Person who deploys SliilS to a VM | Easy install, low ops burden, backups, upgrades |
| **Workspace owner** | Creator of a workspace; first user | Bootstrap, branding, member invites, billing-equivalent (when paid features come) |
| **Workspace admin** | Promoted from member; manages workspace | Member management, channel moderation, retention, audit |
| **Workspace member** | Daily user — most common type | Fast messaging, reliable notifications, search, meetings |
| **Bot / app developer** | 3rd-party building an integration | Clear APIs, signed events, OAuth, install flow |
| **Push proxy operator** | The SliilS project itself (or self-hoster) | Reliability, no-plaintext-leak, scaling |

---

## Workflow A — First-time deployment & workspace bootstrap

```
Install operator                        SliilS server                 First user (becomes owner)
─────────────────                       ─────────────                 ─────────────────────────
1. ssh into AWS VM
2. wget docker-compose.yml
3. edit .env (DOMAIN, SMTP creds)
4. docker compose up -d  ──────────────► boot, run migrations
                                        emit "fresh install" state
5. visit https://sliils.example.com
                                        no workspace exists →
                                        redirect to /setup
                                                                       6. visit /setup
                                                                       7. enter email + password
                                                                          → become first owner
                                                                       8. create workspace
                                                                          (name, slug, logo)
                                                                       9. land in default
                                                                          #general channel
                                                                       10. invite teammates
                                                                          (link or email)
```

**Decision points:** SMTP optional at first (degrades to "share invite link"); first user is implicitly the install owner; bootstrap state cleared once first workspace exists.

**Edge cases:** SMTP misconfigured → invite-link mode + warning banner; subsequent visitors hit normal login (not /setup); operator can pre-seed an admin via CLI flag for unattended provisioning.

---

## Workflow B — Member onboarding (invite → first message)

```
Workspace owner            New member's email          New member            SliilS
────────────────           ─────────────────────       ──────────             ──────
1. /admin/members → Invite
2. paste emails  ────────────────────────────────────────────────────────────►
                            3. invite email arrives
                            4. clicks link  ──────────────────────────────────►
                                                       5. magic-link-style    validate token,
                                                          accept landing      ask for password
                                                          (set password OR    or OAuth choice
                                                          "continue with
                                                          Google/GitHub/Apple")
                                                       6. sign up complete
                                                       7. auto-join default
                                                          channels             post system msg
                                                                              "@new joined"
                                                       8. composer focused
                                                          in #general
                                                       9. types first
                                                          message ───────────► broadcast to
                                                                              channel members
                                                                              over WS
```

**Edge cases:** Invite link expires after 7 days (configurable); rate limit invite-link redemption per IP; if email already exists on this install (Slack-style identity), prompt to "join with existing SliilS account."

---

## Workflow C — Daily messaging (compose → broadcast → reply → react → thread)

```
Sender                      App (web)                  Server                  Other members
──────                      ─────────                  ──────                  ─────────────
1. focused in #general
2. types in TipTap
   composer
3. @-trigger →             show user picker
   completion popup        (Suggestion ext)
4. selects user
5. presses Enter ─────────►
                           build message JSON          INSERT messages
                           optimistic local            (workspace_id RLS)
                           append to                  ──► transactional
                           Virtuoso list                  outbox row for search
                                                       publish to in-process
                                                       pub/sub topic
                                                       "ws:{workspace}:ch:{id}"
                                                                              ──► WS push
                                                                              new message arrives
                                                                              Virtuoso prepends
                                                                              if at-bottom
6. reaction click ───────► optimistic                  upsert reaction row    push delta
                           render local                                       (room only)
7. clicks "reply in                                                           thread sidebar
   thread" on a msg ─────► open thread sidebar         load thread root +
                           inline composer             children paginated
8. types reply ──────────► same flow but with          INSERT w/ thread_root  push to thread
                           parent_id                                          subscribers + room
```

**Edge cases:** WS disconnect → outbox queue; on reconnect, flush outbox + diff-sync since `last_event_id`; rate-limited send → temporary banner, hold in outbox; deleted message in thread → keeps thread structure with "this message was deleted" placeholder.

---

## Workflow D — Multi-workspace switching (Slack-style identity)

```
sesmith2k@gmail.com signed in             SliilS
─────────────────────────────             ──────
sidebar shows workspace switcher          rendered from /me/workspaces
(stacked icons, top-left rail)            joined: [Acme, Beta Co, ClientX]

clicks "Beta Co" icon ──────────────────► PATCH /sessions/current
                                          { workspace_id: beta-co }
                                          → set tenant cookie
                                          → preload Beta Co channels
client navigates to
/w/beta-co/general                        WS reconnects with new
                                          workspace context
                                          RLS now scoped to beta-co
sidebar repopulates with
beta-co channels, DMs, prefs
```

**Decision points:** Workspace switcher is always visible; one current workspace per session per device; per-workspace notification settings; per-workspace status emoji.

**Edge cases:** User removed from a workspace mid-session → 403, gracefully redirect to next workspace; banned user → permanent removal from switcher.

---

## Workflow E — Schedule + host a meeting (calendar → LiveKit room)

```
Member in #project-x        SliilS                      Invitees
──────────────────────      ──────                      ────────
1. types /meet
   in composer ───────────► slash command UI:
                            "Schedule meeting"
                            modal opens (title,
                            time, duration, attendees,
                            recurrence)
2. fills form, submits ───► INSERT events
                            generate event_link
                            schedule reminder jobs (River)
                            push to Google/Outlook
                            for each invitee w/
                            connected calendar         ◄── invite ICS lands
                                                            in their cal

3. event time arrives                                   reminder push fires
                                                        on web/desktop/mobile
4. clicks "Join now"
   in chat or notification ► POST /meetings/{id}/join
                            issue LiveKit JWT
                            (room=evt_{id}, perms=
                             based on invitee role)
                            return room URL
5. browser opens LiveKit
   meeting view             SFU connects                 other invitees join
                            audio/video tracks           same room
6. screen share / blur /
   noise suppression
   handled client-side via
   @livekit/track-processors
7. host clicks "Record" ──► server-side LiveKit Egress
                            captures composite to
                            S3 (SeaweedFS)
                            on completion: post
                            "Recording ready" message
                            in #project-x with link
8. meeting ends             SFU tears down
                            event status → "ended"
                            post summary message
                            (later: AI summary in v1.1)
```

**Edge cases:** Late join → permission still granted; presenter changes screen share mid-call; network drop → auto-rejoin with same JWT (TTL = event end + 1hr); external invitee without SliilS account → "guest" join with limited perms (read-only chat-in-call).

---

## Workflow F — Search

```
Member                       App                         Server                Meilisearch
──────                       ───                         ──────                ───────────
1. cmd+k → search bar
2. types "from:@alice
   has:link in:#design"     parse operators              POST /search
                            translate to Meili filter:   issue tenant token
                            sender_id=alice_id           with workspace=X
                            channel_id=design_id         + visible_to=user
                            has_link=true                                     ──► query w/ token
                                                                                  scoped filter
                                                                                  enforced
                                                                              ◄── results
                            client renders               server hydrates
                            paginated list with          message + channel +
                            highlight, preview,          author from PG
                            jump-to-context              (RLS check)
3. clicks result            navigate to channel +
                            scroll to message,
                            highlight briefly
```

**Edge cases:** Search query before index converges → "still indexing N msgs" banner; user removed from a channel → those msgs no longer in token's `visible_to` filter; permission re-eval on every search (not cached past session).

---

## Workflow G — Install a 3rd-party integration (incoming webhook)

```
Workspace admin              SliilS                    External system
────────────────             ──────                    ───────────────
1. /apps → "Custom
   webhooks" → New
2. picks channel, name,
   icon ─────────────────────► generates webhook URL
                              + signing secret
                              displays once
3. copies URL                                          paste into Jenkins/
                                                       GitHub Actions/etc.

                                                       on event, POST JSON
                                                       w/ HMAC signature ────► verify HMAC
                                                                              parse Block-Kit
                                                                              JSON
                                                                              INSERT message
                                                                              attributed to
                                                                              the webhook bot
                                                                              push via WS
                              channel members see
                              the message inline
                              with bot icon + label
```

**Edge cases:** Invalid HMAC → 401 + audit log entry; rate limit per webhook (e.g., 1 msg/sec sustained, 100 burst); webhook deleted → 404 forever (no soft delete to avoid leaked-secret drama).

---

## Workflow H — Mobile push notification (with push proxy)

```
Sender on web              SliilS server              Push proxy (sliils.com)    APNs/FCM           Recipient (iOS app)
─────────────              ─────────────              ───────────────────────    ────────           ───────────────────
1. sends @-mention
2. broadcast to room  ────► detect @-mention
                            for offline users w/
                            mobile device tokens:
                            POST /push/notify ──────► validate signed
                            { device_token,           request from any
                              opaque_msg_id,          tenant install
                              tenant_id,
                              message_id }            signed JWT for
                                                      APNs / OAuth for
                                                      FCM ─────────────────────► deliver wake-up
                                                                                  ping w/ msg_id
                                                                                                   ◄ wake-up
                                                                                                    silent push
                                                                                                    arrives

                                                                                                    SliilS app fetches
                                                                                                    msg_id from tenant
                                                                                                    server (auth'd)
                                                                                                    over WS or HTTPS
                                                                                                    → renders local
                                                                                                    notification w/
                                                                                                    plaintext content
```

**Privacy invariant:** Push proxy NEVER sees plaintext. Payload is `{ tenant_id, opaque_msg_id, type: "mention" }`. Recipient client decrypts/fetches content from the tenant server.

**Edge cases:** No push credentials configured (self-hoster opted out) → fallback to in-app inbox + email digest; invalid device token (uninstalled app) → APNs/FCM returns "not registered" → SliilS deletes token; UnifiedPush distributor (Android FOSS users) bypasses proxy entirely → direct VAPID web push.

---

## Workflow I — Office doc collaboration

```
Member uploads .docx       SliilS                     Collabora Online       Co-editor
──────────────────────     ──────                     ──────────────────     ─────────
1. drag-drop docx into
   composer ──────────────► presign upload
                            client → SeaweedFS
                            on success, INSERT
                            files row + post msg
                            with attachment block
2. clicks "Open in
   Collabora" ─────────────► server generates WOPI
                              token w/ access scope
                              (workspace, file, user)
                              redirect to Collabora
                                                       fetch file via WOPI
                                                       endpoint on SliilS
                                                       (auth via token)
                                                       open editor in iframe
                                                                              ◄── second user
                                                                                  clicks same
                                                                                  attachment
                                                                              they get a separate
                                                                              WOPI token, join
                                                                              same Collabora
                                                                              session → live
                                                                              co-edit with cursors
3. saves                                               PUT back to SliilS
                                                       via WOPI → new
                                                       version row
                                                       optionally post
                                                       "Alice updated foo.docx"
                                                       in originating channel
```

**Edge cases:** Collabora down → "Collaborative editing unavailable, download to edit" fallback; large file → preview-only mode; permission revoke mid-edit → WOPI token next-fetch denies, user sees "session expired."

---

## Workflow J — Voice/video calls (1:1 and group)

**1:1 from a DM:**
```
Alice in DM with Bob       SliilS                     LiveKit
────────────────────       ──────                     ───────
1. clicks call icon ─────► create ephemeral room
                            room=dm_{alice}_{bob}
                            issue Alice JWT
2. ringing UI shown
                            push notification to
                            Bob's devices
3. Bob accepts ──────────► issue Bob JWT
4. both join room                                     SFU routes A/V
5. either hangs up                                    SFU tears down room
                            post "Call ended,
                            duration 7m12s" in DM
```

**Group call from a channel:**
- Channel banner shows "Start a call" → creates room, broadcast in-channel notification
- Persistent room while ≥1 user is in it; auto-destroy when empty for 30s
- Roster shown in channel sidebar while active

**Edge cases:** Call declined → "Bob declined" message; both users disconnected briefly → JWT remains valid for ~10min for rejoin; recording can only be initiated by call host or admins.

---

## Workflow K — Workspace admin tasks

| Task | Path | Side effects |
|---|---|---|
| Invite members | `/admin/members → Invite` | Generates invite link or sends emails; pending entries in members table |
| Change role | `/admin/members → ⋯ → Change role` | Updates `workspace_memberships.role`; audit log entry |
| Deactivate user | `/admin/members → ⋯ → Deactivate` | Sets `users.deactivated_at`; revokes sessions; preserves messages |
| Archive channel | `Channel → ⋯ → Archive` | Read-only state; hidden from default browse; restorable |
| Set retention policy | `/admin/policies → Retention` | Background job purges messages > N days; per-channel override allowed |
| Export workspace data | `/admin/data → Export` | Async job builds zip in S3, emails admin when ready |
| View audit log | `/admin/audit` | Filterable timeline of admin actions, auth events, integration installs |

---

## Workflow L — GDPR per-user data export

```
Member                     SliilS
──────                     ──────
1. /me/privacy →
   "Export my data" ────► enqueue River job
                          (export_user_data.{id})
                          show "We'll email you when ready"
2. ... (background)        worker queries:
                          - profile + sessions
                          - messages authored (by workspace)
                          - files uploaded
                          - reactions given
                          - DM history
                          builds JSON + media archive
                          uploads to short-lived S3 link
3. email arrives:
   "Your export is ready"
   click link ──────────► presigned URL serves
                          archive directly from
                          SeaweedFS (1-hour TTL)
```

**Edge cases:** Export link expired → request a new one; partial corrupt data → admin alert; per-workspace partitioned exports respect each workspace's retention policy.

---

## Workflow M — Cross-cutting offline / reconnect behavior

When the WS connection drops (mobile network drop, sleep, server restart):

1. Client detects WS close, switches to "offline" indicator in chrome
2. Composes still possible — any sent message goes to IndexedDB outbox
3. Reads continue from IndexedDB cache; "showing cached" banner if stale beyond 30s
4. Reconnect attempts with exponential backoff (1s, 2s, 5s, 10s, 30s, 60s capped)
5. On reconnect:
   - Auth token refresh if expired
   - Diff-sync: client sends `last_event_id` per channel; server replays missed events
   - Outbox flushes serially; any failed sends remain marked "failed - retry"
6. Banner clears, "online" restored

---

## Workflow N — Bot / app developer flow

```
Developer                  SliilS dev portal          External app
─────────                  ─────────────────          ────────────
1. /dev/apps → New App ──► registers app
                            (name, redirect URI,
                             scopes, manifest)
                            issues client_id +
                            client_secret
2. user installs app ────► OAuth 2.0 PKCE flow
   in their workspace      consent screen w/ scopes
                          access_token issued
                          (workspace + user scoped)
3. external app calls ──────────────────────────────► uses token to
   chat.postMessage                                    POST messages,
                                                       subscribe to events,
                                                       open modals
4. event subscriptions  ◄── HMAC-signed POST
                            to app's webhook URL
                            on every matching event

5. modal interactions  ──► trigger_id flow:
                            user clicks button →
                            payload sent to app →
                            app calls views.open →
                            modal renders
```

---

## Cross-Reference: Patterns Borrowed / Avoided

| Pattern | Source | Why we adopt / avoid |
|---|---|---|
| Channel + thread + DM model | Slack | Universally familiar, anchor of our IA |
| Multi-workspace switcher (left rail) | Slack | Matches our identity-spans-workspaces model |
| Block Kit JSON UI | Slack | Best ecosystem; cloning the JSON shape unlocks dev familiarity |
| Streams + topics | Zulip | Avoid — too cognitively expensive for new users; Slack threading is good enough |
| Servers + voice rooms | Discord | Avoid — wrong vibe for "work" positioning |
| Federation | Matrix | Explicitly out of scope |
| Meeting tabs in channels | Teams | Adopt — calendar + meeting links inline in channel matches our use case |
| In-process plugin SDK | Mattermost | Avoid — they're deprecating it; out-of-process HTTPS apps only |
| Recording via Jibri (headless Chrome) | Jitsi | Avoid — heavy; LiveKit Egress (GStreamer) is cleaner |
