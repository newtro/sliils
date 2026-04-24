import { apiFetch } from './client';

export interface Workspace {
  id: number;
  slug: string;
  name: string;
  description: string;
  brand_color?: string;
  created_at: string;
}

export interface CustomStatus {
  emoji?: string;
  text?: string;
  expires_at?: string;
  [key: string]: unknown;
}

export type NotifyPref = 'all' | 'mentions' | 'mute';

export interface WorkspaceMembership {
  workspace: Workspace;
  role: 'owner' | 'admin' | 'member' | 'guest';
  joined_at: string;
  custom_status?: CustomStatus;
  notify_pref: NotifyPref;
}

export interface Channel {
  id: number;
  workspace_id: number;
  type: 'public' | 'private' | 'dm' | 'group_dm';
  name?: string;
  topic: string;
  description: string;
  default_join: boolean;
  created_at: string;
  last_read_message_id?: number;
  unread_count: number;
  mention_count: number;
}

export interface WorkspaceMember {
  user_id: number;
  display_name: string;
  email: string;
  role: 'owner' | 'admin' | 'member' | 'guest';
  email_verified_at?: string;
}

export function listMyWorkspaces(): Promise<WorkspaceMembership[]> {
  return apiFetch<WorkspaceMembership[]>('/me/workspaces');
}

export function createWorkspace(input: {
  name: string;
  slug: string;
  description: string;
}): Promise<Workspace> {
  return apiFetch<Workspace>('/workspaces', { method: 'POST', body: input });
}

export function getWorkspace(slug: string): Promise<Workspace> {
  return apiFetch<Workspace>(`/workspaces/${encodeURIComponent(slug)}`);
}

export function listWorkspaceChannels(slug: string): Promise<Channel[]> {
  return apiFetch<Channel[]>(`/workspaces/${encodeURIComponent(slug)}/channels`);
}

export function createWorkspaceChannel(
  slug: string,
  input: { name: string; topic?: string; description?: string },
): Promise<Channel> {
  return apiFetch<Channel>(`/workspaces/${encodeURIComponent(slug)}/channels`, {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function listWorkspaceMembers(slug: string): Promise<WorkspaceMember[]> {
  return apiFetch<WorkspaceMember[]>(`/workspaces/${encodeURIComponent(slug)}/members`);
}

// ---- M8A: DMs (direct messages) -----------------------------------------

export interface DM {
  channel_id: number;
  other_user_id: number;
  other_display_name: string;
  other_email: string;
  created_at: string;
}

export function findOrCreateDM(slug: string, userID: number): Promise<DM> {
  return apiFetch<DM>(`/workspaces/${encodeURIComponent(slug)}/dms`, {
    method: 'POST',
    body: { user_id: userID },
  });
}

export function listDMs(slug: string): Promise<DM[]> {
  return apiFetch<DM[]>(`/workspaces/${encodeURIComponent(slug)}/dms`);
}

// ---- M7B: custom status -------------------------------------------------

export function setMyStatus(
  slug: string,
  body: { emoji?: string; text?: string; expires_at?: string },
): Promise<{ workspace_id: number; custom_status: CustomStatus }> {
  return apiFetch(`/me/workspaces/${encodeURIComponent(slug)}/status`, {
    method: 'PATCH',
    body,
  });
}

export function clearMyStatus(slug: string): Promise<{ workspace_id: number; custom_status: CustomStatus }> {
  return apiFetch(`/me/workspaces/${encodeURIComponent(slug)}/status`, {
    method: 'PATCH',
    body: {},
  });
}

// ---- M7C: workspace-level notification preference -----------------------

export function setMyNotifyPref(
  slug: string,
  notifyPref: NotifyPref,
): Promise<{ workspace_id: number; notify_pref: NotifyPref }> {
  return apiFetch(`/me/workspaces/${encodeURIComponent(slug)}/notify-pref`, {
    method: 'PATCH',
    body: { notify_pref: notifyPref },
  });
}
