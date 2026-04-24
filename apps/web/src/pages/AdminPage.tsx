// Admin dashboard (M12-P3). Mounts at /w/:slug/admin.
//
// This is where workspace owners/admins manage people + settings +
// retention + audit + exports. Invite lives here because it's an
// admin action on members, not a workspace-level shortcut.

import { useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate, useParams, useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { apiFetch } from '../api/client';
import { listMyWorkspaces } from '../api/workspaces';
import { useAuth } from '../auth/AuthContext';
import { InviteDialog } from '../components/InviteDialog';

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
  const navigate = useNavigate();
  const qc = useQueryClient();

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

  const membersQuery = useQuery({
    queryKey: ['admin', slug, 'members'],
    queryFn: () =>
      apiFetch<AdminMember[]>(`/workspaces/${encodeURIComponent(slug)}/admin/members`),
    enabled: !!user && canAdmin && tab === 'members',
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

  if (authLoading) return <div style={{ padding: 24 }}>Loading…</div>;
  if (!user) return <Navigate to="/login" replace />;
  if (!mshipQuery.isLoading && !current) return <Navigate to="/" replace />;
  if (current && !canAdmin) {
    return (
      <div style={{ padding: 24 }}>
        <h2>Admin</h2>
        <p>You need to be an owner or admin of this workspace to see this page.</p>
        <button type="button" onClick={() => navigate(`/w/${slug}`)}>
          Back to workspace
        </button>
      </div>
    );
  }

  return (
    <div style={{ padding: 24, maxWidth: 960, margin: '0 auto' }}>
      <header
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'baseline',
          marginBottom: 16,
        }}
      >
        <div>
          <h1 style={{ margin: 0 }}>Admin</h1>
          <div style={{ color: '#888', fontSize: 13 }}>
            {current?.workspace.name} &middot; {current?.role}
          </div>
        </div>
        <button
          type="button"
          onClick={() => navigate(`/w/${slug}`)}
          style={secondaryBtn}
        >
          Back to workspace
        </button>
      </header>

      <nav
        aria-label="Admin sections"
        style={{ display: 'flex', gap: 4, borderBottom: '1px solid #e5e5e5', marginBottom: 16 }}
      >
        <TabButton label="Members" active={tab === 'members'} onClick={() => setTab('members')} />
        <TabButton label="Settings" active={tab === 'settings'} onClick={() => setTab('settings')} />
        <TabButton label="Audit log" active={tab === 'audit'} onClick={() => setTab('audit')} />
      </nav>

      {tab === 'members' && (
        <section>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
            <h2 style={{ margin: 0 }}>People</h2>
            <button type="button" onClick={() => setInviteOpen(true)} style={primaryBtn}>
              Invite people
            </button>
          </div>

          {membersQuery.isLoading && <p>Loading members…</p>}
          {membersQuery.data && (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ textAlign: 'left', color: '#666', fontSize: 12 }}>
                  <th style={cell}>Name</th>
                  <th style={cell}>Email</th>
                  <th style={cell}>Role</th>
                  <th style={cell}>Joined</th>
                  <th style={cell} aria-label="Actions"></th>
                </tr>
              </thead>
              <tbody>
                {membersQuery.data.map((m) => (
                  <tr key={m.user_id} style={{ borderTop: '1px solid #eee' }}>
                    <td style={cell}>{m.display_name || '—'}</td>
                    <td style={cell}>{m.email}</td>
                    <td style={cell}>
                      <select
                        value={m.role}
                        disabled={m.user_id === user.id && m.role === 'owner'}
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
                    <td style={cell}>{new Date(m.joined_at).toLocaleDateString()}</td>
                    <td style={cell}>
                      {m.user_id !== user.id && (
                        <button
                          type="button"
                          onClick={() => {
                            if (window.confirm(`Deactivate ${m.display_name || m.email}?`)) {
                              deactivateMutation.mutate(m.user_id);
                            }
                          }}
                          style={dangerBtn}
                        >
                          Deactivate
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}

      {tab === 'settings' && <AdminSettingsTab slug={slug} />}
      {tab === 'audit' && <AdminAuditTab slug={slug} />}

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

// ---- Settings tab ------------------------------------------------------

function AdminSettingsTab({ slug }: { slug: string }): ReactElement {
  const [brandColor, setBrandColor] = useState('');
  const [retentionDays, setRetentionDays] = useState<string>('');
  const [status, setStatus] = useState<string | null>(null);

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
    onSuccess: () => setStatus('Saved.'),
    onError: (err: Error) => setStatus(err.message),
  });

  return (
    <section>
      <h2 style={{ marginTop: 0 }}>Workspace settings</h2>
      <div style={{ display: 'grid', gap: 12, maxWidth: 480 }}>
        <label>
          <span style={{ display: 'block', fontSize: 12, color: '#666' }}>Brand color</span>
          <input
            type="text"
            value={brandColor}
            onChange={(e) => setBrandColor(e.target.value)}
            placeholder="#4a6ee0"
            style={input}
          />
        </label>
        <label>
          <span style={{ display: 'block', fontSize: 12, color: '#666' }}>
            Retention (days, blank = keep forever)
          </span>
          <input
            type="number"
            min={1}
            value={retentionDays}
            onChange={(e) => setRetentionDays(e.target.value)}
            placeholder=""
            style={input}
          />
        </label>
        <div>
          <button
            type="button"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
            style={primaryBtn}
          >
            {saveMutation.isPending ? 'Saving…' : 'Save settings'}
          </button>
          {status && <span style={{ marginLeft: 12, color: '#666' }}>{status}</span>}
        </div>
      </div>
    </section>
  );
}

// ---- Audit tab ---------------------------------------------------------

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

function AdminAuditTab({ slug }: { slug: string }): ReactElement {
  const entriesQuery = useQuery({
    queryKey: ['admin', slug, 'audit'],
    queryFn: () =>
      apiFetch<AuditEntry[]>(`/workspaces/${encodeURIComponent(slug)}/admin/audit?limit=100`),
  });

  return (
    <section>
      <h2 style={{ marginTop: 0 }}>Audit log</h2>
      {entriesQuery.isLoading && <p>Loading…</p>}
      {entriesQuery.data && entriesQuery.data.length === 0 && (
        <p style={{ color: '#888' }}>No entries yet.</p>
      )}
      <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {(entriesQuery.data ?? []).map((e) => (
          <li
            key={e.id}
            style={{
              padding: '10px 0',
              borderBottom: '1px solid #eee',
              fontSize: 14,
            }}
          >
            <div>
              <strong>{e.action}</strong>
              {e.target_kind && (
                <span style={{ color: '#888' }}>
                  {' '}
                  on {e.target_kind}
                  {e.target_id ? `:${e.target_id}` : ''}
                </span>
              )}
            </div>
            <div style={{ color: '#666', fontSize: 12 }}>
              {e.actor_display_name || e.actor_email || 'system'} &middot;{' '}
              {new Date(e.created_at).toLocaleString()}
              {e.actor_ip ? ` · ${e.actor_ip}` : ''}
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---- UI primitives (local, matches PagesPage inline style) -------------

function TabButton({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}): ReactElement {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      style={{
        padding: '8px 16px',
        border: 'none',
        background: 'transparent',
        cursor: 'pointer',
        fontWeight: active ? 600 : 400,
        borderBottom: active ? '2px solid #2a4ea4' : '2px solid transparent',
        color: active ? '#2a4ea4' : '#444',
      }}
    >
      {label}
    </button>
  );
}

const cell: React.CSSProperties = { padding: '8px 10px', verticalAlign: 'middle' };
const input: React.CSSProperties = { padding: 6, width: '100%', boxSizing: 'border-box' };

const primaryBtn: React.CSSProperties = {
  padding: '8px 14px',
  border: '1px solid #2a4ea4',
  background: '#2a4ea4',
  color: '#fff',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 13,
};

const secondaryBtn: React.CSSProperties = {
  padding: '6px 12px',
  border: '1px solid #ddd',
  background: '#fff',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 13,
};

const dangerBtn: React.CSSProperties = {
  padding: '4px 10px',
  border: '1px solid #d66',
  background: '#fff',
  color: '#a33',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 12,
};
