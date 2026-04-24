import { describe, it, expect } from 'vitest';
import { healthResponseSchema, readyResponseSchema, pingResponseSchema } from './index';

describe('health schema', () => {
  it('accepts a well-formed response', () => {
    const parsed = healthResponseSchema.parse({ status: 'ok', service: 'sliils-app' });
    expect(parsed.service).toBe('sliils-app');
  });

  it('rejects a bad status', () => {
    expect(() => healthResponseSchema.parse({ status: 'weird', service: 'x' })).toThrow();
  });
});

describe('ready schema', () => {
  it('accepts ready and not_ready', () => {
    expect(readyResponseSchema.parse({ status: 'ready', checks: {} })).toEqual({
      status: 'ready',
      checks: {},
    });
    expect(readyResponseSchema.parse({ status: 'not_ready', checks: { db: 'down' } })).toEqual({
      status: 'not_ready',
      checks: { db: 'down' },
    });
  });
});

describe('ping schema', () => {
  it('accepts a pong string', () => {
    expect(pingResponseSchema.parse({ pong: 'sliils' })).toEqual({ pong: 'sliils' });
  });
});
