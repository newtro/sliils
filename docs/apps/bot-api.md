# Bot API reference

Endpoints a bot hits with its `slis-xat-...` access token.

## Authentication

All bot endpoints expect:

```
Authorization: Bearer slis-xat-{token_id}-{secret}
```

Token validation is constant-time. Failure = 401.

## POST /api/v1/chat.postMessage

Post a message as the installation's bot user.

**Required scope:** `chat:write`.

### Request

```json
{
  "channel": "123",                  // channel id as string (Slack-shape)
  "text": "Build passed",            // plain-text fallback
  "blocks": [                        // optional Block-Kit blocks
    {
      "type": "header",
      "text": {"type": "plain_text", "text": "Build Report"}
    },
    {
      "type": "section",
      "text": {"type": "mrkdwn", "text": "*Build #427* passed"}
    }
  ],
  "thread_ts": "100"                 // optional parent message id for threading
}
```

Either `text` or `blocks` is required (both is recommended — `text` is the fallback when a renderer doesn't support blocks).

### Response

```json
{ "ok": true, "channel": 123, "message_id": 456 }
```

### Errors

| Status | Cause |
|---|---|
| 400 | Missing channel, both text and blocks empty, malformed Block-Kit shape |
| 401 | Invalid or revoked token |
| 403 | Token lacks `chat:write` |
| 404 | Channel not found in this workspace |

## GET /api/v1/auth.test

Verify a token. Returns the installation + scopes + bot user id, or 401 if the token is invalid.

### Response

```json
{
  "ok": true,
  "app_id": 1,
  "installation_id": 1,
  "workspace_id": 1,
  "bot_user_id": 42,
  "scopes": ["chat:write", "bot"]
}
```

## Rate limits

At v1 the bot API uses the same rate limiter as human users (1000 req/hr/user). A future release adds per-app quotas.

## What's not here (yet)

The Slack `views.*`, `chat.update`, `chat.delete`, `reactions.*`, and `conversations.*` surfaces are in the roadmap for v1.1. If you need one, open an issue describing the use case.
