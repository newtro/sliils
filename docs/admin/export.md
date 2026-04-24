# Data export

Two flavours:

## Workspace export (for backup or migration)

`POST /api/v1/workspaces/:slug/admin/export` — admin only.

Streams a ZIP of:

- `workspace.json` — basic metadata
- `members.json` — roster (id, email, name, role, joined_at)
- `channels.json` — channel list
- `messages.ndjson` — one JSON message per line (so huge exports don't need to fit in RAM)

Returns `Content-Type: application/zip`. Consume with `curl -o export.zip`.

Fires a `workspace.export` audit event.

## Per-user GDPR export (Subject Access Request)

`GET /api/v1/workspaces/:slug/admin/export/user/:uid` — admin only.

Streams a ZIP of:

- `profile.json` — user row (email, display_name, email_verified_at)
- `messages.ndjson` — every message the user authored in this workspace

Designed to satisfy a GDPR Article 15 Subject Access Request in one admin click. Fires a `user.export` audit event.

## Scale notes (v1)

Both endpoints stream inline with the HTTP response. Fine up to ~10GB workspaces.

Larger deploys: we plan to move export to an async River job that uploads the zip to SeaweedFS and emails the admin a link when ready. Tracked for v1.1.
