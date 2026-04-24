import { apiFetch, ApiError, tryRefresh } from './client';

export interface FileDTO {
  id: number;
  filename: string;
  mime: string;
  size_bytes: number;
  width?: number;
  height?: number;
  scan_status: 'pending' | 'clean' | 'infected' | 'failed';
  url: string;
  created_at: string;
}

/**
 * Upload a single file to a workspace. The server stores it under the
 * content-addressed key sha256(processed_bytes) and returns the canonical
 * file record. Re-uploading the same bytes returns the existing record.
 */
export async function uploadFile(workspaceID: number, file: File): Promise<FileDTO> {
  const fd = new FormData();
  fd.append('workspace_id', String(workspaceID));
  fd.append('file', file);

  // apiFetch is JSON-oriented; use a raw fetch with auth so we can send
  // multipart/form-data. We route through the same refresh logic manually.
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;

  let res = await fetch('/api/v1/files', {
    method: 'POST',
    headers,
    body: fd,
    credentials: 'include',
  });
  if (res.status === 401) {
    const ok = await tryRefresh();
    if (ok) {
      const fresh = getAccessToken();
      if (fresh) headers['Authorization'] = `Bearer ${fresh}`;
      res = await fetch('/api/v1/files', {
        method: 'POST',
        headers,
        body: fd,
        credentials: 'include',
      });
    }
  }

  if (!res.ok) {
    let problem;
    try {
      problem = await res.json();
    } catch {
      problem = { type: 'about:blank', title: res.statusText, status: res.status };
    }
    throw new ApiError(problem);
  }
  return (await res.json()) as FileDTO;
}

// peek at the currently-attached access token without importing client
// internals. The module-level provider registered by AuthProvider exposes
// a getter, which we reach for via a small indirection.
let _tokenGetter: () => string | null = () => null;
export function installFilesTokenGetter(get: () => string | null): void {
  _tokenGetter = get;
}
function getAccessToken(): string | null {
  return _tokenGetter();
}

/**
 * fetchFileAsBlobURL downloads authenticated bytes and returns an
 * `object URL` usable as an `<img src>` or `<a href>`. The caller is
 * responsible for calling URL.revokeObjectURL when the component unmounts.
 */
export async function fetchFileAsBlobURL(path: string): Promise<string> {
  // apiFetch JSON-decodes; we need the raw body.
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;

  let res = await fetch(path, { headers, credentials: 'include' });
  if (res.status === 401) {
    const ok = await tryRefresh();
    if (ok) {
      const fresh = getAccessToken();
      if (fresh) headers['Authorization'] = `Bearer ${fresh}`;
      res = await fetch(path, { headers, credentials: 'include' });
    }
  }
  if (!res.ok) {
    throw new ApiError({
      type: 'about:blank',
      title: res.statusText,
      status: res.status,
    });
  }
  const blob = await res.blob();
  return URL.createObjectURL(blob);
}

// Re-export apiFetch so legacy imports of this module still compile if any.
export { apiFetch };
