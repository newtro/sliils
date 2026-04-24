import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';

type Mode = 'request' | 'consuming' | 'sent' | 'error';

export function MagicLinkPage(): ReactElement {
  const { requestMagicLink, consumeMagicLink } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const tokenFromURL = params.get('token');

  const [email, setEmail] = useState('');
  const [mode, setMode] = useState<Mode>(tokenFromURL ? 'consuming' : 'request');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Magic-link consume is single-use on the server. React 19 StrictMode
  // re-runs effects in dev, which would fire the consume twice — the second
  // call fails with "invalid or expired" even though the first succeeded.
  // Guard with a ref keyed on the token so we hit the server exactly once
  // per unique token per page lifetime.
  const consumedRef = useRef<string | null>(null);

  useEffect(() => {
    if (!tokenFromURL) return;
    if (consumedRef.current === tokenFromURL) return;
    consumedRef.current = tokenFromURL;

    // No cancelled-flag + cleanup pattern here: the ref guard already
    // ensures exactly-once. Adding a `cancelled` check would race against
    // StrictMode's cleanup — the cleanup sets cancelled=true after the
    // first effect starts, the second effect is a no-op (guarded by the
    // ref), and the original fetch's success/error handler would find
    // cancelled already true and silently drop both branches, stranding
    // the UI on "Signing you in…" forever.
    (async () => {
      try {
        await consumeMagicLink(tokenFromURL);
        nav('/', { replace: true });
      } catch (err) {
        setMode('error');
        setError(err instanceof ApiError ? (err.problem.detail ?? err.message) : 'Link invalid or expired');
      }
    })();
  }, [tokenFromURL, consumeMagicLink, nav]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await requestMagicLink(email);
      setMode('sent');
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  if (mode === 'consuming') {
    return (
      <AuthCard heading="Signing you in…">
        <p>Verifying your sign-in link.</p>
      </AuthCard>
    );
  }

  if (mode === 'sent') {
    return (
      <AuthCard
        heading="Check your inbox"
        subtext={`If ${email} is on file, a sign-in link is on its way. It expires in 15 minutes.`}
        footer={<Link to="/login">Back to sign in</Link>}
      >
        <p className="sl-muted">You can close this tab — the link will sign you in on the device you open it on.</p>
      </AuthCard>
    );
  }

  if (mode === 'error') {
    return (
      <AuthCard
        heading="That link didn't work"
        subtext="Magic links expire after 15 minutes and can only be used once. Request a fresh one below."
        footer={<Link to="/login">Or use your password</Link>}
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
          {error && <div role="alert" className="sl-error">{error}</div>}
          <button type="submit" className="sl-primary" disabled={busy}>
            {busy ? 'Sending…' : 'Send a new link'}
          </button>
        </form>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      heading="Sign in with email"
      subtext="We'll email you a link that signs you in — no password needed."
      footer={<Link to="/login">Use your password instead</Link>}
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
        {error && <div role="alert" className="sl-error">{error}</div>}
        <button type="submit" className="sl-primary" disabled={busy}>
          {busy ? 'Sending…' : 'Email me a sign-in link'}
        </button>
      </form>
    </AuthCard>
  );
}
