// Admin dashboard (M12-P3). Mounts at /w/:slug/admin.
//
// Workspace owners/admins manage people, workspace settings, retention,
// and the audit log here. Invite flow lives on the Members tab where it
// makes contextual sense.

import { useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiError, apiFetch } from '../api/client';
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

type Tab = 'members' | 'settings' | 'audit';

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
          <div className="sl-admin-empty-title">No one's here yet</div>
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
