# Block-Kit

SliilS accepts [Slack's Block-Kit JSON shape](https://api.slack.com/block-kit) verbatim. Paste Slack examples and they work.

## Supported block types (v1)

| Type | Purpose |
|---|---|
| `section` | Rich text + optional accessory (button, image) |
| `divider` | Visual rule |
| `header` | Big section title — `plain_text` only |
| `image` | Image URL + alt text |
| `context` | Small meta text ("Posted by @alice 2m ago") |
| `actions` | A row of interactive elements (buttons, selects) |

Unknown block types are preserved in JSON but may render as plain text. Safe to paste from Slack app manifests that use newer types.

## Validation limits

- Max **50 top-level blocks** per message (Slack parity)
- Max **3000 characters** per text object
- Text objects must be `plain_text` or `mrkdwn`
- `header` requires `plain_text` (not `mrkdwn`)
- `section` requires either `text` or `fields`

Violations → HTTP 400 with a descriptive error.

## Example: alert with action buttons

```json
{
  "channel": "123",
  "text": "Incident: high API latency",
  "blocks": [
    {"type":"header","text":{"type":"plain_text","text":"🔥 Incident"}},
    {"type":"section","fields":[
      {"type":"mrkdwn","text":"*Service*\napi"},
      {"type":"mrkdwn","text":"*Severity*\np1"},
      {"type":"mrkdwn","text":"*Since*\n2 min ago"},
      {"type":"mrkdwn","text":"*Owner*\n@on-call"}
    ]},
    {"type":"actions","elements":[
      {"type":"button","text":{"type":"plain_text","text":"Acknowledge"},"style":"primary","value":"ack_123"},
      {"type":"button","text":{"type":"plain_text","text":"Escalate"},"style":"danger","value":"esc_123"},
      {"type":"button","text":{"type":"plain_text","text":"Open runbook"},"url":"https://wiki/runbook"}
    ]}
  ]
}
```

## Interactive actions

Button clicks and select changes in `actions` blocks fire an `interaction.invoked` event which your app receives via an outgoing webhook (coming in v1.1 — the protocol surface exists, the delivery path lands with interactive views). Track [the GitHub issue](https://github.com/newtro/sliils/issues) for progress.
