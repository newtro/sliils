# Apps quickstart

Build a "hello world" bot that posts into a channel. End to end in about 10 minutes.

## 1. Create an app

As the developer (any authenticated SliilS user), POST to `/dev/apps`:

```bash
curl -X POST http://localhost:8080/api/v1/dev/apps \
  -H "Authorization: Bearer $MY_SESSION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "slug": "hello-bot",
    "name": "Hello Bot",
    "description": "A minimal example bot",
    "manifest": {
      "scopes": ["chat:write", "bot"],
      "redirect_uris": ["http://localhost:3000/cb"],
      "bot_user": {"display_name": "Hello"}
    }
  }'
```

The response includes your `client_id` (shareable) and `client_secret` (shown **once**, store it).

## 2. Install the app into a workspace

As a workspace admin, visit this URL in a browser (with an active session):

```
http://localhost:8080/api/v1/oauth/authorize
  ?client_id=slis-app-...
  &redirect_uri=http://localhost:3000/cb
  &scope=chat:write+bot
  &code_challenge=<PKCE_CHALLENGE>
  &code_challenge_method=S256
  &workspace_slug=my-workspace
  &state=random-csrf-value
```

Generate the PKCE challenge:

```bash
# verifier is a 43-128 char url-safe random string
VERIFIER="dBjftJeZ4CVP-mB92K27uhbUJU1p1r-wW1gFWFOEjXk"
# challenge = base64url(sha256(verifier)) with no padding
CHALLENGE=$(printf %s "$VERIFIER" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
echo "$CHALLENGE"
```

The server returns `{"redirect_to": "http://localhost:3000/cb?code=...&state=..."}`. Extract the `code`.

## 3. Exchange the code for an access token

Your app's backend calls the token endpoint:

```bash
curl -X POST http://localhost:8080/api/v1/oauth/token \
  -d "grant_type=authorization_code" \
  -d "code=<THE_CODE_FROM_STEP_2>" \
  -d "redirect_uri=http://localhost:3000/cb" \
  -d "client_id=slis-app-..." \
  -d "code_verifier=$VERIFIER"
```

Response:

```json
{
  "access_token": "slis-xat-1-abc123...",
  "token_type": "Bearer",
  "scope": "chat:write bot",
  "workspace_id": 1,
  "installation_id": 1,
  "bot_user_id": 42,
  "app_id": 1
}
```

Store this token. It's long-lived; revoke via `DELETE /api/v1/installations/:id` or rotate by uninstalling + reinstalling.

## 4. Post a message

```bash
curl -X POST http://localhost:8080/api/v1/chat.postMessage \
  -H "Authorization: Bearer slis-xat-1-abc123..." \
  -H "Content-Type: application/json" \
  -d '{
    "channel": "1",
    "text": "Hello from my bot!",
    "blocks": [
      {
        "type": "section",
        "text": {"type": "mrkdwn", "text": "*Hello* from my bot!"}
      }
    ]
  }'
```

Response:

```json
{"ok": true, "channel": 1, "message_id": 99}
```

That's it. Your bot now lives in the workspace and posts as its synthetic user. From here:

- Add a [slash command](slash-commands.md) so users can invoke your bot on demand
- Subscribe to [outgoing webhooks](webhooks.md) to react to workspace events
- Add [interactive Block-Kit buttons](block-kit.md) for richer UIs
