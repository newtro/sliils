# Retention + audit

## Message retention

Workspaces can opt into automatic message purging. When set, a periodic worker soft-deletes (and body-scrubs) messages older than the configured window.

### Set retention

`PATCH /api/v1/workspaces/:slug/admin/settings`

```json
{"retention_days": 90}
```

Pass `"clear_retention": true` to revert to "keep forever."

### What gets purged

- Message `body_md` and `body_blocks` are scrubbed (set to empty)
- The row stays so thread root references + partition layout remain valid
- Attached files are NOT deleted (tracked against file retention separately)
- Audit log entries are NOT retention-scoped — they persist

### Partition pruning

Messages are partitioned monthly. The retention sweep scopes its UPDATE with a `created_at` range so Postgres prunes to exactly the affected partitions. Expect sub-second sweeps for workspaces under a million messages per partition.

## Audit log

Every admin action + invite + install writes a row to `audit_log`. The dashboard viewer shows:

- Actor (display name + email + IP)
- Action (`member.role_changed`, `workspace.settings_updated`, etc.)
- Target (`user:42`, `workspace:1`, etc.)
- Metadata (whatever the handler recorded)
- Timestamp

### API

`GET /api/v1/workspaces/:slug/admin/audit?limit=100&offset=0`

Ordered most-recent first. `limit` is capped at 500. For larger historical reads, run the data export.
