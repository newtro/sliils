// Centralised user-facing strings.
//
// This is the v1 i18n surface: every user-facing string that doesn't
// come from API data should live here. A future commit wires
// react-intl or an equivalent to load alternate locales at runtime;
// for now the keys are a stable contract so components don't regress.
//
// Pattern — in a component:
//
//   import { t } from '../i18n/messages';
//   <button>{t('page.archive')}</button>
//
// When a new string appears in UI, add the key here + reference from
// the component. Reviewers can grep for hardcoded strings as a
// regression gate; tooling (`pnpm lint:i18n` in a future commit)
// formalises it.

type MessageBundle = Record<string, string>;

// en-US is the source-of-truth locale. Translation bundles extend
// this shape; missing keys fall back to en-US.
const en: MessageBundle = {
  // Global actions
  'common.cancel': 'Cancel',
  'common.save': 'Save',
  'common.delete': 'Delete',
  'common.close': 'Close',
  'common.loading': 'Loading…',
  'common.error.generic': 'Something went wrong.',

  // Auth
  'auth.login.heading': 'Sign in',
  'auth.signup.heading': 'Create your account',
  'auth.forgot-password.heading': 'Reset your password',
  'auth.magic-link.sent': 'Check your email for a sign-in link.',

  // Pages
  'page.new': '+ New page',
  'page.creating': 'Creating…',
  'page.archive': 'Archive',
  'page.archive.confirm': 'Archive "{title}"?',
  'page.untitled': 'Untitled',
  'page.loading': 'Loading…',
  'page.last-edited': 'Last edited {when}',

  // Notifications
  'notifications.heading': 'Notifications',
  'notifications.enable': 'Enable notifications',
  'notifications.enabled': 'Enabled on this device',
  'notifications.disable': 'Disable',
  'notifications.unsupported': 'This browser does not support web push.',
  'notifications.blocked': 'Notifications are blocked in your browser settings.',
  'notifications.dnd': 'Do not disturb',
  'notifications.snooze': 'Snooze',
  'notifications.quiet-hours.start': 'Quiet hours start',
  'notifications.quiet-hours.end': 'Quiet hours end',

  // Calendar
  'calendar.new-event': '+ New event',
  'calendar.rsvp.yes': 'Yes',
  'calendar.rsvp.no': 'No',
  'calendar.rsvp.maybe': 'Maybe',
};

// Registry of all supported locales. en is always present; others
// lazy-load via dynamic imports in v1.1.
const bundles: Record<string, MessageBundle> = {
  'en-US': en,
  en,
};

let currentLocale = 'en';

export function setLocale(locale: string): void {
  if (bundles[locale]) {
    currentLocale = locale;
  }
}

export function getLocale(): string {
  return currentLocale;
}

/**
 * Resolve a message key to its localised string. Supports simple
 * `{placeholder}` interpolation.
 *
 * Missing keys fall back to the key itself so a missed translation
 * shows something recognisable at runtime rather than crashing.
 */
export function t(key: string, vars?: Record<string, string | number>): string {
  const bundle = bundles[currentLocale] ?? en;
  const template = bundle[key] ?? en[key] ?? key;
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (_, name) =>
    vars[name] !== undefined ? String(vars[name]) : `{${name}}`,
  );
}

// Register additional locales (wire this from a locale-switcher UI in
// v1.1). Bundles can be partial — missing keys fall back to en.
export function registerBundle(locale: string, bundle: Partial<MessageBundle>): void {
  const merged: MessageBundle = { ...en };
  for (const [k, v] of Object.entries(bundle)) {
    if (typeof v === 'string') merged[k] = v;
  }
  bundles[locale] = merged;
}
