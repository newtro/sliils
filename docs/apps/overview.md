# Apps platform overview

SliilS's apps platform is the extensibility layer that lets developers (and internal teams) add functionality to a workspace without touching the core codebase. The API shape deliberately mirrors Slack's, so existing Slack app SDKs work with a base-URL swap.

## What you can build

| Pattern | How |
|---|---|
| **CI / deploy notifications** | An incoming webhook. Your build server POSTs JSON, it becomes a message. |
| **Alerts with action buttons** | Incoming webhook + [Block-Kit](block-kit.md) interactive blocks. |
| **Ticket integrations** | An OAuth-installed app with a bot user + [slash command](slash-commands.md) (`/jira create`). |
| **Internal AI assistant** | An outgoing [webhook](webhooks.md) subscribed to `message.created` events. |
| **Standup / poll bots** | Slash command + bot API to post aggregated results. |

## Primitives

<div class="grid cards" markdown>

- **Apps**
    
    The package a developer creates once. Carries the manifest (scopes + redirect URIs + slash commands + webhook subscriptions). Has a stable `client_id` and a rotatable `client_secret`.

- **Installations**
    
    A specific workspace's install of an app. Carries the granted scopes + an optional bot user. OAuth install URL lands the workspace admin on SliilS, who either approves or denies.

- **Bots**
    
    Synthetic users the app posts as. Appear in conversations like regular users (avatar, name) but are attributed on messages via `author_bot_installation_id` so admins can see "which app posted this."

- **Incoming webhooks**
    
    An admin-created URL (`https://your-sliils/api/v1/hooks/{token}`) that accepts JSON POSTs and posts a message into a specific channel. Optional shared-secret verification.

- **Outgoing webhooks**
    
    App-installation-scoped subscriptions. When an event matching `event_pattern` happens in the workspace, we HMAC-sign a JSON body and POST it to the app's `target_url`.

- **Slash commands**
    
    Workspace-unique commands (`/poll`, `/jira create`). When a user types one, SliilS forwards a Slack-shaped payload to the app and renders the app's response.

</div>

## The Signal-style push promise

When a bot sends a message that would trigger a native push notification to a mobile user, we never include the message body in the push payload. The payload is opaque (`{msg_id, type, tenant_url}`); the recipient's signed-in app fetches the real content from the tenant server. A compromised mobile-push infrastructure never sees plaintext.

## What's NOT in v1

These are explicit scope-outs the roadmap lists for v1.0:

- A browseable marketplace UI (v1.1)
- Interactive views (`views.open`, `views.update`, modal dialogs) — the protocol surface exists but the UI is v1.1
- Public app directory for non-owner discovery

## Next

- [Quickstart](quickstart.md) — build a "hello world" bot in 10 minutes
- [OAuth + install](oauth.md) — the full authorization-code + PKCE flow
- [Bot API reference](bot-api.md) — `/chat.postMessage`, `/auth.test`
- [Webhooks](webhooks.md), [Slash commands](slash-commands.md), [Block-Kit](block-kit.md)
