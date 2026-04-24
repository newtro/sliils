# API reference

The full endpoint list, grouped by surface. All endpoints live under `/api/v1/` and return JSON. Errors are [RFC 7807 Problem Details](https://datatracker.ietf.org/doc/html/rfc7807).

## Conventions

- **Auth:** `Authorization: Bearer <jwt>` (human users) or `Authorization: Bearer slis-xat-...` (apps)
- **Errors:** `{"type":"https://sliils.com/problems/400","title":"Bad request","status":400,"detail":"..."}`
- **Pagination:** `limit` + `offset` query params (caps vary by endpoint)
- **Timestamps:** RFC 3339 with timezone

## Auth

| Method | Path | Notes |
|---|---|---|
| POST | `/auth/signup` | Create user + session |
| POST | `/auth/login` | Password login |
| POST | `/auth/logout` | Revoke current session |
| POST | `/auth/refresh` | Exchange refresh cookie for new access token |
| POST | `/auth/magic-link` | Email a signin link |
| POST | `/auth/magic-link/consume` | Consume a magic-link token |
| POST | `/auth/verify-email` | Verify email via token |
| POST | `/auth/forgot-password` | Send password-reset email |
| POST | `/auth/reset-password` | Reset via token |

## Me

| Method | Path | Notes |
|---|---|---|
| GET | `/me` | Current user profile |
| PATCH | `/me` | Update display_name |
| GET | `/me/workspaces` | Workspaces I belong to |
| POST | `/me/devices` | Register push device |
| GET | `/me/devices` | List my push devices |
| DELETE | `/me/devices/:id` | Unregister a device |
| PATCH | `/me/dnd` | Set snooze / quiet hours |
| GET | `/me/push-public-key` | VAPID public key |
| PATCH | `/me/workspaces/:slug/status` | Per-workspace custom status |
| PATCH | `/me/workspaces/:slug/notify-pref` | Per-workspace notification preference |

## Workspaces + channels + messages

| Method | Path | Notes |
|---|---|---|
| POST | `/workspaces` | Create workspace |
| GET | `/workspaces/:slug` | Workspace detail |
| GET | `/workspaces/:slug/channels` | List channels |
| POST | `/workspaces/:slug/channels` | Create channel |
| POST | `/workspaces/:slug/channels/:id/members` | Add member |
| GET | `/channels/:id/messages` | List messages (cursor paginated) |
| POST | `/channels/:id/messages` | Post message |
| PATCH | `/messages/:id` | Edit |
| DELETE | `/messages/:id` | Delete |
| POST | `/messages/:id/reactions` | Add reaction |

## Search

| Method | Path | Notes |
|---|---|---|
| POST | `/search` | Full-text + operator search |

## Calendar + meetings

| Method | Path | Notes |
|---|---|---|
| GET | `/workspaces/:slug/events` | Events in date range (RRULE-expanded) |
| POST | `/workspaces/:slug/events` | Create event |
| PATCH | `/events/:id` | Update |
| DELETE | `/events/:id` | Cancel |
| POST | `/events/:id/rsvp` | RSVP yes/no/maybe |
| POST | `/events/:id/join` | Bridge to meetings |
| POST | `/meetings/:id/join` | Issue LiveKit JWT |
| POST | `/meetings/:id/end` | End call |

## Pages (M10)

| Method | Path | Notes |
|---|---|---|
| GET | `/workspaces/:slug/pages` | List pages |
| POST | `/workspaces/:slug/pages` | Create page |
| GET | `/pages/:id` | Page metadata |
| PATCH | `/pages/:id` | Rename / move / re-icon |
| DELETE | `/pages/:id` | Archive |
| POST | `/pages/:id/auth` | Issue Y-Sweet client token |
| GET | `/pages/:id/snapshots` | Version history |
| POST | `/pages/:id/snapshots` | Manual snapshot |
| POST | `/pages/:id/snapshots/:sid/restore` | Restore a snapshot |
| GET | `/pages/:id/comments` | List comments |
| POST | `/pages/:id/comments` | Post comment |

## Files + WOPI

| Method | Path | Notes |
|---|---|---|
| POST | `/workspaces/:slug/files` | Upload (multipart) |
| GET | `/files/:id/raw` | Download (authenticated) |
| POST | `/files/:id/edit-session` | Start a Collabora session |
| GET | `/wopi/files/:id` | WOPI CheckFileInfo (Collabora callback) |

## Apps platform (M12)

| Method | Path | Notes |
|---|---|---|
| GET/POST | `/dev/apps` | Developer portal |
| PATCH/DELETE | `/dev/apps/:slug` | |
| POST | `/dev/apps/:slug/rotate-secret` | |
| GET | `/oauth/authorize` | OAuth install |
| POST | `/oauth/token` | Token exchange |
| POST | `/chat.postMessage` | Bot posts a message |
| GET | `/auth.test` | Verify a bot token |
| POST | `/workspaces/:slug/webhooks/incoming` | |
| POST | `/hooks/:token` | Public receiver |
| POST | `/installations/:id/webhooks/outgoing` | |
| POST | `/installations/:id/slash-commands` | |
| POST | `/workspaces/:slug/slash-commands/invoke` | |

## Admin (M12)

| Method | Path | Notes |
|---|---|---|
| GET | `/workspaces/:slug/admin/members` | |
| PATCH/DELETE | `/workspaces/:slug/admin/members/:uid` | |
| GET | `/workspaces/:slug/admin/audit` | |
| PATCH | `/workspaces/:slug/admin/settings` | |
| POST | `/workspaces/:slug/admin/export` | Workspace ZIP |
| GET | `/workspaces/:slug/admin/export/user/:uid` | GDPR per-user ZIP |
