// Admin dashboard (M12-P3). Mounts at /w/:slug/admin.
//
// Workspace owners/admins manage people, workspace settings, retention,
// and the audit log here. Invite flow lives on the Members tab where it
// makes contextual sense.

import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiError, apiFetch } from '../api/client';
import {
  generateVAPIDKeys,
  getInstallEmail,
  getInstallInfrastructure,
  getRestartStatus,
  getSignupMode,
  getWorkspaceEmail,
  patchInstallEmail,
  patchInstallInfrastructure,
  patchWorkspaceEmail,
  restartServer,
  setSignupMode,
  testWorkspaceEmail,
} from '../api/install';
import type {
  InstallEmailConfig,
  InstallInfrastructure,
  PatchInstallInfrastructure,
  SignupMode,
  WorkspaceEmailConfig,
} from '../api/install';
import { listMyWorkspaces } from '../api/workspaces';
import { useAuth } from '../auth/AuthContext';
import { InviteDialog } from '../components/InviteDialog';
import { WorkspaceRail } from '../components/WorkspaceRail';

interface AdminMember {
  user_id: number;
  email: string;
  display_name: string;
  role: string;
  joined_at: string;
  deactivated_at?: string | null;
}

type Tab = 'members' | 'integrations' | 'settings' | 'audit';

export function AdminPage(): ReactElement {
  const { user, loading: authLoading } = useAuth();
  const { slug = '' } = useParams();

  const [tab, setTab] = useState<Tab>('members');
  const [inviteOpen, setInviteOpen] = useState(false);

  const mshipQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: !!user,
    staleTime: 30_000,
  });
  const current = mshipQuery.data?.find((m) => m.workspace.slug === slug) ?? null;
  const canAdmin = current?.role === 'owner' || current?.role === 'admin';

  if (authLoading) {
    return <div className="sl-placeholder">Loading…</div>;
  }
  if (!user) return <Navigate to="/login" replace />;
  if (!mshipQuery.isLoading && !current) return <Navigate to="/" replace />;

  if (current && !canAdmin) {
    return (
      <div style={{ display: 'flex', height: '100vh' }}>
        <WorkspaceRail activeSlug={slug} />
        <div className="sl-admin-page">
          <div className="sl-admin-container">
            <h1 className="sl-admin-title">Admin</h1>
            <p className="sl-muted">
              You need to be an owner or admin of this workspace to access admin tools.
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', height: '100vh' }}>
      <WorkspaceRail activeSlug={slug} />
      <main className="sl-admin-page">
        <div className="sl-admin-container">
          <header className="sl-admin-header">
            <div>
              <h1 className="sl-admin-title">Admin</h1>
              <div className="sl-admin-subtitle">
                {current?.workspace.name}
                {current?.role ? ` · you are ${current.role}` : ''}
              </div>
            </div>
          </header>

          <nav className="sl-admin-tabs" aria-label="Admin sections">
            <button
              type="button"
              className={`sl-admin-tab ${tab === 'members' ? 'active' : ''}`}
              aria-pressed={tab === 'members'}
              onClick={() => setTab('members')}
            >
              Members
            </button>
            <button
              type="button"
              className={`sl-admin-tab ${tab === 'integrations' ? 'active' : ''}`}
              aria-pressed={tab === 'integrations'}
              onClick={() => setTab('integrations')}
            >
              Integrations
            </button>
            <button
              type="button"
              className={`sl-admin-tab ${tab === 'settings' ? 'active' : ''}`}
              aria-pressed={tab === 'settings'}
              onClick={() => setTab('settings')}
            >
              Settings
            </button>
            <button
              type="button"
              className={`sl-admin-tab ${tab === 'audit' ? 'active' : ''}`}
              aria-pressed={tab === 'audit'}
              onClick={() => setTab('audit')}
            >
              Audit log
            </button>
          </nav>

          {tab === 'members' && (
            <MembersTab slug={slug} meUserID={user.id} onInvite={() => setInviteOpen(true)} />
          )}
          {tab === 'integrations' && <IntegrationsTab slug={slug} />}
          {tab === 'settings' && <SettingsTab slug={slug} />}
          {tab === 'audit' && <AuditTab slug={slug} />}
        </div>
      </main>

      {current && (
        <InviteDialog
          workspaceSlug={current.workspace.slug}
          workspaceName={current.workspace.name}
          myRole={current.role}
          open={inviteOpen}
          onClose={() => setInviteOpen(false)}
        />
      )}
    </div>
  );
}

// ---- Members tab --------------------------------------------------------

function MembersTab({
  slug,
  meUserID,
  onInvite,
}: {
  slug: string;
  meUserID: number;
  onInvite: () => void;
}): ReactElement {
  const qc = useQueryClient();
  const membersQuery = useQuery({
    queryKey: ['admin', slug, 'members'],
    queryFn: () =>
      apiFetch<AdminMember[]>(`/workspaces/${encodeURIComponent(slug)}/admin/members`),
  });

  const roleMutation = useMutation({
    mutationFn: ({ uid, role }: { uid: number; role: string }) =>
      apiFetch<void>(`/workspaces/${encodeURIComponent(slug)}/admin/members/${uid}`, {
        method: 'PATCH',
        body: JSON.stringify({ role }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', slug, 'members'] }),
  });

  const deactivateMutation = useMutation({
    mutationFn: (uid: number) =>
      apiFetch<void>(`/workspaces/${encodeURIComponent(slug)}/admin/members/${uid}`, {
        method: 'DELETE',
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', slug, 'members'] }),
  });

  const members = membersQuery.data ?? [];

  return (
    <section className="sl-admin-section">
      <div className="sl-admin-section-header">
        <h2 className="sl-admin-section-title">People</h2>
        <button type="button" className="sl-primary sl-primary-sm" onClick={onInvite}>
          Invite people
        </button>
      </div>

      {membersQuery.isLoading && <p className="sl-muted">Loading…</p>}

      {!membersQuery.isLoading && members.length === 0 && (
        <div className="sl-admin-card sl-admin-empty">
          <div className="sl-admin-empty-title">No one&rsquo;s here yet</div>
          <p className="sl-muted" style={{ marginBottom: 16 }}>
            Invite someone to get started.
          </p>
          <button type="button" className="sl-primary" onClick={onInvite}>
            Invite your first teammate
          </button>
        </div>
      )}

      {members.length > 0 && (
        <div className="sl-admin-card">
          <table className="sl-admin-table">
            <thead>
              <tr>
                <th>Member</th>
                <th>Role</th>
                <th>Joined</th>
                <th aria-label="Actions"></th>
              </tr>
            </thead>
            <tbody>
              {members.map((m) => {
                const initial = (m.display_name || m.email)[0]?.toUpperCase() ?? '?';
                const isMe = m.user_id === meUserID;
                return (
                  <tr key={m.user_id}>
                    <td>
                      <div className="sl-admin-member">
                        <div className="sl-admin-avatar" aria-hidden="true">
                          {initial}
                        </div>
                        <div>
                          <div className="sl-admin-name">
                            {m.display_name || '—'}
                            {isMe && (
                              <span className="sl-muted" style={{ fontWeight: 400 }}>
                                {' '}· you
                              </span>
                            )}
                          </div>
                          <div className="sl-admin-email">{m.email}</div>
                        </div>
                      </div>
                    </td>
                    <td>
                      <select
                        className="sl-admin-select"
                        value={m.role}
                        disabled={isMe && m.role === 'owner'}
                        onChange={(e) =>
                          roleMutation.mutate({ uid: m.user_id, role: e.target.value })
                        }
                        aria-label={`Role for ${m.display_name || m.email}`}
                      >
                        <option value="owner">Owner</option>
                        <option value="admin">Admin</option>
                        <option value="member">Member</option>
                        <option value="guest">Guest</option>
                      </select>
                    </td>
                    <td className="sl-muted">
                      {new Date(m.joined_at).toLocaleDateString(undefined, {
                        year: 'numeric',
                        month: 'short',
                        day: 'numeric',
                      })}
                    </td>
                    <td style={{ textAlign: 'right' }}>
                      {!isMe && (
                        <button
                          type="button"
                          className="sl-admin-danger"
                          onClick={() => {
                            if (
                              window.confirm(
                                `Deactivate ${m.display_name || m.email}? They will lose access immediately.`,
                              )
                            ) {
                              deactivateMutation.mutate(m.user_id);
                            }
                          }}
                        >
                          Deactivate
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

// ---- Settings tab -------------------------------------------------------

function SettingsTab({ slug }: { slug: string }): ReactElement {
  const [brandColor, setBrandColor] = useState('');
  const [retentionDays, setRetentionDays] = useState<string>('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const saveMutation = useMutation({
    mutationFn: () => {
      const payload: Record<string, unknown> = {};
      if (brandColor.trim()) payload.brand_color = brandColor.trim();
      if (retentionDays.trim()) {
        const n = Number(retentionDays);
        if (Number.isFinite(n) && n > 0) payload.retention_days = n;
      } else {
        payload.clear_retention = true;
      }
      return apiFetch<void>(
        `/workspaces/${encodeURIComponent(slug)}/admin/settings`,
        { method: 'PATCH', body: JSON.stringify(payload) },
      );
    },
    onSuccess: () => setStatus({ kind: 'ok', text: 'Settings saved.' }),
    onError: (err: Error) => {
      const msg =
        err instanceof ApiError ? (err.problem.detail ?? err.message) : err.message;
      setStatus({ kind: 'err', text: msg });
    },
  });

  return (
    <section className="sl-admin-section">
      <div className="sl-admin-section-header">
        <h2 className="sl-admin-section-title">Workspace settings</h2>
      </div>

      <div className="sl-admin-card" style={{ padding: 24 }}>
        <div className="sl-admin-form">
          <label>
            <span>Brand color</span>
            <input
              type="text"
              value={brandColor}
              onChange={(e) => setBrandColor(e.target.value)}
              placeholder="#4a6ee0"
            />
          </label>
          <label>
            <span>Message retention (days)</span>
            <input
              type="number"
              min={1}
              value={retentionDays}
              onChange={(e) => setRetentionDays(e.target.value)}
              placeholder="Leave blank to keep messages forever"
            />
            <span style={{ marginTop: 6, fontSize: 12, color: 'var(--text-muted)' }}>
              Messages older than this are automatically purged. Leave blank for no auto-purge.
            </span>
          </label>
          <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 8 }}>
            <button
              type="button"
              className="sl-primary"
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending}
            >
              {saveMutation.isPending ? 'Saving…' : 'Save settings'}
            </button>
            {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
            {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
          </div>
        </div>
      </div>
    </section>
  );
}

// ---- Audit tab ----------------------------------------------------------

interface AuditEntry {
  id: number;
  actor_user_id?: number;
  actor_display_name?: string;
  actor_email?: string;
  actor_ip?: string;
  action: string;
  target_kind?: string;
  target_id?: string;
  metadata?: unknown;
  created_at: string;
}

function AuditTab({ slug }: { slug: string }): ReactElement {
  const entriesQuery = useQuery({
    queryKey: ['admin', slug, 'audit'],
    queryFn: () =>
      apiFetch<AuditEntry[]>(`/workspaces/${encodeURIComponent(slug)}/admin/audit?limit=100`),
  });

  const entries = entriesQuery.data ?? [];

  return (
    <section className="sl-admin-section">
      <div className="sl-admin-section-header">
        <h2 className="sl-admin-section-title">Audit log</h2>
      </div>

      {entriesQuery.isLoading && <p className="sl-muted">Loading…</p>}

      {!entriesQuery.isLoading && entries.length === 0 && (
        <div className="sl-admin-card sl-admin-empty">
          <div className="sl-admin-empty-title">Nothing logged yet</div>
          <p className="sl-muted">
            Membership changes, role updates, and exports will appear here as they happen.
          </p>
        </div>
      )}

      {entries.length > 0 && (
        <div className="sl-admin-card">
          <ul className="sl-admin-audit-list">
            {entries.map((e) => (
              <li key={e.id} className="sl-admin-audit-row">
                <span className="sl-admin-audit-action">{e.action}</span>
                <div className="sl-admin-audit-detail">
                  <div className="sl-admin-audit-target">
                    {e.actor_display_name || e.actor_email || 'system'}
                    {e.target_kind && (
                      <>
                        {' '}→ <span className="sl-muted">
                          {e.target_kind}
                          {e.target_id ? `:${e.target_id}` : ''}
                        </span>
                      </>
                    )}
                  </div>
                  <div className="sl-admin-audit-meta">
                    {new Date(e.created_at).toLocaleString()}
                    {e.actor_ip ? ` · ${e.actor_ip}` : ''}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

// ---- Integrations tab ---------------------------------------------------

// RestartBanner is a super-admin-only CTA that appears when
// install_settings.restart_required_at is set — i.e. when the admin
// patched VAPID / Collabora / Y-Sweet / LiveKit since the server last
// booted. A confirm-first button fires /install/restart and polls
// /healthz until the server is back, then reloads the page.
function RestartBanner(): ReactElement | null {
  const qc = useQueryClient();
  const statusQuery = useQuery({
    queryKey: ['install', 'restart-status'],
    queryFn: () => getRestartStatus(),
    // Poll while the banner is visible so it picks up saves from
    // other admins. Short interval is fine — the endpoint is tiny.
    refetchInterval: 8_000,
    refetchOnWindowFocus: true,
  });
  const [confirming, setConfirming] = useState(false);
  const [phase, setPhase] = useState<'idle' | 'requesting' | 'waiting' | 'done' | 'error'>('idle');
  const [errMsg, setErrMsg] = useState<string | null>(null);

  const status = statusQuery.data;
  if (!status || !status.restart_required) return null;

  async function waitForReadiness(): Promise<void> {
    // Poll /api/v1/ping until it comes back, then reload. Hard cap at
    // 30s — if the process doesn't come back on its own by then, the
    // supervisor is likely misconfigured and the admin needs to look
    // at logs rather than waiting forever.
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      try {
        const res = await fetch('/api/v1/ping', { cache: 'no-store' });
        if (res.ok) {
          setPhase('done');
          // Invalidate the status query before reload so the next
          // view doesn't flash a stale banner.
          qc.invalidateQueries({ queryKey: ['install', 'restart-status'] });
          setTimeout(() => window.location.reload(), 600);
          return;
        }
      } catch {
        // network error expected during the restart window
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
    setPhase('error');
    setErrMsg('Server did not come back within 30s. Check your supervisor logs (systemctl status sliils-app or docker compose logs).');
  }

  async function onRestart(): Promise<void> {
    setPhase('requesting');
    setErrMsg(null);
    try {
      await restartServer();
      setPhase('waiting');
      await waitForReadiness();
    } catch (err) {
      const msg = err instanceof ApiError ? (err.problem.detail ?? err.message) : (err as Error).message;
      setPhase('error');
      setErrMsg(msg);
    }
  }

  const sinceText = status.since
    ? new Date(status.since).toLocaleString()
    : 'recently';

  return (
    <div
      className="sl-admin-card"
      style={{
        padding: 20,
        marginBottom: 16,
        borderColor: 'var(--warning, #b45309)',
        background: 'color-mix(in srgb, var(--warning, #b45309) 8%, var(--surface))',
      }}
    >
      <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div style={{ flex: '1 1 320px', minWidth: 0 }}>
          <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4 }}>
            Server restart required
          </div>
          <div className="sl-muted" style={{ fontSize: 13, lineHeight: 1.5 }}>
            Infrastructure settings changed {sinceText}. Push, calls, pages,
            and office editing still use the previously-loaded values until
            the server restarts.
            {!status.supervised && (
              <>
                {' '}This install isn&rsquo;t running under a supervisor, so
                the in-app restart isn&rsquo;t available. Run{' '}
                <code>systemctl restart sliils-app</code> or{' '}
                <code>docker compose restart sliils-app</code> from the host
                to apply.
              </>
            )}
          </div>
          {phase === 'waiting' && (
            <div className="sl-muted" style={{ fontSize: 12, marginTop: 8 }}>
              Waiting for the supervisor to bring the server back…
            </div>
          )}
          {phase === 'done' && (
            <div className="sl-success" style={{ fontSize: 12, marginTop: 8 }}>
              Server is back. Reloading.
            </div>
          )}
          {phase === 'error' && errMsg && (
            <div className="sl-error" style={{ fontSize: 12, marginTop: 8 }}>
              {errMsg}
            </div>
          )}
        </div>
        {status.supervised && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {!confirming && phase === 'idle' && (
              <button
                type="button"
                className="sl-primary"
                onClick={() => setConfirming(true)}
              >
                Restart server
              </button>
            )}
            {confirming && phase === 'idle' && (
              <>
                <span className="sl-muted" style={{ fontSize: 12 }}>
                  Users will briefly disconnect. Continue?
                </span>
                <button
                  type="button"
                  className="sl-primary"
                  onClick={() => {
                    setConfirming(false);
                    void onRestart();
                  }}
                >
                  Confirm restart
                </button>
                <button
                  type="button"
                  className="sl-notif-btn"
                  onClick={() => setConfirming(false)}
                >
                  Cancel
                </button>
              </>
            )}
            {(phase === 'requesting' || phase === 'waiting') && (
              <button type="button" className="sl-primary" disabled>
                Restarting…
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function IntegrationsTab({ slug }: { slug: string }): ReactElement {
  const { user } = useAuth();
  const isSuper = !!user?.is_super_admin;
  return (
    <section className="sl-admin-section">
      {isSuper && <RestartBanner />}
      <EmailIntegrationCard slug={slug} />
      <SignupModeCard />
      {isSuper && (
        <>
          <InstallEmailCard />
          <VAPIDCard />
          <CollaboraCard />
          <YSweetCard />
          <LiveKitCard />
        </>
      )}
    </section>
  );
}

function EmailIntegrationCard({ slug }: { slug: string }): ReactElement {
  const qc = useQueryClient();
  const cfgQuery = useQuery({
    queryKey: ['admin', slug, 'integrations', 'email'],
    queryFn: () => getWorkspaceEmail(slug),
  });
  const [apiKey, setApiKey] = useState('');
  const [fromAddress, setFromAddress] = useState('');
  const [fromName, setFromName] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Seed the inputs once when the server config arrives. Using an
  // effect (not inline setState during render) so React doesn't warn
  // about cascading updates. The empty-string guard means later
  // refetches don't clobber in-progress edits.
  useEffect(() => {
    if (!cfgQuery.data) return;
    if (cfgQuery.data.from_address && fromAddress === '') {
      setFromAddress(cfgQuery.data.from_address);
    }
    if (cfgQuery.data.from_name && fromName === '') {
      setFromName(cfgQuery.data.from_name);
    }
    // Intentionally omit fromAddress/fromName from deps: we only want
    // this to fire when the server data changes, not on every keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfgQuery.data]);

  const saveMutation = useMutation({
    mutationFn: () =>
      patchWorkspaceEmail(slug, {
        provider: 'resend',
        resend_api_key: apiKey,
        from_address: fromAddress,
        from_name: fromName,
      }),
    onSuccess: (cfg: WorkspaceEmailConfig) => {
      qc.setQueryData(['admin', slug, 'integrations', 'email'], cfg);
      setApiKey('');
      setStatus({ kind: 'ok', text: 'Saved.' });
    },
    onError: (err: Error) => {
      const msg =
        err instanceof ApiError ? (err.problem.detail ?? err.message) : err.message;
      setStatus({ kind: 'err', text: msg });
    },
  });

  const testMutation = useMutation({
    mutationFn: () => testWorkspaceEmail(slug),
    onSuccess: (res) => {
      setStatus(
        res.ok
          ? { kind: 'ok', text: 'Test email sent to your address.' }
          : { kind: 'err', text: res.error ?? 'Test failed.' },
      );
    },
    onError: (err: Error) => setStatus({ kind: 'err', text: err.message }),
  });

  const cfg = cfgQuery.data;

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 8 }}>
        Email provider
      </h2>
      <p className="sl-muted" style={{ marginTop: 0 }}>
        Invites sent from this workspace use the configuration below. If no
        API key is set, the install-level default is used as a fallback.
      </p>

      <div className="sl-admin-form">
        <label>
          <span>Resend API key {cfg?.api_key_is_set ? '(configured — leave blank to keep)' : '(required)'}</span>
          <input
            type="password"
            autoComplete="off"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={cfg?.api_key_is_set ? '•••••• (unchanged)' : 're_...'}
          />
        </label>
        <label>
          <span>From address</span>
          <input
            type="email"
            value={fromAddress}
            onChange={(e) => setFromAddress(e.target.value)}
            placeholder="no-reply@yourdomain.com"
          />
        </label>
        <label>
          <span>From name</span>
          <input
            type="text"
            value={fromName}
            onChange={(e) => setFromName(e.target.value)}
            placeholder="Your workspace name"
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            {saveMutation.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          <button
            type="button"
            className="sl-notif-btn"
            disabled={testMutation.isPending || !cfg?.api_key_is_set}
            onClick={() => testMutation.mutate()}
            title={cfg?.api_key_is_set ? 'Send a test email to yourself' : 'Save a config first'}
          >
            {testMutation.isPending ? 'Sending…' : 'Send test email'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}

function SignupModeCard(): ReactElement {
  const qc = useQueryClient();
  const modeQuery = useQuery({
    queryKey: ['install', 'signup-mode'],
    queryFn: () => getSignupMode(),
  });
  const mutation = useMutation({
    mutationFn: (mode: SignupMode) => setSignupMode(mode),
    onSuccess: (res) => qc.setQueryData(['install', 'signup-mode'], res),
  });
  const current = modeQuery.data?.signup_mode ?? 'invite_only';

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 8 }}>
        Who can sign up
      </h2>
      <p className="sl-muted" style={{ marginTop: 0 }}>
        This controls the whole install. &ldquo;Invite only&rdquo; means new
        accounts need a valid invitation; &ldquo;Open&rdquo; lets anyone
        register and create their own workspace (true multi-tenant).
      </p>
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button
          type="button"
          className={current === 'invite_only' ? 'sl-primary' : 'sl-notif-btn'}
          onClick={() => mutation.mutate('invite_only')}
          disabled={mutation.isPending}
        >
          Invite only
        </button>
        <button
          type="button"
          className={current === 'open' ? 'sl-primary' : 'sl-notif-btn'}
          onClick={() => mutation.mutate('open')}
          disabled={mutation.isPending}
        >
          Open signup
        </button>
      </div>
    </div>
  );
}

// ---- Install-level cards (super-admin only) ---------------------------

function InstallEmailCard(): ReactElement {
  const qc = useQueryClient();
  const cfgQuery = useQuery({
    queryKey: ['install', 'email'],
    queryFn: () => getInstallEmail(),
  });
  const [apiKey, setApiKey] = useState('');
  const [fromAddress, setFromAddress] = useState('');
  const [fromName, setFromName] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (!cfgQuery.data) return;
    if (cfgQuery.data.from_address && fromAddress === '') {
      setFromAddress(cfgQuery.data.from_address);
    }
    if (cfgQuery.data.from_name && fromName === '') {
      setFromName(cfgQuery.data.from_name);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfgQuery.data]);

  const saveMutation = useMutation({
    mutationFn: () =>
      patchInstallEmail({
        provider: 'resend',
        resend_api_key: apiKey,
        from_address: fromAddress,
        from_name: fromName,
      }),
    onSuccess: (cfg: InstallEmailConfig) => {
      qc.setQueryData(['install', 'email'], cfg);
      setApiKey('');
      setStatus({ kind: 'ok', text: 'Saved.' });
    },
    onError: (err: Error) => {
      const msg =
        err instanceof ApiError ? (err.problem.detail ?? err.message) : err.message;
      setStatus({ kind: 'err', text: msg });
    },
  });

  const cfg = cfgQuery.data;

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 4 }}>
        Install-wide email (auth flows)
      </h2>
      <div className="sl-muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Super-admin only · used for magic-link, password-reset, verify-email
      </div>
      <p className="sl-muted" style={{ marginTop: 0 }}>
        This is the fallback email provider for install-level messages and the
        default for workspaces that don&rsquo;t set their own. Changes apply
        immediately &mdash; no restart needed.
      </p>

      <div className="sl-admin-form">
        <label>
          <span>Resend API key {cfg?.api_key_is_set ? '(configured — leave blank to keep)' : '(required)'}</span>
          <input
            type="password"
            autoComplete="off"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder={cfg?.api_key_is_set ? '•••••• (unchanged)' : 're_...'}
          />
        </label>
        <label>
          <span>From address</span>
          <input
            type="email"
            value={fromAddress}
            onChange={(e) => setFromAddress(e.target.value)}
            placeholder="no-reply@yourdomain.com"
          />
        </label>
        <label>
          <span>From name</span>
          <input
            type="text"
            value={fromName}
            onChange={(e) => setFromName(e.target.value)}
            placeholder="SliilS"
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            {saveMutation.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}

function useInfrastructure() {
  return useQuery({
    queryKey: ['install', 'infrastructure'],
    queryFn: () => getInstallInfrastructure(),
  });
}

function useInfraPatchMutation(
  onSuccess?: (cfg: InstallInfrastructure) => void,
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: PatchInstallInfrastructure) =>
      patchInstallInfrastructure(input),
    onSuccess: (cfg) => {
      qc.setQueryData(['install', 'infrastructure'], cfg);
      onSuccess?.(cfg);
    },
  });
}

function VAPIDCard(): ReactElement {
  const infraQuery = useInfrastructure();
  const [publicKey, setPublicKey] = useState('');
  const [privateKey, setPrivateKey] = useState('');
  const [subject, setSubject] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (!infraQuery.data) return;
    if (infraQuery.data.vapid_public_key && publicKey === '') {
      setPublicKey(infraQuery.data.vapid_public_key);
    }
    if (infraQuery.data.vapid_subject && subject === '') {
      setSubject(infraQuery.data.vapid_subject);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [infraQuery.data]);

  const save = useInfraPatchMutation(() => {
    setPrivateKey('');
    setStatus({ kind: 'ok', text: 'Saved.' });
  });

  const generate = useMutation({
    mutationFn: () => generateVAPIDKeys(),
    onSuccess: (kp) => {
      setPublicKey(kp.public_key);
      setPrivateKey(kp.private_key);
      setStatus({ kind: 'ok', text: 'Fresh keypair generated. Click Save to persist.' });
    },
    onError: (err: Error) => setStatus({ kind: 'err', text: err.message }),
  });

  const infra = infraQuery.data;

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 4 }}>
        Web push (VAPID)
      </h2>
      <div className="sl-muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Super-admin only · identifies this install to push services
      </div>
      <p className="sl-muted" style={{ marginTop: 0 }}>
        VAPID keys authenticate push messages from this install. Generate a
        fresh keypair on first setup — the private key is encrypted at rest
        and never returned by the API.
      </p>

      <div className="sl-admin-form">
        <label>
          <span>Public key</span>
          <input
            type="text"
            value={publicKey}
            onChange={(e) => setPublicKey(e.target.value)}
            placeholder="BH… (base64url)"
          />
        </label>
        <label>
          <span>Private key {infra?.vapid_private_key_set ? '(configured — leave blank to keep)' : '(required)'}</span>
          <input
            type="password"
            autoComplete="off"
            value={privateKey}
            onChange={(e) => setPrivateKey(e.target.value)}
            placeholder={infra?.vapid_private_key_set ? '•••••• (unchanged)' : 'base64url private key'}
          />
        </label>
        <label>
          <span>Subject</span>
          <input
            type="text"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="mailto:ops@yourdomain.com"
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={save.isPending}
            onClick={() =>
              save.mutate({
                vapid_public_key: publicKey,
                vapid_private_key: privateKey,
                vapid_subject: subject,
              })
            }
          >
            {save.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          <button
            type="button"
            className="sl-notif-btn"
            disabled={generate.isPending}
            onClick={() => generate.mutate()}
          >
            {generate.isPending ? 'Generating…' : 'Generate new keypair'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}

function CollaboraCard(): ReactElement {
  const infraQuery = useInfrastructure();
  const [url, setUrl] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (infraQuery.data?.collabora_url && url === '') {
      setUrl(infraQuery.data.collabora_url);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [infraQuery.data]);

  const save = useInfraPatchMutation(() => setStatus({ kind: 'ok', text: 'Saved.' }));

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 4 }}>
        Collabora Online (office docs)
      </h2>
      <div className="sl-muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Super-admin only · optional · powers in-browser editing of uploaded docs
      </div>

      <div className="sl-admin-form">
        <label>
          <span>Collabora URL</span>
          <input
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://collabora.yourdomain.com"
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={save.isPending}
            onClick={() => save.mutate({ collabora_url: url })}
          >
            {save.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}

function YSweetCard(): ReactElement {
  const infraQuery = useInfrastructure();
  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (infraQuery.data?.ysweet_url && url === '') {
      setUrl(infraQuery.data.ysweet_url);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [infraQuery.data]);

  const save = useInfraPatchMutation(() => {
    setToken('');
    setStatus({ kind: 'ok', text: 'Saved.' });
  });

  const infra = infraQuery.data;

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 4 }}>
        Y-Sweet (collaborative pages)
      </h2>
      <div className="sl-muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Super-admin only · optional · backs the Pages multi-cursor experience
      </div>

      <div className="sl-admin-form">
        <label>
          <span>Y-Sweet URL</span>
          <input
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://ysweet.yourdomain.com"
          />
        </label>
        <label>
          <span>Server token {infra?.ysweet_server_token_set ? '(configured — leave blank to keep)' : '(required)'}</span>
          <input
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder={infra?.ysweet_server_token_set ? '•••••• (unchanged)' : 'ys_...'}
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={save.isPending}
            onClick={() =>
              save.mutate({
                ysweet_url: url,
                ysweet_server_token: token,
              })
            }
          >
            {save.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}

function LiveKitCard(): ReactElement {
  const infraQuery = useInfrastructure();
  const [url, setUrl] = useState('');
  const [wsUrl, setWsUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [secret, setSecret] = useState('');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    if (!infraQuery.data) return;
    if (infraQuery.data.livekit_url && url === '') setUrl(infraQuery.data.livekit_url);
    if (infraQuery.data.livekit_ws_url && wsUrl === '') setWsUrl(infraQuery.data.livekit_ws_url);
    if (infraQuery.data.livekit_api_key && apiKey === '') setApiKey(infraQuery.data.livekit_api_key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [infraQuery.data]);

  const save = useInfraPatchMutation(() => {
    setSecret('');
    setStatus({ kind: 'ok', text: 'Saved.' });
  });

  const infra = infraQuery.data;

  return (
    <div className="sl-admin-card" style={{ padding: 24, marginBottom: 16 }}>
      <h2 className="sl-admin-section-title" style={{ marginBottom: 4 }}>
        LiveKit (calls + meetings)
      </h2>
      <div className="sl-muted" style={{ fontSize: 12, marginBottom: 8 }}>
        Super-admin only · optional · powers voice, video, and screen share
      </div>

      <div className="sl-admin-form">
        <label>
          <span>HTTP URL</span>
          <input
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://livekit.yourdomain.com"
          />
        </label>
        <label>
          <span>WebSocket URL</span>
          <input
            type="url"
            value={wsUrl}
            onChange={(e) => setWsUrl(e.target.value)}
            placeholder="wss://livekit.yourdomain.com"
          />
        </label>
        <label>
          <span>API key</span>
          <input
            type="text"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            placeholder="APIxxxx"
          />
        </label>
        <label>
          <span>API secret {infra?.livekit_api_secret_set ? '(configured — leave blank to keep)' : '(required)'}</span>
          <input
            type="password"
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder={infra?.livekit_api_secret_set ? '•••••• (unchanged)' : 'secret'}
          />
        </label>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <button
            type="button"
            className="sl-primary"
            disabled={save.isPending}
            onClick={() =>
              save.mutate({
                livekit_url: url,
                livekit_ws_url: wsUrl,
                livekit_api_key: apiKey,
                livekit_api_secret: secret,
              })
            }
          >
            {save.isPending ? 'Saving…' : 'Save configuration'}
          </button>
          {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
          {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
        </div>
      </div>
    </div>
  );
}
