# WebSocket protocol

Single connection per logged-in client. Handles the entire realtime surface (messages, reactions, presence, typing, calls, calendar reminders, page edits, push events).

## Connection

```
wss://your-host/api/v1/socket?token=<access_jwt>
```

Token is passed as a query param since browsers can't set headers on the WS handshake. Validated the same way the HTTP middleware validates — same `auth.TokenIssuer.Parse` call.

## Frame shape

```json
{
  "v": 1,
  "type": "message.created",
  "id": "client-msg-id (optional)",
  "data": {...}
}
```

## Server → client events

| `type` | Fired on |
|---|---|
| `hello` | Initial handshake; carries capabilities + ping interval + last server event id |
| `message.created` | New message in a subscribed channel |
| `message.updated` | Edit |
| `message.deleted` | Soft delete |
| `reaction.added` / `reaction.removed` | |
| `presence.changed` | Active / away / DND |
| `typing.started` / `typing.stopped` | |
| `channel.created` / `channel.updated` / `channel.archived` | |
| `member.added` / `member.removed` | Workspace membership |
| `mention.created` | User mentioned in a message |
| `event.upcoming` | Calendar reminder 4-6 min before start |
| `meeting.started` / `meeting.ended` | LiveKit room lifecycle |
| `page.created` / `page.updated` / `page.archived` | |
| `page.comment.added` | |
| `page.restored` | Snapshot restored |
| `error` | Framing error; client should reconnect |

## Client → server messages

| `type` | Purpose |
|---|---|
| `subscribe` | Add a channel topic (membership-checked server-side) |
| `unsubscribe` | Remove topic |
| `typing.heartbeat` | Ongoing typing indicator |
| `presence.set` | active / away / dnd |
| `ack` | Reports last_event_id for diff-sync |
| `ping` | Liveness |

## Reconnect / diff-sync

On reconnect the client provides `?since=<event_id>`. The server replays missed events from an in-process 5-minute buffer, or returns `{"must_full_resync": true}` when the gap is too large.

## Topics

Events are published into scoped topics; the subscribing client is filtered at publish time by the `realtime.Broker`:

- `workspace:<id>` — workspace-wide events (presence, member changes, workspace-level fanouts for mentions/incoming-calls)
- `channel:<ws_id>:<ch_id>` — message-level events
- `user:<id>` — per-user direct fanouts
- `dm:<id>` — DM room events

Clients never subscribe to raw topic names; they subscribe to channel ids + we translate internally. The user_id-keyed channel filter prevents cross-tenant leakage.
