// Zod schemas shared between web client and any JS-side validation.
//
// At M0 only the trivial response shapes are covered. M1 adds the auth
// payload schemas (signup/login/refresh) and they live here so both the
// client forms and any BFF code use the same source of truth.

import { z } from 'zod';

export const healthResponseSchema = z.object({
  status: z.literal('ok'),
  service: z.string(),
});
export type HealthResponse = z.infer<typeof healthResponseSchema>;

export const readyResponseSchema = z.object({
  status: z.enum(['ready', 'not_ready']),
  checks: z.record(z.string(), z.string()),
});
export type ReadyResponse = z.infer<typeof readyResponseSchema>;

export const pingResponseSchema = z.object({
  pong: z.string(),
});
export type PingResponse = z.infer<typeof pingResponseSchema>;
