// Admin-only "Invite people" overlay. Mounts inside AdminPage; the
// Members tab's "Invite people" button drives open/close.
//
// Layered surface:
//   1. Workspaces picker — the inviter selects one or more of the
//      workspaces where they already have owner/admin. Defaults to
//      the current workspace.
//   2. Single email (optional) + single role, applied to every
//      selected workspace.
//   3. Submit loops over the selected workspaces, creating one
//      invite per workspace, then shows a per-workspace results
//      block with Copy-link + Revoke actions for each new invite.
//   4. Below the create form, a "Pending invites" list for the
//      primary workspace (where the dialog was opened).
//
// Why client-side loop instead of a bulk server endpoint? At v1
// scale the admin will pick ≤ 10 workspaces at most, and each
// POST succeeds/fails independently. A failure in one workspace
// shouldn't nuke the others. The server surface stays narrow.

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '../api/client';
import {
  createInvite,
  listInvites,
  revokeInvite,
} from '../api/invites';
import type { Invite } from '../api/invites';
import { listMyWorkspaces } from '../api/workspaces';
import type { WorkspaceMembership } from '../api/workspaces';

interface Props {
  workspaceSlug: string;
  workspaceName: string;
  myRole: string; // "owner" | "admin" | "member" | "guest"
  open: boolean;
  onClose: () => void;
}

type Role = 'admin' | 'member' | 'guest';

interface CreatedInvite extends Invite {
  workspace_slug: string;
  workspace_name: string;
}

interface WorkspaceChoice {
  slug: string;
  name: string;
  role: string;
}

export function InviteDialog({
  workspaceSlug,
  workspaceName,
  myRole,
  open,
  onClose,
}: Props): ReactElement | null {
  const qc = useQueryClient();
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<Role>('member');
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [created, setCreated] = useState<CreatedInvite[]>([]);
  const [selectedSlugs, setSelectedSlugs] = useState<Set<string>>(
    new Set([workspaceSlug]),
  );
  const [submitting, setSubmitting] = useState(false);
  const canAdmin = myRole === 'owner' || myRole === 'admin';

  // Workspaces the current user is owner/admin on. Used to build the
  // multi-select. Loaded only when the dialog is open + the user can
  // actually invite.
  const workspacesQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: open && canAdmin,
    staleTime: 60_000,
  });

  const eligibleWorkspaces: WorkspaceChoice[] = useMemo(
    () =>
      (workspacesQuery.data ?? [])
        .filter((m: WorkspaceMembership) => m.role === 'owner' || m.role === 'admin')
        .map((m) => ({
          slug: m.workspace.slug,
          name: m.workspace.name,
          role: m.role,
        })),
    [workspacesQuery.data],
  );

  // Pending invites stay scoped to the workspace the dialog opened
  // from — users can open the admin page for other workspaces to
  // manage their pending invites separately.
  const pendingQuery = useQuery({
    queryKey: ['workspace', workspaceSlug, 'invites'],
    queryFn: () => listInvites(workspaceSlug),
    enabled: open && canAdmin,
    staleTime: 10_000,
  });

  const revokeMutation = useMutation({
    mutationFn: (id: number) => revokeInvite(workspaceSlug, id),
    onSuccess: (_, id) => {
      qc.setQueryData<Invite[]>(
        ['workspace', workspaceSlug, 'invites'],
        (prev = []) => prev.filter((p) => p.id !== id),
      );
      setCreated((prev) => prev.filter((p) => p.id !== id));
    },
  });

  useEffect(() => {
    if (!open) {
      setEmail('');
      setRole('member');
      setError(null);
      setNotice(null);
      setCreated([]);
      setSelectedSlugs(new Set([workspaceSlug]));
    }
  }, [open, workspaceSlug]);

  const inviteLinkFor = useCallback((token: string) => {
    return `${window.location.origin}/invite/${token}`;
  }, []);

  const copy = useCallback(async (text: string) => {
    try {
      if (window.navigator.clipboard?.writeText) {
        await window.navigator.clipboard.writeText(text);
        setNotice('Link copied.');
        return;
      }
    } catch {
      /* fall through */
    }
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
      setNotice('Link copied.');
    } finally {
      document.body.removeChild(ta);
    }
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);

    const slugs = [...selectedSlugs];
    if (slugs.length === 0) {
      setError('Pick at least one workspace to invite to.');
      return;
    }

    setSubmitting(true);
    const results: CreatedInvite[] = [];
    const failures: string[] = [];

    for (const slug of slugs) {
      const wsName =
        eligibleWorkspaces.find((w) => w.slug === slug)?.name ?? slug;
      try {
        const inv = await createInvite(slug, {
          email: email.trim() || undefined,
          role,
        });
        results.push({ ...inv, workspace_slug: slug, workspace_name: wsName });
      } catch (err) {
        const msg =
          err instanceof ApiError
            ? (err.problem.detail ?? err.problem.title)
            : (err as Error).message;
        failures.push(`${wsName}: ${msg}`);
      }
    }

    if (results.length > 0) {
      setCreated((prev) => [...results, ...prev]);
      if (results.some((r) => r.workspace_slug === workspaceSlug)) {
        qc.invalidateQueries({ queryKey: ['workspace', workspaceSlug, 'invites'] });
      }

      // Distinguish email-dispatch outcomes so the admin knows whether
      // to share the link manually.
      if (email.trim()) {
        const sent = results.filter((r) => r.email_status === 'sent').length;
        const failed = results.filter((r) => r.email_status === 'failed');
        const skipped = results.filter((r) => r.email_status === 'skipped').length;
        const parts: string[] = [];
        if (sent > 0) parts.push(`${sent} email${sent === 1 ? '' : 's'} sent`);
        if (failed.length > 0) {
          parts.push(
            `${failed.length} email${failed.length === 1 ? '' : 's'} could not be sent — share the link${failed.length === 1 ? '' : 's'} below`,
          );
        }
        if (skipped > 0) {
          parts.push(
            `${skipped} skipped (email not configured on this server) — share the link${skipped === 1 ? '' : 's'} below`,
          );
        }
        setNotice(parts.join(' · '));
        // Surface the specific provider error so the admin can act
        // (e.g. verify a domain on Resend, check SMTP creds).
        if (failed.length > 0) {
          const firstErr = failed.find((f) => f.email_error)?.email_error;
          if (firstErr) {
            setError(`Email provider says: ${firstErr}`);
          }
        }
      } else {
        setNotice(
          `Created ${results.length} link${results.length === 1 ? '' : 's'}. Copy below.`,
        );
      }
      setEmail('');
    }
    if (failures.length > 0) {
      setError((prev) => (prev ? `${prev}\n${failures.join('\n')}` : failures.join('\n')));
    }
    setSubmitting(false);
  }

  const pendingInvites = useMemo(() => pendingQuery.data ?? [], [pendingQuery.data]);

  if (!open) return null;

  if (!canAdmin) {
    return (
      <div
        className="sl-modal-backdrop"
        onMouseDown={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <div className="sl-modal" onMouseDown={(e) => e.stopPropagation()}>
          <header className="sl-modal-header">
            <h2>Invite to {workspaceName}</h2>
            <button
              type="button"
              className="sl-icon-btn"
              onClick={onClose}
              aria-label="Close"
            >
              ×
            </button>
          </header>
          <div className="sl-modal-body">
            <p className="sl-muted">
              Only workspace owners and admins can send invites. Ask someone
              with admin access to invite people.
            </p>
          </div>
        </div>
      </div>
    );
  }

  function toggleSlug(slug: string) {
    setSelectedSlugs((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug);
      else next.add(slug);
      return next;
    });
  }

  return (
    <div
      className="sl-modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        className="sl-modal"
        role="dialog"
        aria-modal="true"
        aria-label="Invite people"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="sl-modal-header">
          <h2>Invite people</h2>
          <button
            type="button"
            className="sl-icon-btn"
            onClick={onClose}
            aria-label="Close"
          >
            ×
          </button>
        </header>

        <form className="sl-modal-body" onSubmit={onSubmit}>
          <label className="sl-field">
            <span>Email (optional)</span>
            <input
              type="email"
              placeholder="they@example.com — leave blank for a shareable link"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="off"
            />
          </label>
          <label className="sl-field">
            <span>Role</span>
            <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="member">Member</option>
              <option value="admin">Admin</option>
              <option value="guest">Guest</option>
            </select>
          </label>

          <div className="sl-field">
            <span>Workspaces</span>
            {workspacesQuery.isLoading && (
              <p className="sl-muted" style={{ margin: 0 }}>Loading…</p>
            )}
            {!workspacesQuery.isLoading && eligibleWorkspaces.length === 0 && (
              <p className="sl-muted" style={{ margin: 0 }}>
                You&rsquo;re not an owner or admin on any workspace yet.
              </p>
            )}
            {eligibleWorkspaces.length > 0 && (
              <ul className="sl-invite-ws-list">
                {eligibleWorkspaces.map((w) => {
                  const checked = selectedSlugs.has(w.slug);
                  const inputId = `invite-ws-${w.slug}`;
                  return (
                    <li key={w.slug} className="sl-invite-ws-item">
                      <input
                        id={inputId}
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleSlug(w.slug)}
                      />
                      <label htmlFor={inputId} className="sl-invite-ws-label">
                        <span className="sl-invite-ws-name">{w.name}</span>
                        <span className="sl-invite-ws-role">{w.role}</span>
                      </label>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {error && (
            <div role="alert" className="sl-error" style={{ whiteSpace: 'pre-wrap' }}>
              {error}
            </div>
          )}
          {notice && (
            <div role="status" className="sl-notice">
              {notice}
            </div>
          )}
          <div className="sl-modal-actions">
            <button type="button" className="sl-linkbtn" onClick={onClose}>
              Close
            </button>
            <button
              type="submit"
              className="sl-primary"
              disabled={submitting || selectedSlugs.size === 0}
            >
              {submitting
                ? 'Working…'
                : email.trim()
                  ? `Send ${selectedSlugs.size} invitation${selectedSlugs.size === 1 ? '' : 's'}`
                  : `Create ${selectedSlugs.size} link${selectedSlugs.size === 1 ? '' : 's'}`}
            </button>
          </div>
        </form>

        {created.length > 0 && (
          <section className="sl-modal-section">
            <div className="sl-modal-section-header">
              <h3>Just created</h3>
            </div>
            <ul className="sl-invite-list">
              {created.map((inv) => {
                let emailBadge: ReactElement | null = null;
                if (inv.email) {
                  if (inv.email_status === 'sent') {
                    emailBadge = (
                      <span className="sl-invite-email-badge ok">email sent</span>
                    );
                  } else if (inv.email_status === 'failed') {
                    emailBadge = (
                      <span
                        className="sl-invite-email-badge bad"
                        title={inv.email_error ?? undefined}
                      >
                        email failed — share the link
                      </span>
                    );
                  } else if (inv.email_status === 'skipped') {
                    emailBadge = (
                      <span className="sl-invite-email-badge bad">
                        email not configured — share the link
                      </span>
                    );
                  }
                }
                return (
                  <li key={`${inv.workspace_slug}-${inv.id}`} className="sl-invite-row">
                    <div className="sl-invite-row-primary">
                      <div className="sl-invite-row-name">
                        {inv.email || <em className="sl-muted">Shareable link</em>}
                        <span className="sl-muted"> &middot; {inv.workspace_name}</span>
                      </div>
                      <div className="sl-invite-row-meta">
                        {inv.role} · expires {new Date(inv.expires_at).toLocaleDateString()}
                        {emailBadge && <> &middot; {emailBadge}</>}
                      </div>
                    </div>
                    <div className="sl-invite-row-actions">
                      {inv.token && (
                        <button
                          type="button"
                          className="sl-linkbtn"
                          onClick={() => copy(inviteLinkFor(inv.token!))}
                        >
                          Copy link
                        </button>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
        )}

        <section className="sl-modal-section">
          <div className="sl-modal-section-header">
            <h3>Pending for {workspaceName}</h3>
            {pendingQuery.isLoading && <span className="sl-muted">Loading…</span>}
          </div>
          {pendingInvites.length === 0 ? (
            <p className="sl-muted" style={{ margin: '8px 0 0' }}>
              No pending invites for this workspace.
            </p>
          ) : (
            <ul className="sl-invite-list">
              {pendingInvites.map((inv) => (
                <li key={inv.id} className="sl-invite-row">
                  <div className="sl-invite-row-primary">
                    <div className="sl-invite-row-name">
                      {inv.email || <em className="sl-muted">Shareable link</em>}
                    </div>
                    <div className="sl-invite-row-meta">
                      {inv.role} · expires {new Date(inv.expires_at).toLocaleDateString()}
                    </div>
                  </div>
                  <div className="sl-invite-row-actions">
                    {inv.token && (
                      <button
                        type="button"
                        className="sl-linkbtn"
                        onClick={() => copy(inviteLinkFor(inv.token!))}
                      >
                        Copy link
                      </button>
                    )}
                    <button
                      type="button"
                      className="sl-linkbtn sl-danger-link"
                      onClick={() => revokeMutation.mutate(inv.id)}
                      disabled={revokeMutation.isPending}
                    >
                      Revoke
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
