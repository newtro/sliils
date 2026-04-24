// Far-left rail shared by every workspace-scoped page (channels list,
// Pages, Calendar, Admin). Renders the brand, workspace switcher,
// cross-feature nav (Pages / Calendar / Admin), and the bottom
// theme-toggle + sign-out cluster.
//
// Active detection works off the URL path so whichever page is
// currently rendered lights up its own icon without prop drilling.

import type { ReactElement } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';
import { useQuery } from '@tanstack/react-query';

import { listMyWorkspaces } from '../api/workspaces';
import type { WorkspaceMembership } from '../api/workspaces';
import { useAuth } from '../auth/AuthContext';
import { ThemeToggle } from '../theme/ThemeToggle';

interface Props {
  /** Slug of the workspace the surrounding page is rendering. */
  activeSlug: string;
}

export function WorkspaceRail({ activeSlug }: Props): ReactElement {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  const mshipQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: !!user,
    staleTime: 30_000,
  });
  const memberships = mshipQuery.data ?? [];
  const current: WorkspaceMembership | null =
    memberships.find((m) => m.workspace.slug === activeSlug) ?? null;
  const canAdmin = current?.role === 'owner' || current?.role === 'admin';

  // Figure out which cross-feature tab is active from the URL path.
  const isPagesActive = location.pathname.startsWith(`/w/${activeSlug}/pages`);
  const isCalendarActive = location.pathname.startsWith(`/w/${activeSlug}/calendar`);
  const isAdminActive = location.pathname.startsWith(`/w/${activeSlug}/admin`);

  return (
    <aside className="sl-ws-rail" aria-label="Workspace navigation">
      <button
        type="button"
        className="sl-ws-brand"
        title="SliilS home"
        aria-label="SliilS home"
        onClick={() => navigate('/')}
      >
        <img src="/favicon.png" alt="" />
      </button>
      <div className="sl-ws-rail-sep" aria-hidden="true" />

      {/* Workspace switcher tiles. Active whenever we're ANYWHERE
       * inside this workspace — the tabs below the tile indicate
       * which surface is active within it. */}
      {memberships.map((m) => (
        <Link
          key={m.workspace.id}
          to={`/w/${m.workspace.slug}`}
          className={`sl-ws-rail-icon ${m.workspace.slug === activeSlug ? 'active' : ''}`}
          title={m.workspace.name}
          aria-current={m.workspace.slug === activeSlug ? 'true' : undefined}
        >
          {m.workspace.name[0]?.toUpperCase() ?? '?'}
        </Link>
      ))}

      {/* Create a new workspace. Always visible — lets users start a
       * second workspace without having to find a settings menu. */}
      <button
        type="button"
        className="sl-ws-rail-icon sl-ws-rail-icon-btn"
        title="Create a new workspace"
        aria-label="Create a new workspace"
        onClick={() => navigate('/setup')}
      >
        <PlusIcon />
      </button>

      {/* Cross-feature nav for the active workspace. Only shows when a
       * current workspace is resolved. */}
      {current && (
        <>
          <div className="sl-ws-rail-sep" aria-hidden="true" />
          <Link
            to={`/w/${activeSlug}/pages`}
            className={`sl-ws-rail-icon ${isPagesActive ? 'active' : ''}`}
            title="Pages"
            aria-label="Pages"
            aria-current={isPagesActive ? 'page' : undefined}
          >
            <PagesIcon />
          </Link>
          <Link
            to={`/w/${activeSlug}/calendar`}
            className={`sl-ws-rail-icon ${isCalendarActive ? 'active' : ''}`}
            title="Calendar"
            aria-label="Calendar"
            aria-current={isCalendarActive ? 'page' : undefined}
          >
            <CalendarIcon />
          </Link>
          {canAdmin && (
            <Link
              to={`/w/${activeSlug}/admin`}
              className={`sl-ws-rail-icon ${isAdminActive ? 'active' : ''}`}
              title="Admin"
              aria-label="Admin"
              aria-current={isAdminActive ? 'page' : undefined}
            >
              <AdminIcon />
            </Link>
          )}
        </>
      )}

      <div className="sl-ws-rail-spacer" />
      <div className="sl-ws-rail-bottom">
        <ThemeToggle />
        <Link
          to="/me"
          className={`sl-ws-rail-icon ${location.pathname === '/me' ? 'active' : ''}`}
          title="Your profile"
          aria-label="Your profile"
        >
          {(user?.display_name || user?.email || '?')[0]?.toUpperCase()}
        </Link>
        <button
          type="button"
          className="sl-ws-rail-icon sl-ws-rail-icon-btn"
          title="Sign out"
          aria-label="Sign out"
          onClick={logout}
        >
          <SignOutIcon />
        </button>
      </div>
    </aside>
  );
}

// ---- inline SVG icons -------------------------------------------------
// Kept local so the rail is one self-contained file. 18px, currentColor,
// line weight 2 — matches the sign-out icon already present.

function PagesIcon(): ReactElement {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="8" y1="13" x2="16" y2="13" />
      <line x1="8" y1="17" x2="16" y2="17" />
      <line x1="8" y1="9" x2="10" y2="9" />
    </svg>
  );
}

function CalendarIcon(): ReactElement {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
      <line x1="16" y1="2" x2="16" y2="6" />
      <line x1="8" y1="2" x2="8" y2="6" />
      <line x1="3" y1="10" x2="21" y2="10" />
    </svg>
  );
}

function AdminIcon(): ReactElement {
  // People-ish gear glyph: two user silhouettes. Distinct from the
  // Pages document and the Calendar grid at a glance.
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  );
}

function PlusIcon(): ReactElement {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="5" y1="12" x2="19" y2="12" />
    </svg>
  );
}

function SignOutIcon(): ReactElement {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <polyline points="16 17 21 12 16 7" />
      <line x1="21" y1="12" x2="9" y2="12" />
    </svg>
  );
}
