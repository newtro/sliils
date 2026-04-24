import { describe, it, expect } from 'vitest';
import { colors, spacing, radius } from './tokens';

describe('design tokens', () => {
  it('exposes the brand navy', () => {
    expect(colors.navy).toBe('#1A2D43');
  });

  it('exposes both person colors from the wordmark', () => {
    expect(colors.green).toBe('#5BB85C');
    expect(colors.blue).toBe('#3B7DD8');
  });

  it('spacing matches shadcn/Tailwind defaults', () => {
    expect(spacing.sm).toBe(8);
    expect(spacing.lg).toBe(16);
  });

  it('exposes a pill radius for rounded chips', () => {
    expect(radius.pill).toBe(9999);
  });
});
