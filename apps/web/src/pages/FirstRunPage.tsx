// First-run wizard. Mounts at /first-run; rendered when the server
// reports `first_run.completed = false` (no users in the DB yet).
//
// One page, four cards (Admin → Email → Signup mode → Workspace),
// one submit. All fields validate client-side before hitting
// /first-run/bootstrap so a typo doesn't round-trip.

import { useEffect, useMemo, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Navigate, useNavigate } from 'react-router';

import { ApiError } from '../api/client';
import { bootstrapInstall, getFirstRunState } from '../api/firstRun';
import { useAuth } from '../auth/AuthContext';

// AuthContext's completeBootstrap applies the access token from a
// /first-run/bootstrap response and fetches /me so subsequent requests
// authenticate. Keeps the bootstrap response handling centralised.

function slugify(input: string): string {
  return input
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 40);
}

export function FirstRunPage(): ReactElement {
  const nav = useNavigate();
  const { completeBootstrap } = useAuth();

  const [completed, setCompleted] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Admin
  const [adminEmail, setAdminEmail] = useState('');
  const [adminPassword, setAdminPassword] = useState('');
  const [adminDisplay, setAdminDisplay] = useState('');

  // Email provider (optional at bootstrap — can be filled in later)
  const [skipEmail, setSkipEmail] = useState(false);
  const [resendKey, setResendKey] = useState('');
  const [fromAddress, setFromAddress] = useState('');
  const [fromName, setFromName] = useState('');

  // Signup mode
  const [signupMode, setSignupMode] = useState<'open' | 'invite_only'>('invite_only');

  // Workspace
  const [wsName, setWsName] = useState('');
  const [wsSlug, setWsSlug] = useState('');
  const [wsSlugTouched, setWsSlugTouched] = useState(false);
  const [wsDescription, setWsDescription] = useState('');

  useEffect(() => {
    getFirstRunState()
      .then((s) => setCompleted(s.completed))
      .catch(() => setCompleted(false)); // if the endpoint fails, let the admin try anyway
  }, []);

  useEffect(() => {
    if (!wsSlugTouched) setWsSlug(slugify(wsName));
  }, [wsName, wsSlugTouched]);

  const canSubmit = useMemo(() => {
    if (!adminEmail || !adminPassword || adminPassword.length < 10) return false;
    if (!wsName || !wsSlug) return false;
    if (!skipEmail && (!resendKey || !fromAddress)) return false;
    return true;
  }, [adminEmail, adminPassword, wsName, wsSlug, skipEmail, resendKey, fromAddress]);

  if (completed) {
    return <Navigate to="/login" replace />;
  }
  if (completed === null) {
    return <div className="sl-placeholder">Loading…</div>;
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const result = await bootstrapInstall({
        admin: {
          email: adminEmail.trim(),
          password: adminPassword,
          display_name: adminDisplay.trim() || adminEmail.trim(),
        },
        email: skipEmail
          ? {}
          : {
              provider: 'resend',
              resend_api_key: resendKey.trim(),
              from_address: fromAddress.trim(),
              from_name: fromName.trim(),
            },
        signup_mode: signupMode,
        workspace: {
          name: wsName.trim(),
          slug: wsSlug.trim(),
          description: wsDescription.trim(),
        },
      });
      await completeBootstrap(result.access_token);
      nav(`/w/${result.workspace_slug}`, { replace: true });
    } catch (err) {
      setError(
        err instanceof ApiError
          ? (err.problem.detail ?? err.message)
          : err instanceof Error
            ? err.message
            : 'Bootstrap failed',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="sl-firstrun-shell">
      <div className="sl-firstrun-container">
        <header className="sl-firstrun-header">
          <h1 className="sl-firstrun-title">Welcome to SliilS</h1>
          <p className="sl-firstrun-subtitle">
            Let&rsquo;s set up this install. It takes about a minute.
          </p>
        </header>

        <form onSubmit={onSubmit} className="sl-firstrun-form">
          {/* Step 1: Admin */}
          <section className="sl-firstrun-card">
            <div className="sl-firstrun-step">1 · Super-admin account</div>
            <p className="sl-muted" style={{ marginTop: 0 }}>
              This is the operator account. It can edit install-wide settings
              that individual workspace owners can&rsquo;t.
            </p>
            <div className="sl-firstrun-grid">
              <label>
                <span>Display name</span>
                <input
                  type="text"
                  value={adminDisplay}
                  onChange={(e) => setAdminDisplay(e.target.value)}
                  placeholder="How you want to appear"
                />
              </label>
              <label>
                <span>Email</span>
                <input
                  type="email"
                  required
                  value={adminEmail}
                  onChange={(e) => setAdminEmail(e.target.value)}
                  placeholder="you@example.com"
                />
              </label>
              <label>
                <span>Password</span>
                <input
                  type="password"
                  required
                  minLength={10}
                  value={adminPassword}
                  onChange={(e) => setAdminPassword(e.target.value)}
                  placeholder="At least 10 characters"
                />
              </label>
            </div>
          </section>

          {/* Step 2: Email */}
          <section className="sl-firstrun-card">
            <div className="sl-firstrun-step">2 · Email provider</div>
            <p className="sl-muted" style={{ marginTop: 0 }}>
              Used for magic-link / password reset / verify-email. Workspace
              invites default to this too, unless a workspace sets its own.
            </p>
            <label
              className="sl-firstrun-inline-toggle"
              style={{ display: 'flex', alignItems: 'center', gap: 8 }}
            >
              <input
                type="checkbox"
                checked={skipEmail}
                onChange={(e) => setSkipEmail(e.target.checked)}
              />
              <span>Skip — I&rsquo;ll configure email later in Admin → Integrations.</span>
            </label>
            {!skipEmail && (
              <div className="sl-firstrun-grid" style={{ marginTop: 8 }}>
                <label>
                  <span>Resend API key</span>
                  <input
                    type="password"
                    value={resendKey}
                    onChange={(e) => setResendKey(e.target.value)}
                    placeholder="re_..."
                    autoComplete="off"
                  />
                </label>
                <label>
                  <span>From address</span>
                  <input
                    type="email"
                    value={fromAddress}
                    onChange={(e) => setFromAddress(e.target.value)}
                    placeholder="no-reply@yourdomain.com"
                  />
                </label>
                <label>
                  <span>From name</span>
                  <input
                    type="text"
                    value={fromName}
                    onChange={(e) => setFromName(e.target.value)}
                    placeholder="SliilS"
                  />
                </label>
              </div>
            )}
          </section>

          {/* Step 3: Signup mode */}
          <section className="sl-firstrun-card">
            <div className="sl-firstrun-step">3 · Who can sign up</div>
            <p className="sl-muted" style={{ marginTop: 0 }}>
              Controls how new accounts can be created on this install.
            </p>
            <div className="sl-firstrun-radio-group">
              <label className={`sl-firstrun-radio ${signupMode === 'invite_only' ? 'active' : ''}`}>
                <input
                  type="radio"
                  name="signup"
                  checked={signupMode === 'invite_only'}
                  onChange={() => setSignupMode('invite_only')}
                />
                <div>
                  <div className="sl-firstrun-radio-title">Invite only</div>
                  <div className="sl-muted" style={{ fontSize: 13 }}>
                    Only people who get a valid invitation can register. Best
                    for self-hosted team installs.
                  </div>
                </div>
              </label>
              <label className={`sl-firstrun-radio ${signupMode === 'open' ? 'active' : ''}`}>
                <input
                  type="radio"
                  name="signup"
                  checked={signupMode === 'open'}
                  onChange={() => setSignupMode('open')}
                />
                <div>
                  <div className="sl-firstrun-radio-title">Open signup</div>
                  <div className="sl-muted" style={{ fontSize: 13 }}>
                    Anyone can register and create their own workspace.
                    True multi-tenant &mdash; your SliilS becomes a platform.
                  </div>
                </div>
              </label>
            </div>
          </section>

          {/* Step 4: Workspace */}
          <section className="sl-firstrun-card">
            <div className="sl-firstrun-step">4 · Your first workspace</div>
            <p className="sl-muted" style={{ marginTop: 0 }}>
              Every SliilS install needs at least one workspace. You can create
              more later.
            </p>
            <div className="sl-firstrun-grid">
              <label>
                <span>Workspace name</span>
                <input
                  type="text"
                  required
                  value={wsName}
                  onChange={(e) => setWsName(e.target.value)}
                  placeholder="Acme Team"
                />
              </label>
              <label>
                <span>URL slug</span>
                <input
                  type="text"
                  required
                  value={wsSlug}
                  onChange={(e) => {
                    setWsSlug(slugify(e.target.value));
                    setWsSlugTouched(true);
                  }}
                  placeholder="acme"
                />
                <span className="sl-hint">
                  Appears in the URL: /w/<strong>{wsSlug || 'your-slug'}</strong>
                </span>
              </label>
              <label>
                <span>Description (optional)</span>
                <input
                  type="text"
                  value={wsDescription}
                  onChange={(e) => setWsDescription(e.target.value)}
                  placeholder="What this workspace is for"
                />
              </label>
            </div>
          </section>

          {error && (
            <div role="alert" className="sl-error" style={{ whiteSpace: 'pre-wrap' }}>
              {error}
            </div>
          )}
          <div className="sl-firstrun-submit">
            <button type="submit" className="sl-primary" disabled={!canSubmit || busy}>
              {busy ? 'Setting up…' : 'Complete setup'}
            </button>
          </div>
        </form>
      </div>
    </main>
  );
}
