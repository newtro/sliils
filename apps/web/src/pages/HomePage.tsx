import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate } from 'react-router';
import { useAuth } from '../auth/AuthContext';
import { listMyWorkspaces } from '../api/workspaces';
import type { WorkspaceMembership } from '../api/workspaces';
import { MarketingPage } from './MarketingPage';

// HomePage is the routing switchboard at "/". For anonymous visitors it
// renders the marketing page in-place (so "/" is the true front door,
// not a redirect). Authenticated users get routed onward to setup, their
// workspace, or wherever the user's state dictates.
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

  // Anonymous visitors land on marketing. We render it directly rather
  // than redirecting to /marketing so "/" stays the canonical URL.
  if (!user) return <MarketingPage />;
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
