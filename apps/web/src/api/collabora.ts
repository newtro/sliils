import { apiFetch } from './client';

// WOPI / Collabora integration (M10-P2). The server-side flow is:
//   1. POST /files/:id/edit-session           → edit URL + access_token
//   2. Browser opens <iframe src={edit_url}>  → Collabora loads the doc
//   3. Collabora calls GET/POST /api/v1/wopi/files/... back on us
//      using the access_token we issued, and we verify the HMAC before
//      streaming bytes.
//
// This module owns step 1 — the UI just calls openInCollabora(fileId).

export interface EditSession {
  edit_url: string;
  wopi_src: string;
  access_token: string;
  access_token_ttl: number; // ms since epoch
  expires_at: string;
  can_write: boolean;
}

export function createEditSession(fileID: number, opts?: { view?: boolean }): Promise<EditSession> {
  const qs = opts?.view ? '?mode=view' : '';
  return apiFetch<EditSession>(`/files/${fileID}/edit-session${qs}`, { method: 'POST' });
}

// isCollaboraEditable keeps the "Open in editor" button from showing on
// MIME types Collabora doesn't handle. Mirrors the server's allow-list
// in internal/wopi/wopi.go so the two stay consistent.
const collaboraMimePrefixes = [
  'application/vnd.openxmlformats-officedocument',
  'application/vnd.oasis.opendocument',
];
const collaboraMimes = new Set([
  'application/msword',
  'application/vnd.ms-excel',
  'application/vnd.ms-powerpoint',
  'application/rtf',
  'text/csv',
]);
const collaboraExts = new Set([
  'docx', 'xlsx', 'pptx',
  'doc', 'xls', 'ppt',
  'odt', 'ods', 'odp',
  'rtf', 'csv',
]);

export function isCollaboraEditable(mime: string, filename: string): boolean {
  const m = mime.toLowerCase();
  if (collaboraMimes.has(m)) return true;
  if (collaboraMimePrefixes.some((p) => m.startsWith(p))) return true;
  const ext = filename.split('.').pop()?.toLowerCase();
  return !!ext && collaboraExts.has(ext);
}
