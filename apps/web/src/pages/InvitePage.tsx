// /invite/:token — the page an invitee lands on after clicking an email link
// (or following a shared URL). Walks four states:
//
//   1. Loading — fetching the preview.
//   2. Unclaimable — revoked / expired / already accepted / 404. Terminal.
//   3. Unauthenticated — show the workspace card + a prompt to sign in or
//      sign up. After auth, the same route re-enters and falls through to
//      case 4. We prefill the signup email if the invite was email-targeted.
//   4. Authenticated — one-click "Join {workspace}" button that calls
//      POST /invites/:token/accept then navigates to /w/:slug.
//
// We do NOT auto-accept on mount. A human tap is a useful consent signal
// — both for the invitee ("yes, I'm joining this") and for the audit log.

import { useCallback, useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router';

import { ApiError } from '../api/client';
import { acceptInvite, previewInvite } from '../api/invites';
import type { InvitePreview } from '../api/invites';
import { useAuth } from '../auth/AuthContext';

export function InvitePage(): ReactElement {
  const { token = '' } = useParams();
  const navigate = useNavigate();
  const { user, loading: authLoading, refreshMe } = useAuth();

  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [accepting, setAccepting] = useState(false);

  useEffect(() => {
    if (!token) {
      setError('Missing invite token.');
      setLoading(false);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const p = await previewInvite(token);
        if (!cancelled) setPreview(p);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 404) {
          setError("This invite link doesn't exist or has been deleted.");
        } else {
          setError(err instanceof Error ? err.message : 'Failed to load invite.');
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);

  const onAccept = useCallback(async () => {
    if (!token) return;
    setAccepting(true);
    setError(null);
    try {
      const result = await acceptInvite(token);
      // Refresh /me so `needs_setup` clears before WorkspacePage mounts —
      // otherwise a newly-signed-up invitee bounces straight to /setup
      // despite being a valid member of the target workspace now.
      await refreshMe();
      navigate(`/w/${result.workspace_slug}`, { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.problem.detail ?? err.message);
      } else {
        setError(err instanceof Error ? err.message : 'Accept failed.');
      }
    } finally {
      setAccepting(false);
    }
  }, [token, navigate, refreshMe]);

  if (loading || authLoading) {
    return (
      <div className="sl-placeholder">
        <div className="sl-card" style={{ maxWidth: 480 }}>
          <p className="sl-muted">Loading invitation…</p>
        </div>
      </div>
    );
  }

  if (error && !preview) {
    return (
      <div className="sl-placeholder">
        <div className="sl-card" style={{ maxWidth: 480 }}>
          <h1 className="sl-auth-heading">Invite unavailable</h1>
          <p className="sl-error" role="alert">{error}</p>
          <Link to="/">Go home</Link>
        </div>
      </div>
    );
  }

  if (!preview) {
    return <Navigate to="/" replace />;
  }

  const terminal =
    preview.accepted || preview.revoked || preview.expired;

  return (
    <div className="sl-placeholder">
      <div className="sl-card" style={{ maxWidth: 480 }}>
        <h1 className="sl-auth-heading" aria-label="SliilS">
          {'Sl'}
          <span className="sl-i-green" aria-hidden="true">i</span>
          <span className="sl-i-blue" aria-hidden="true">i</span>
          {'lS'}
        </h1>
        <h2 className="sl-auth-subhead">You&rsquo;ve been invited to</h2>
        <div className="sl-invite-card">
          <div className="sl-invite-ws-name">{preview.workspace_name}</div>
          {preview.workspace_description && (
            <div className="sl-invite-ws-desc">{preview.workspace_description}</div>
          )}
          <div className="sl-invite-role sl-muted">
            Role on accept: <strong>{preview.role}</strong>
            {preview.email && (
              <>
                {' · '}Invited as <strong>{preview.email}</strong>
              </>
            )}
          </div>
        </div>

        {terminal ? (
          <div className="sl-error" role="alert" style={{ marginTop: 16 }}>
            {preview.accepted && 'This invite has already been accepted.'}
            {preview.revoked && 'This invite was revoked by a workspace admin.'}
            {preview.expired && 'This invite has expired. Ask the sender for a new one.'}
          </div>
        ) : user ? (
          <>
            <p className="sl-auth-note" style={{ marginTop: 16 }}>
              Signed in as <strong>{user.email}</strong>.{' '}
              {preview.email && preview.email.toLowerCase() !== user.email.toLowerCase() && (
                <span style={{ color: 'var(--danger)' }}>
                  This invite is for {preview.email}.{' '}
                  <Link to="/login">Sign in as that user</Link> to accept.
                </span>
              )}
            </p>
            <button
              type="button"
              className="sl-primary"
              style={{ width: '100%', marginTop: 12 }}
              disabled={
                accepting ||
                (!!preview.email &&
                  preview.email.toLowerCase() !== user.email.toLowerCase())
              }
              onClick={onAccept}
            >
              {accepting ? 'Joining…' : `Join ${preview.workspace_name}`}
            </button>
            {error && (
              <p className="sl-error" role="alert" style={{ marginTop: 12 }}>
                {error}
              </p>
            )}
          </>
        ) : (
          <div style={{ marginTop: 16 }}>
            <p className="sl-auth-note">Sign in or create an account to join.</p>
            <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
              <Link
                className="sl-primary"
                style={{ flex: 1, textAlign: 'center', textDecoration: 'none' }}
                to={`/login?next=${encodeURIComponent(`/invite/${token}`)}${
                  preview.email ? `&email=${encodeURIComponent(preview.email)}` : ''
                }`}
              >
                Sign in
              </Link>
              <Link
                className="sl-secondary"
                style={{ flex: 1, textAlign: 'center', textDecoration: 'none' }}
                to={`/signup?next=${encodeURIComponent(`/invite/${token}`)}${
                  preview.email ? `&email=${encodeURIComponent(preview.email)}` : ''
                }`}
              >
                Create account
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
