import { useEffect, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { Link, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';

type State = 'verifying' | 'ok' | 'error' | 'missing';

export function VerifyEmailPage(): ReactElement {
  const { verifyEmail } = useAuth();
  const [params] = useSearchParams();
  const token = params.get('token');
  const [state, setState] = useState<State>(token ? 'verifying' : 'missing');
  const [error, setError] = useState<string | null>(null);

  // Email-verify tokens are single-use; StrictMode's double-effect would
  // consume successfully on the first call and fail on the second, making
  // a valid verification look broken. Guard with a token-keyed ref.
  const verifiedRef = useRef<string | null>(null);

  useEffect(() => {
    if (!token) return;
    if (verifiedRef.current === token) return;
    verifiedRef.current = token;

    // Ref-guard alone; no cancelled flag. StrictMode's cleanup would
    // set cancelled=true before the in-flight verify returns, stranding
    // the UI on "Verifying…" forever.
    (async () => {
      try {
        await verifyEmail(token);
        setState('ok');
      } catch (err) {
        setState('error');
        setError(err instanceof ApiError ? (err.problem.detail ?? err.message) : 'Verification failed');
      }
    })();
  }, [token, verifyEmail]);

  if (state === 'verifying') {
    return <AuthCard heading="Verifying your email…" subtext="One moment." />;
  }
  if (state === 'ok') {
    return (
      <AuthCard
        heading="Email verified"
        subtext="Thanks — your SliilS account is fully activated."
        footer={<Link to="/">Continue</Link>}
      />
    );
  }
  if (state === 'missing') {
    return (
      <AuthCard
        heading="No verification token"
        subtext="Open this page from the link in your verification email."
        footer={<Link to="/">Home</Link>}
      />
    );
  }
  return (
    <AuthCard
      heading="We couldn't verify this link"
      subtext={error ?? 'The verification link is invalid or has expired.'}
      footer={<Link to="/">Home</Link>}
    />
  );
}
