# Members + roles

Workspace admins manage members from the admin dashboard (`/w/:slug/admin/members` in the web app) or via the API.

## Roles

| Role | Can |
|---|---|
| `owner` | Everything, including changing other owners' roles + workspace deletion |
| `admin` | Member management, retention, branding, app installs, audit log |
| `member` | Normal user; create channels + DMs + files |
| `guest` | Constrained access (v1.1) |

Every workspace must have at least one owner. Self-demoting from the last-owner state returns 409.

## Change a role

`PATCH /api/v1/workspaces/:slug/admin/members/:uid`

```json
{"role": "admin"}
```

Fires a `member.role_changed` audit event.

## Deactivate

`DELETE /api/v1/workspaces/:slug/admin/members/:uid`

- Soft-deletes the membership (keeps history)
- Can't deactivate yourself — have another owner do it
- Fires `member.deactivated` audit event

## Reactivate

Re-invite the user via the normal invite flow. The existing soft-deleted membership is reactivated (ON CONFLICT upsert) rather than creating a duplicate.
