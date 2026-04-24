import { useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';

export function ForgotPasswordPage(): ReactElement {
  const { requestPasswordReset } = useAuth();
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await requestPasswordReset(email);
      setSent(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <AuthCard
        heading="Check your inbox"
        subtext={`If ${email} is on file, a password-reset link is on its way. It expires in 1 hour.`}
        footer={<Link to="/login">Back to sign in</Link>}
      />
    );
  }

  return (
    <AuthCard
      heading="Reset your password"
      subtext="Enter your account email and we'll send you a reset link."
      footer={<Link to="/login">Back to sign in</Link>}
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
          {busy ? 'Sending…' : 'Send reset link'}
        </button>
      </form>
    </AuthCard>
  );
}

export function ResetPasswordPage(): ReactElement {
  const { confirmPasswordReset } = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const token = params.get('token') ?? '';

  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [missingToken, setMissingToken] = useState(false);

  useEffect(() => {
    if (!token) setMissingToken(true);
  }, [token]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError("Passwords don't match");
      return;
    }
    setBusy(true);
    try {
      await confirmPasswordReset(token, password);
      nav('/login', { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  if (missingToken) {
    return (
      <AuthCard
        heading="Reset link missing"
        subtext="This page expects a ?token=… in the URL from a password-reset email."
        footer={<Link to="/forgot-password">Request a new link</Link>}
      />
    );
  }

  return (
    <AuthCard heading="Set a new password" footer={<Link to="/login">Back to sign in</Link>}>
      <form onSubmit={onSubmit} className="sl-form">
        <label className="sl-field">
          <span>New password</span>
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
        <label className="sl-field">
          <span>Confirm password</span>
          <input
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </label>
        {error && <div role="alert" className="sl-error">{error}</div>}
        <button type="submit" className="sl-primary" disabled={busy}>
          {busy ? 'Updating…' : 'Update password'}
        </button>
      </form>
    </AuthCard>
  );
}
