# Security checklist

Sign off on every item before exposing SliilS to the internet. Treat this as a living document — open a PR when you think something is missing.

## Must-do before first public use

### Secrets

- [ ] `SLIILS_JWT_SIGNING_KEY` is ≥ 32 random bytes (`openssl rand -hex 32`)
- [ ] `.env` is NOT in version control (it's in `.gitignore` — verify via `git ls-files | grep .env`)
- [ ] `SLIILS_COOKIE_SECURE=true` in production
- [ ] `SLIILS_COOKIE_SAMESITE=strict` in production
- [ ] VAPID private key is in env only, never logged
- [ ] `SLIILS_CALENDAR_ENCRYPTION_KEY` set (if external calendar sync enabled)

### Database

- [ ] Postgres on an encrypted volume (LUKS / EBS / equivalent)
- [ ] Runtime DB role is `sliils_app` (non-owner) — the role has no bypass for `FORCE ROW LEVEL SECURITY`
- [ ] Owner pool (for migrations + workers) uses a separate role with least-necessary privilege
- [ ] Automated nightly `pg_dump` to a separate host
- [ ] Tested a full restore at least once

### Network

- [ ] All inbound HTTPS via Caddy with valid cert (HSTS preloaded for public installs)
- [ ] TURN server (Coturn) behind auth, not open-relay
- [ ] Internal service ports (5432, 7700, 7880, 8787) firewalled off from public
- [ ] WebSocket endpoint upgrades gated on JWT

### App

- [ ] Every tenant table has `FORCE ROW LEVEL SECURITY` on (see migrations — this is already true for everything v1 ships)
- [ ] Webhook signing secrets for production integrations are stored server-side, never in client code
- [ ] Push proxy JWT (if using) is scoped to this install
- [ ] OAuth app `client_secret` values rotated on developer departure

### Monitoring

- [ ] `/readyz` hits every expected sidecar
- [ ] Postgres slow-query log enabled (> 1s)
- [ ] River job failure rate alerted (catches stuck search indexing + push delivery)
- [ ] Audit log shipped to long-term storage (cheaper than keeping it in Postgres forever)

## Nice-to-have before scale

- [ ] Postgres read replica for analytics
- [ ] Separate VM for LiveKit if calls load is non-trivial
- [ ] WAF in front of Caddy (Cloudflare, AWS WAF) with rate-limit rules for auth endpoints

## Incident response

- [ ] Admin credentials rotation runbook
- [ ] Token-leak playbook: `UPDATE app_tokens SET revoked_at = now() WHERE token_id = ...`
- [ ] User-deactivate playbook: `DELETE /workspaces/:slug/admin/members/:uid`
- [ ] Data-export playbook for Subject Access Requests

## Audit trail

Audit log captures:

- Authentication (login, login_failure, password_reset, magic_link_used)
- Workspace CRUD (created, settings_updated, archived)
- Membership changes (invite_sent, invite_accepted, role_changed, deactivated)
- App lifecycle (app_installed, app_uninstalled, token_issued, token_revoked)
- Data access (workspace_exported, user_exported)

Retention: `audit_log` is NOT scoped to workspace retention policy — it persists independently until manually purged.
