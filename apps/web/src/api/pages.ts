import { apiFetch } from './client';

// Pages API (M10).
//
// The server owns metadata + auth-token issuance. Content streaming happens
// out-of-band via Y-Sweet's WebSocket — the browser connects directly using
// the URL + token returned by issuePageAuth.

export interface Page {
  id: number;
  workspace_id: number;
  channel_id?: number | null;
  title: string;
  icon?: string;
  doc_id: string;
  created_by?: number;
  creator_display_name?: string;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
}

export interface PageAuth {
  url: string;
  base_url: string;
  doc_id: string;
  token: string;
  expires_at: string;
}

export interface PageSnapshot {
  id: number;
  page_id: number;
  byte_size: number;
  reason: string;
  created_by?: number;
  creator_display_name?: string;
  created_at: string;
}

export interface PageComment {
  id: number;
  page_id: number;
  parent_id?: number | null;
  author_id?: number;
  author_display_name?: string;
  anchor?: string;
  body_md: string;
  resolved_at?: string | null;
  created_at: string;
  updated_at: string;
}

export function listPages(slug: string): Promise<Page[]> {
  return apiFetch<Page[]>(`/workspaces/${encodeURIComponent(slug)}/pages`);
}

export function createPage(
  slug: string,
  input: { title?: string; icon?: string; channel_id?: number | null },
): Promise<Page> {
  return apiFetch<Page>(`/workspaces/${encodeURIComponent(slug)}/pages`, {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function getPage(pageID: number): Promise<Page> {
  return apiFetch<Page>(`/pages/${pageID}`);
}

export function patchPage(
  pageID: number,
  patch: { title?: string; icon?: string; channel_id?: number | null; clear_channel?: boolean },
): Promise<Page> {
  return apiFetch<Page>(`/pages/${pageID}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

export function archivePage(pageID: number): Promise<void> {
  return apiFetch<void>(`/pages/${pageID}`, { method: 'DELETE' });
}

export function issuePageAuth(pageID: number): Promise<PageAuth> {
  return apiFetch<PageAuth>(`/pages/${pageID}/auth`, { method: 'POST' });
}

export function listPageSnapshots(pageID: number): Promise<PageSnapshot[]> {
  return apiFetch<PageSnapshot[]>(`/pages/${pageID}/snapshots`);
}

export function createPageSnapshot(pageID: number): Promise<PageSnapshot> {
  return apiFetch<PageSnapshot>(`/pages/${pageID}/snapshots`, { method: 'POST' });
}

export function restorePageSnapshot(pageID: number, snapshotID: number): Promise<void> {
  return apiFetch<void>(`/pages/${pageID}/snapshots/${snapshotID}/restore`, {
    method: 'POST',
  });
}

export function listPageComments(pageID: number): Promise<PageComment[]> {
  return apiFetch<PageComment[]>(`/pages/${pageID}/comments`);
}

export function createPageComment(
  pageID: number,
  input: { body_md: string; parent_id?: number; anchor?: string },
): Promise<PageComment> {
  return apiFetch<PageComment>(`/pages/${pageID}/comments`, {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

export function patchPageComment(
  commentID: number,
  patch: { body_md?: string; resolved?: boolean },
): Promise<PageComment> {
  return apiFetch<PageComment>(`/comments/${commentID}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

export function deletePageComment(commentID: number): Promise<void> {
  return apiFetch<void>(`/comments/${commentID}`, { method: 'DELETE' });
}
