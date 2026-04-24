// Admin-only "Invite people" overlay. Mounts inside WorkspacePage; the
// workspace header's Invite button drives open/close.
//
// Three things in one panel:
//   1. Send form (email optional + role select) → POST /workspaces/:slug/invites
//   2. Pending list with a Copy-link action for each row
//   3. Revoke on any pending row
//
// Copies the shareable URL to the clipboard using the Clipboard API when
// available, falling back to a text-selection trick for older browsers.

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

interface Props {
  workspaceSlug: string;
  workspaceName: string;
  myRole: string; // "owner" | "admin" | "member" | "guest"
  open: boolean;
  onClose: () => void;
}

type Role = 'admin' | 'member' | 'guest';

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
  const canAdmin = myRole === 'owner' || myRole === 'admin';

  const pendingQuery = useQuery({
    queryKey: ['workspace', workspaceSlug, 'invites'],
    queryFn: () => listInvites(workspaceSlug),
    enabled: open && canAdmin,
    staleTime: 10_000,
  });

  const sendMutation = useMutation({
    mutationFn: (body: { email?: string; role?: Role }) =>
      createInvite(workspaceSlug, body),
    onSuccess: (inv) => {
      qc.setQueryData<Invite[]>(
        ['workspace', workspaceSlug, 'invites'],
        (prev = []) => [inv, ...prev],
      );
      setEmail('');
      setRole('member');
      setNotice(
        inv.email
          ? `Invitation sent to ${inv.email}.`
          : 'Link created — copy and share it.',
      );
    },
    onError: (err) => {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Send failed');
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: number) => revokeInvite(workspaceSlug, id),
    onSuccess: (_, id) => {
      qc.setQueryData<Invite[]>(
        ['workspace', workspaceSlug, 'invites'],
        (prev = []) => prev.filter((p) => p.id !== id),
      );
    },
  });

  useEffect(() => {
    if (!open) {
      setEmail('');
      setRole('member');
      setError(null);
      setNotice(null);
    }
  }, [open]);

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
      // fall through to the legacy path
    }
    // Legacy fallback: write into a hidden textarea + execCommand('copy').
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

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    sendMutation.mutate({
      email: email.trim() || undefined,
      role,
    });
  }

  const pendingInvites = useMemo(() => pendingQuery.data ?? [], [pendingQuery.data]);

  if (!open) return null;

  if (!canAdmin) {
    return (
      <div className="sl-modal-backdrop" onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}>
        <div className="sl-modal" onMouseDown={(e) => e.stopPropagation()}>
          <header className="sl-modal-header">
            <h2>Invite to {workspaceName}</h2>
            <button type="button" className="sl-icon-btn" onClick={onClose} aria-label="Close">×</button>
          </header>
          <div className="sl-modal-body">
            <p className="sl-muted">
              Only workspace owners and admins can send invites. Ask someone with admin access to invite people.
            </p>
          </div>
        </div>
      </div>
    );
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
        aria-label={`Invite people to ${workspaceName}`}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="sl-modal-header">
          <h2>Invite people to {workspaceName}</h2>
          <button type="button" className="sl-icon-btn" onClick={onClose} aria-label="Close">×</button>
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
          {error && <div role="alert" className="sl-error">{error}</div>}
          {notice && <div role="status" className="sl-notice">{notice}</div>}
          <div className="sl-modal-actions">
            <button type="button" className="sl-linkbtn" onClick={onClose}>Cancel</button>
            <button type="submit" className="sl-primary" disabled={sendMutation.isPending}>
              {sendMutation.isPending ? 'Sending…' : email.trim() ? 'Send invite' : 'Create link'}
            </button>
          </div>
        </form>

        <section className="sl-modal-section">
          <div className="sl-modal-section-header">
            <h3>Pending invites</h3>
            {pendingQuery.isLoading && <span className="sl-muted">Loading…</span>}
          </div>
          {pendingInvites.length === 0 ? (
            <p className="sl-muted" style={{ margin: '8px 0 0' }}>No pending invites.</p>
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
