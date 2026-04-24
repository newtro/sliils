# Webhooks

Two directions:

- **Incoming** — you give a third-party a URL; they POST JSON; it posts a message into a channel. The workflow automation / CI-notification pattern.
- **Outgoing** — your app subscribes to events; we POST them to your URL. Reactive bots.

## Incoming webhooks

### Create one (admin only)

`POST /api/v1/workspaces/:slug/webhooks/incoming`

```json
{
  "name": "grafana alerts",
  "channel_id": 123,
  "require_secret": true
}
```

Response (shown **once**):

```json
{
  "webhook": {
    "id": 1,
    "workspace_id": 1,
    "channel_id": 123,
    "name": "grafana alerts",
    "url": "https://your-sliils/api/v1/hooks/opaque-token-here"
  },
  "signing_secret": "whsec_..."
}
```

### POST a message

```bash
curl -X POST "https://your-sliils/api/v1/hooks/opaque-token-here" \
  -H "Content-Type: application/json" \
  -H "X-SliilS-Request-Secret: whsec_..." \
  -d '{
    "text": "Alert fired on svc-api",
    "blocks": [
      {"type":"header","text":{"type":"plain_text","text":"🔥 Alert"}},
      {"type":"section","text":{"type":"mrkdwn","text":"*svc-api* p99 latency > 2s"}}
    ]
  }'
```

Payload shape:

| Field | Type | Notes |
|---|---|---|
| `text` | string | Optional plain-text body |
| `blocks` | array | Optional [Block-Kit](block-kit.md) blocks |

When `require_secret: true`, the `X-SliilS-Request-Secret` header is mandatory. Optionally carry `X-SliilS-Request-Timestamp` + `X-SliilS-Signature` (HMAC-SHA256) for replay protection.

### Rotate / revoke

Secrets don't rotate. To rotate, `DELETE /api/v1/webhooks/incoming/:id` and create a new one.

## Outgoing webhooks

Subscribe an installed app to workspace events. Deliveries are HMAC-signed and retried on 5xx.

### Create

`POST /api/v1/installations/:id/webhooks/outgoing` (admin only)

```json
{
  "event_pattern": "message.created",
  "target_url": "https://your-app.example.com/webhook"
}
```

`event_pattern` values at v1:

- `message.created`
- `message.updated`
- `message.deleted`
- `reaction.added`
- `reaction.removed`
- `channel.created`
- `member.added`
- `*` — all events

### Delivery shape

```
POST https://your-app.example.com/webhook
Content-Type: application/json
X-SliilS-Request-Timestamp: 1756989123
X-SliilS-Signature: v0=<hmac-sha256-hex>

{
  "event_type": "message.created",
  "workspace_id": 1,
  "data": { ... }
}
```

### Verify the signature

```
base = "v0:" + timestamp + ":" + raw_body
sig  = hmac_sha256(secret, base)
```

Reject if timestamp is more than 5 minutes old. Constant-time compare.

### Retries

- 2xx — success, no retry
- 4xx — logged, no retry (caller is broken)
- 5xx / timeout — exponential backoff, up to 5 attempts over ~15 minutes
