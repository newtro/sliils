import { useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Navigate, useNavigate } from 'react-router';
import { NotificationsPanel } from '../components/NotificationsPanel';
import { useAuth } from '../auth/AuthContext';
import { ApiError, apiFetch } from '../api/client';
import type { User } from '../auth/AuthContext';

// Profile page. Reached from the rail's avatar chip. Themed to match
// the rest of the authenticated surface — light + dark — using the
// shared design tokens.

export function ProfilePage(): ReactElement {
  const { user, loading } = useAuth();
  const navigate = useNavigate();
  const [displayName, setDisplayName] = useState(user?.display_name ?? '');
  const [status, setStatus] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  if (loading) {
    return <div className="sl-placeholder">Loading your profile…</div>;
  }
  if (!user) {
    return <Navigate to="/login" replace />;
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setStatus(null);
    setBusy(true);
    try {
      await apiFetch<User>('/me', {
        method: 'PATCH',
        body: { display_name: displayName },
      });
      setStatus({ kind: 'ok', text: 'Saved.' });
    } catch (err) {
      const msg =
        err instanceof ApiError ? (err.problem.detail ?? err.message) : 'Save failed';
      setStatus({ kind: 'err', text: msg });
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="sl-profile-shell" style={{ minHeight: '100vh' }}>
      <div className="sl-profile-container">
        <button
          type="button"
          className="sl-linkbtn"
          onClick={() => navigate(-1)}
          style={{ marginBottom: 8 }}
        >
          Back
        </button>
        <h1 className="sl-profile-title">Your profile</h1>
        <div className="sl-profile-subtitle">{user.email}</div>

        <section className="sl-profile-card">
          <h2 className="sl-profile-card-title">Display</h2>
          <form onSubmit={onSubmit} className="sl-profile-row">
            <label>
              <span>Display name</span>
              <input
                type="text"
                maxLength={64}
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                aria-label="Display name"
              />
            </label>
            <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
              <button type="submit" className="sl-primary" disabled={busy}>
                {busy ? 'Saving…' : 'Save'}
              </button>
              {status?.kind === 'ok' && <span className="sl-success">{status.text}</span>}
              {status?.kind === 'err' && <span className="sl-error">{status.text}</span>}
            </div>
          </form>
        </section>

        <section className="sl-profile-card">
          <h2 className="sl-profile-card-title">Notifications</h2>
          <NotificationsPanel />
        </section>
      </div>
    </main>
  );
}
