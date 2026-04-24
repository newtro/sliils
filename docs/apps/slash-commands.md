# Slash commands

Commands the user types in the composer (`/poll`, `/jira create`, etc.) that route to your app's HTTPS endpoint.

## Register

`POST /api/v1/installations/:id/slash-commands` (admin only)

```json
{
  "command": "/poll",
  "target_url": "https://your-app.example.com/slash/poll",
  "description": "Run a quick poll",
  "usage_hint": "[question] [option 1] [option 2] ..."
}
```

Constraints:

- `command` must start with `/` and be globally unique per workspace
- `target_url` must be `http://` or `https://` (use an opaque slug in the path — no auth header is sent with invocations at v1)

Returns a `signing_secret` **once** — keep it to verify future invocations.

## Invocation shape

When a user runs `/poll "Best snack?" crisps cheese pizza`, we POST to your `target_url`:

```
POST /slash/poll
Content-Type: application/json
X-SliilS-Request-Timestamp: 1756989123

{
  "command": "/poll",
  "text": "\"Best snack?\" crisps cheese pizza",
  "user_id": "42",
  "channel_id": "123",
  "workspace_id": "1"
}
```

## Response shape

Your handler should return JSON within 10 seconds:

```json
{
  "response_type": "ephemeral",
  "text": "Poll created!",
  "blocks": [
    {"type":"section","text":{"type":"mrkdwn","text":"*Best snack?*"}},
    {"type":"actions","elements":[
      {"type":"button","text":{"type":"plain_text","text":"Crisps"},"value":"crisps"},
      {"type":"button","text":{"type":"plain_text","text":"Cheese"},"value":"cheese"},
      {"type":"button","text":{"type":"plain_text","text":"Pizza"},"value":"pizza"}
    ]}
  ]
}
```

- `response_type: "ephemeral"` — only the invoking user sees the response
- `response_type: "in_channel"` — everyone in the channel sees it

Empty 200 responses fall back to an ephemeral "ok" acknowledgement.

## Delete

`DELETE /api/v1/slash-commands/:id` (admin only)

## HMAC signing (v1.1)

Slash command delivery currently relies on an opaque `target_url` for auth. HMAC signing lands in v1.1 — same shape as [outgoing webhook signatures](webhooks.md).
