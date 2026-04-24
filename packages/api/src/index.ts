// SliilS API types.
//
// At M0 this is a tiny hand-written set covering only the endpoints the server
// currently exposes (/healthz, /readyz, /api/v1/ping). Starting at M1 (auth),
// this package is regenerated from the server's OpenAPI document so the
// TypeScript types track the Go handlers automatically.

export interface HealthResponse {
  status: 'ok';
  service: string;
}

export interface ReadyResponse {
  status: 'ready' | 'not_ready';
  checks: Record<string, string>;
}

export interface PingResponse {
  pong: string;
}

/**
 * RFC 7807 problem details. Every 4xx/5xx response from the SliilS API is
 * serialized as `application/problem+json` in this shape.
 */
export interface ProblemDetails {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  [key: string]: unknown;
}
