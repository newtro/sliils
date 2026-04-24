# OAuth 2.0 + PKCE

SliilS implements the standard authorization-code + PKCE flow. The endpoints and parameter names match [RFC 6749](https://datatracker.ietf.org/doc/html/rfc6749) and [RFC 7636](https://datatracker.ietf.org/doc/html/rfc7636), so existing OAuth client libraries work unchanged.

## Flow diagram

```
┌─────────┐                ┌──────────┐                ┌──────────┐
│ Browser │                │  SliilS  │                │ Your app │
│ (admin) │                │          │                │ backend  │
└────┬────┘                └────┬─────┘                └────┬─────┘
     │                          │                           │
     │ 1. Admin clicks          │                           │
     │    "Install to SliilS"   │                           │
     ├──────────────────────────┼──────────────────────────►│
     │                          │                           │
     │ 2. Browser redirects to  │                           │
     │    /oauth/authorize      │                           │
     │    with client_id,       │                           │
     │    code_challenge, etc.  │                           │
     │◄─────────────────────────┼───────────────────────────┤
     │                          │                           │
     │ 3. Admin authenticates + │                           │
     │    approves              │                           │
     ├─────────────────────────►│                           │
     │                          │                           │
     │ 4. SliilS redirects back │                           │
     │    with ?code=...        │                           │
     │◄─────────────────────────┤                           │
     │                          │                           │
     ├──────────────────────────┼──────────────────────────►│
     │ 5. Browser lands on      │                           │
     │    your callback URL     │                           │
     │                          │                           │
     │                          │◄──────────────────────────┤
     │                          │ 6. Your backend POSTs     │
     │                          │    /oauth/token with code │
     │                          │    + code_verifier        │
     │                          │                           │
     │                          ├──────────────────────────►│
     │                          │ 7. Access token           │
     │                          │                           │
```

## Authorize endpoint

`GET /api/v1/oauth/authorize` — authenticated (the admin must already be logged in).

| Parameter | Required | Description |
|---|---|---|
| `client_id` | yes | The app's stable ID (begins with `slis-app-`) |
| `redirect_uri` | yes | Must exactly match one registered in the manifest |
| `scope` | yes | Space-separated list; every scope MUST be in the manifest |
| `code_challenge` | yes | PKCE challenge, base64url(sha256(verifier)) |
| `code_challenge_method` | no | `S256` (default) or `plain` (dev-tool escape hatch) |
| `workspace_slug` | yes | Which workspace to install into |
| `state` | no | Passed through to the redirect for CSRF protection |

**Auth requirement:** the authenticated user MUST be `owner` or `admin` of the workspace. Non-admins get 403.

**Response:** 200 with `{"redirect_to": "<url>"}` — the client follows this URL (typically done inline by a React SPA).

## Token endpoint

`POST /api/v1/oauth/token` — unauthenticated (credentials are in the body).

Content type: `application/x-www-form-urlencoded`.

| Parameter | Required | Description |
|---|---|---|
| `grant_type` | yes | `authorization_code` |
| `code` | yes | From the authorize redirect |
| `redirect_uri` | yes | Must match what was sent to `/authorize` |
| `client_id` | yes | |
| `code_verifier` | one-of | PKCE verifier (public clients / SPAs) |
| `client_secret` | one-of | Confidential client secret (server-to-server apps) |

**Either `code_verifier` OR `client_secret` is required.** If both are present, `client_secret` wins — a compromised verifier can't downgrade a confidential client.

### Success response

```json
{
  "access_token": "slis-xat-1-abc...",
  "token_type": "Bearer",
  "scope": "chat:write bot",
  "workspace_id": 1,
  "installation_id": 1,
  "bot_user_id": 42,
  "app_id": 1
}
```

### Error responses

| Status | Cause |
|---|---|
| 400 | Missing / malformed parameter, unknown client_id, redirect_uri mismatch, code already used or expired, `grant_type` not `authorization_code` |
| 401 | Invalid client_secret, PKCE verification failed |

## Token format

```
slis-xat-{token_id}-{secret}
```

- `slis-xat-` is a stable, grep-detectable prefix that secret scanners (GitHub, TruffleHog) can flag.
- `token_id` is the DB row id. Used for O(1) lookup.
- `secret` is a 32-byte base64url string. Only its SHA-256 hash is stored server-side; constant-time comparison on verify.

Treat the token like a password. Never log it, never check it into git, never share it cross-tenant.

## Scopes

Granted scopes at v1:

| Scope | Grants |
|---|---|
| `chat:write` | Post messages as the bot user |
| `channels:read` | List public channels + membership |
| `channels:history` | Read message history in channels the bot is added to |
| `users:read` | List workspace members |
| `commands` | Respond to slash commands |
| `incoming-webhook` | Receive an incoming webhook URL at install time |
| `bot` | Create a bot user in the workspace |

Every scope you request in `/authorize` MUST be declared in the manifest. Attempting to request a scope outside the manifest returns 400.

## Code single-use + PKCE

Each authorization code:

- is 32 bytes of random base64url (opaque)
- is single-use (replay returns 400)
- expires after 10 minutes
- is bound to the issuing PKCE challenge — the matching verifier MUST be presented at token exchange

## Revocation

`DELETE /api/v1/installations/:id` (admin-only) revokes an installation. All its tokens immediately fail the middleware check (it refuses revoked installs).
