import { describe, it, expect } from 'vitest';
import type { HealthResponse, ReadyResponse, ProblemDetails } from './index';

describe('@sliils/api types', () => {
  it('HealthResponse shape is constructible', () => {
    const h: HealthResponse = { status: 'ok', service: 'sliils-app' };
    expect(h.status).toBe('ok');
  });

  it('ReadyResponse shape is constructible', () => {
    const r: ReadyResponse = { status: 'ready', checks: { db: 'ok' } };
    expect(r.checks.db).toBe('ok');
  });

  it('ProblemDetails follows RFC 7807', () => {
    const p: ProblemDetails = {
      type: 'https://sliils.com/problems/not-found',
      title: 'Resource not found',
      status: 404,
    };
    expect(p.status).toBe(404);
  });
});
