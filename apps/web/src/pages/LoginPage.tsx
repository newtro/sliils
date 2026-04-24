import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';

export function LoginPage(): ReactElement {
  const { loginWithPassword } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  // `next` lets us deep-link back to, e.g., /invite/:token after auth.
  // Whitelist to internal paths — never honor a cross-site `next=http://...`.
  const next = safeNext(params.get('next'));
  const [email, setEmail] = useState(params.get('email') ?? '');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const me = await loginWithPassword(email, password);
      const dest = me.needs_setup ? '/setup' : (next ?? '/');
      nav(dest, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthCard
      heading="Sign in"
      subtext="Welcome back. Pick up where you left off."
      footer={
        <>
          <Link to={signupHref(next, params.get('email'))}>Create an account</Link>
          <span className="sl-muted"> · </span>
          <Link to="/magic-link">Email me a sign-in link</Link>
          <span className="sl-muted"> · </span>
          <Link to="/forgot-password">Forgot password?</Link>
        </>
      }
    >
      <form onSubmit={onSubmit} className="sl-form">
        <label className="sl-field">
          <span>Email</span>
          <input
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label className="sl-field">
          <span>Password</span>
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && (
          <div role="alert" className="sl-error">
            {error}
          </div>
        )}
        <button type="submit" className="sl-primary" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </AuthCard>
  );
}

// safeNext accepts an internal path and rejects anything that looks like a
// cross-site URL. Open-redirect guard — without this, an attacker could
// send `/login?next=https://evil.example.com` to phish after login.
function safeNext(raw: string | null): string | null {
  if (!raw) return null;
  if (!raw.startsWith('/')) return null;
  if (raw.startsWith('//')) return null; // protocol-relative
  return raw;
}

function signupHref(next: string | null, emailHint: string | null): string {
  const qs = new URLSearchParams();
  if (next) qs.set('next', next);
  if (emailHint) qs.set('email', emailHint);
  const suffix = qs.toString();
  return suffix ? `/signup?${suffix}` : '/signup';
}
