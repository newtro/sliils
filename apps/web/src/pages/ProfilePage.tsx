import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Link, Navigate } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError, apiFetch } from '../api/client';
import type { User } from '../auth/AuthContext';

export function ProfilePage(): ReactElement {
  const { user, loading } = useAuth();
  const [displayName, setDisplayName] = useState(user?.display_name ?? '');
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (loading) {
    return <AuthCard heading="Loading your profile…" />;
  }
  if (!user) {
    return <Navigate to="/login" replace />;
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSaved(false);
    setBusy(true);
    try {
      await apiFetch<User>('/me', {
        method: 'PATCH',
        body: { display_name: displayName },
      });
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.problem.detail ?? err.message : 'Save failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthCard
      heading="Your profile"
      subtext={user.email}
      footer={<Link to="/">Back home</Link>}
    >
      <form onSubmit={onSubmit} className="sl-form">
        <label className="sl-field">
          <span>Display name</span>
          <input
            type="text"
            maxLength={64}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </label>
        {error && <div role="alert" className="sl-error">{error}</div>}
        {saved && (
          <div role="status" className="sl-success">
            Saved.
          </div>
        )}
        <button type="submit" className="sl-primary" disabled={busy}>
          {busy ? 'Saving…' : 'Save'}
        </button>
      </form>
    </AuthCard>
  );
}
