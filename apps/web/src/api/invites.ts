import { apiFetch } from './client';

// Invite API (M7A).
//
// Two shapes:
//   - `Invite` — the admin's view of a pending row (includes token, which
//     is only returned to the admin who created it).
//   - `InvitePreview` — the unauthenticated visitor's view at /invite/:token.
//     Deliberately narrow: enough to show "Join Foo workspace" without
//     leaking anything the token holder couldn't already see.

export interface Invite {
  id: number;
  workspace_id: number;
  token?: string;
  email?: string;
  role: 'admin' | 'member' | 'guest';
  created_at: string;
  expires_at: string;
  creator_display_name?: string;
  creator_email?: string;
  // Only populated on the create response — reports whether the email
  // actually dispatched so the UI can surface a real status instead of
  // a blanket "sent" claim.
  email_status?: 'sent' | 'failed' | 'skipped' | '';
  email_error?: string;
}

export interface InvitePreview {
  workspace_slug: string;
  workspace_name: string;
  workspace_description?: string;
  email?: string;
  role: 'admin' | 'member' | 'guest';
  expires_at: string;
  accepted: boolean;
  revoked: boolean;
  expired: boolean;
}

export interface AcceptInviteResult {
  workspace_slug: string;
  workspace_name: string;
  role: 'admin' | 'member' | 'guest';
}

export function createInvite(
  slug: string,
  body: { email?: string; role?: 'admin' | 'member' | 'guest' },
): Promise<Invite> {
  return apiFetch<Invite>(`/workspaces/${encodeURIComponent(slug)}/invites`, {
    method: 'POST',
    body,
  });
}

export function listInvites(slug: string): Promise<Invite[]> {
  return apiFetch<Invite[]>(`/workspaces/${encodeURIComponent(slug)}/invites`);
}

export function revokeInvite(slug: string, inviteID: number): Promise<void> {
  return apiFetch<void>(`/workspaces/${encodeURIComponent(slug)}/invites/${inviteID}`, {
    method: 'DELETE',
  });
}

export function previewInvite(token: string): Promise<InvitePreview> {
  // skipRefresh so an unauthenticated visitor doesn't trigger a 401 refresh
  // dance on the marketing / signup path — they may have no session yet.
  return apiFetch<InvitePreview>(`/invites/${encodeURIComponent(token)}`, {
    skipRefresh: true,
  });
}

export function acceptInvite(token: string): Promise<AcceptInviteResult> {
  return apiFetch<AcceptInviteResult>(`/invites/${encodeURIComponent(token)}/accept`, {
    method: 'POST',
  });
}
