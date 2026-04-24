import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';

export function SignupPage(): ReactElement {
  const { signup } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));
  const [email, setEmail] = useState(params.get('email') ?? '');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await signup(email, password, displayName);
      // Invite flow (next is set): skip the /setup workspace-creation wizard
      // and let the accept page enroll the new user into the inviting
      // workspace. For organic signups we still route through /setup.
      if (next) {
        nav(next, { replace: true });
      } else {
        nav('/setup', { replace: true });
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  function safeNext(raw: string | null): string | null {
    if (!raw) return null;
    if (!raw.startsWith('/')) return null;
    if (raw.startsWith('//')) return null;
    return raw;
  }

  return (
    <AuthCard
      heading="Create your account"
      subtext="Set up your SliilS identity. Takes about 30 seconds."
      footer={
        <>
          Already have an account? <Link to="/login">Sign in</Link>
        </>
      }
    >
      <form onSubmit={onSubmit} className="sl-form">
        <label className="sl-field">
          <span>Display name</span>
          <input
            type="text"
            autoComplete="name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="How you want to appear"
          />
        </label>
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
            autoComplete="new-password"
            required
            minLength={10}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <span className="sl-hint">At least 10 characters.</span>
        </label>
        {error && (
          <div role="alert" className="sl-error">
            {error}
          </div>
        )}
        <button type="submit" className="sl-primary" disabled={busy}>
          {busy ? 'Creating account…' : 'Create account'}
        </button>
      </form>
    </AuthCard>
  );
}
