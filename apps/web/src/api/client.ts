// Thin wrapper over fetch that:
// - attaches the current access token (memory-only) to every request
// - refreshes the access token automatically on 401
// - parses RFC 7807 error responses into a typed ApiError
//
// Access tokens are deliberately NOT persisted anywhere — refresh happens via
// the HttpOnly `sliils_refresh` cookie on /api/v1/auth/refresh.

import type { ProblemDetails } from '@sliils/api';

export type AccessTokenProvider = {
  get: () => string | null;
  set: (token: string | null) => void;
};

let provider: AccessTokenProvider = {
  get: () => null,
  set: () => {},
};

export function configureAccessToken(p: AccessTokenProvider): void {
  provider = p;
}

export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails;

  constructor(problem: ProblemDetails) {
    super(problem.detail ?? problem.title);
    this.status = problem.status;
    this.problem = problem;
  }
}

export interface FetchOptions {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  credentials?: RequestCredentials;
  skipRefresh?: boolean;
}

async function raw(path: string, opts: FetchOptions = {}): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...opts.headers,
  };
  const token = provider.get();
  if (token) headers['Authorization'] = `Bearer ${token}`;

  let body: BodyInit | undefined;
  if (opts.body !== undefined && opts.body !== null) {
    headers['Content-Type'] = 'application/json';
    // Accept both pre-stringified callers (body: JSON.stringify(x))
    // and object callers (body: { ... }). Double-stringifying causes
    // the server to receive a JSON-escaped string, which fails to
    // bind into the request struct and returns a 400. This branch
    // passed-through strings so we never re-encode them.
    body =
      typeof opts.body === 'string'
        ? opts.body
        : JSON.stringify(opts.body);
  }

  return fetch(`/api/v1${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body,
    credentials: 'include',
  });
}

async function parseOrThrow<T>(res: Response): Promise<T> {
  if (res.ok) {
    // 204 No Content responses have an empty body — calling .json() on
    // them throws "Unexpected end of JSON input". Handlers that use
    // `apiFetch<void>` (PATCH /dnd, DELETE /devices, role changes,
    // etc.) expect undefined back; return it cleanly.
    if (res.status === 204 || res.headers.get('Content-Length') === '0') {
      return undefined as T;
    }
    const text = await res.text();
    if (!text) return undefined as T;
    return JSON.parse(text) as T;
  }
  let problem: ProblemDetails;
  try {
    problem = (await res.json()) as ProblemDetails;
  } catch {
    problem = {
      type: 'about:blank',
      title: res.statusText,
      status: res.status,
    };
  }
  throw new ApiError(problem);
}

export async function apiFetch<T>(path: string, opts: FetchOptions = {}): Promise<T> {
  let res = await raw(path, opts);

  if (res.status === 401 && !opts.skipRefresh && path !== '/auth/refresh') {
    const refreshed = await tryRefresh();
    if (refreshed) {
      res = await raw(path, opts);
    }
  }
  return parseOrThrow<T>(res);
}

let inflightRefresh: Promise<boolean> | null = null;

export async function tryRefresh(): Promise<boolean> {
  if (inflightRefresh) return inflightRefresh;
  inflightRefresh = (async () => {
    const res = await raw('/auth/refresh', { method: 'POST', skipRefresh: true });
    if (!res.ok) {
      provider.set(null);
      return false;
    }
    const data = (await res.json()) as { access_token: string };
    provider.set(data.access_token);
    return true;
  })();
  try {
    return await inflightRefresh;
  } finally {
    inflightRefresh = null;
  }
}
