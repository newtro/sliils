import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate } from 'react-router';
import { useAuth } from '../auth/AuthContext';
import { listMyWorkspaces } from '../api/workspaces';
import type { WorkspaceMembership } from '../api/workspaces';

// HomePage is a routing switchboard: once we know the user's state, send
// them to the right place (setup, their only workspace, or an empty state).
export function HomePage(): ReactElement {
  const { user, loading } = useAuth();
  const [memberships, setMemberships] = useState<WorkspaceMembership[] | null>(null);

  useEffect(() => {
    if (!user || user.needs_setup) return;
    let cancelled = false;
    (async () => {
      try {
        const ms = await listMyWorkspaces();
        if (!cancelled) setMemberships(ms);
      } catch {
        if (!cancelled) setMemberships([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [user]);

  if (loading) {
    return (
      <main className="sl-placeholder">
        <div className="sl-card">
          <p className="sl-muted">Loading…</p>
        </div>
      </main>
    );
  }

  if (!user) return <Navigate to="/login" replace />;
  if (user.needs_setup) return <Navigate to="/setup" replace />;

  if (memberships === null) {
    return (
      <main className="sl-placeholder">
        <div className="sl-card">
          <p className="sl-muted">Loading your workspaces…</p>
        </div>
      </main>
    );
  }

  if (memberships.length === 0) {
    // User has no workspaces but needs_setup was false — stale state. Push
    // them back to setup to reconcile.
    return <Navigate to="/setup" replace />;
  }

  // Single-workspace users land on their workspace directly. Multi-workspace
  // users (M7+) will eventually get a picker; for now take the first.
  const target = memberships[0]!.workspace.slug;
  return <Navigate to={`/w/${target}`} replace />;
}
