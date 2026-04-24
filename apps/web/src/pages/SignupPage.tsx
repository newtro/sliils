import { useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';
import { getInstallStatus } from '../api/install';
import type { SignupMode } from '../api/install';

export function SignupPage(): ReactElement {
  const { signup } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));
  const inviteToken = params.get('invite') ?? extractTokenFromNext(next);

  const [email, setEmail] = useState(params.get('email') ?? '');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Hide the signup form when the install is invite-only and the user
  // isn't arriving with a valid token. Keeps truly-private installs
  // from exposing a registration UI they can't actually use.
  const [signupMode, setSignupMode] = useState<SignupMode | null>(null);
  useEffect(() => {
    getInstallStatus()
      .then((s) => setSignupMode(s.signup_mode))
      .catch(() => setSignupMode('open')); // fall open so a broken /install/status doesn't block signup
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await signup(email, password, displayName, inviteToken ?? undefined);
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

  if (signupMode === 'invite_only' && !inviteToken) {
    return (
      <AuthCard
        heading="Signup is invite-only"
        subtext="Ask a workspace owner for an invitation link."
        footer={
          <>
            Already have an account? <Link to="/login">Sign in</Link>
          </>
        }
      >
        <p className="sl-muted">
          This SliilS install doesn&rsquo;t allow open signup. Once someone
          sends you an invitation you&rsquo;ll receive a link that lets you
          register.
        </p>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      heading="Create your account"
      subtext={
        inviteToken
          ? 'You were invited. Complete signup to join the workspace.'
          : 'Set up your SliilS identity. Takes about 30 seconds.'
      }
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

function safeNext(raw: string | null): string | null {
  if (!raw) return null;
  if (!raw.startsWith('/')) return null;
  if (raw.startsWith('//')) return null;
  return raw;
}

// When signup is launched from an invite accept flow, the `next` param
// is /invite/<token>. Pull the token out so we can pre-satisfy the
// signup gate on invite-only installs.
function extractTokenFromNext(next: string | null): string | null {
  if (!next) return null;
  const m = next.match(/^\/invite\/([^/?#]+)/);
  return m?.[1] ?? null;
}
