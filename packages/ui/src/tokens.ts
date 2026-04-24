// Design tokens sampled from the SliilS wordmark.
// Keep in sync with apps/web/src/styles/global.css and apps/server/internal/server/splash.html.

export const colors = {
  navy: '#1A2D43',
  navyDeep: '#0F1B2A',
  green: '#5BB85C',
  blue: '#3B7DD8',
  teal: '#27A8AC',
  surface: '#FBFBFD',
  surfaceRaised: '#FFFFFF',
  muted: '#6B7A8F',
} as const;

export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  '2xl': 32,
  '3xl': 48,
  '4xl': 64,
} as const;

export const radius = {
  sm: 4,
  md: 6,
  lg: 8,
  xl: 12,
  '2xl': 16,
  pill: 9999,
} as const;

export type ColorToken = keyof typeof colors;
export type SpacingToken = keyof typeof spacing;
export type RadiusToken = keyof typeof radius;
