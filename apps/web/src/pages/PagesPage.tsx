// Pages (M10). Lives at /w/:slug/pages[/:pageId?].
//
// Left rail lists workspace pages (newest first). Right pane opens the
// selected page in a TipTap editor wired to Y-Sweet for realtime sync.
// "+ New page" is a single-click action — we create with title "Untitled"
// and the user renames in-place.

import { useMemo, useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { archivePage, createPage, listPages, patchPage } from '../api/pages';
import type { Page } from '../api/pages';
import { listMyWorkspaces } from '../api/workspaces';
import { useAuth } from '../auth/AuthContext';
import { PageEditor } from '../components/PageEditor';
import { PageSidebar } from '../components/PageSidebar';
import { PageCommentsPanel } from '../components/PageCommentsPanel';
import { PageHistoryPanel } from '../components/PageHistoryPanel';
import { WorkspaceRail } from '../components/WorkspaceRail';

export function PagesPage(): ReactElement {
  const { user, loading: authLoading } = useAuth();
  const { slug = '', pageId } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const mshipQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: !!user,
    staleTime: 30_000,
  });
  const current = mshipQuery.data?.find((m) => m.workspace.slug === slug) ?? null;

  const pagesQuery = useQuery({
    queryKey: ['pages', slug],
    queryFn: () => listPages(slug),
    enabled: !!user && !!slug,
  });

  const selectedId = useMemo(() => {
    const raw = pageId ? Number(pageId) : null;
    if (raw !== null && !Number.isNaN(raw)) return raw;
    return pagesQuery.data?.[0]?.id ?? null;
  }, [pageId, pagesQuery.data]);

  const selectedPage: Page | null = useMemo(() => {
    if (selectedId === null) return null;
    return pagesQuery.data?.find((p) => p.id === selectedId) ?? null;
  }, [pagesQuery.data, selectedId]);

  const createMutation = useMutation({
    mutationFn: () => createPage(slug, { title: 'Untitled' }),
    onSuccess: (page) => {
      qc.invalidateQueries({ queryKey: ['pages', slug] });
      navigate(`/w/${slug}/pages/${page.id}`);
    },
  });

  const renameMutation = useMutation({
    mutationFn: (args: { id: number; title: string }) => patchPage(args.id, { title: args.title }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['pages', slug] }),
  });

  const archiveMutation = useMutation({
    mutationFn: (id: number) => archivePage(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['pages', slug] });
      if (selectedId) navigate(`/w/${slug}/pages`);
    },
  });

  if (authLoading) return <div style={{ padding: 24 }}>Loading…</div>;
  if (!user) return <Navigate to="/login" replace />;
  if (!mshipQuery.isLoading && !current) return <Navigate to="/" replace />;

  return (
    <div style={{ display: 'flex', height: '100vh' }}>
      <WorkspaceRail activeSlug={slug} />
      <div className="sl-pages-shell">
        <aside className="sl-pages-sidebar" aria-label="Pages">
          <PageSidebar
            pages={pagesQuery.data ?? []}
            selectedId={selectedId}
            onSelect={(id) => navigate(`/w/${slug}/pages/${id}`)}
            onCreate={() => createMutation.mutate()}
            onArchive={(id) => archiveMutation.mutate(id)}
            isCreating={createMutation.isPending}
          />
        </aside>

        <div className="sl-pages-content">
          {selectedPage ? (
            <PageSurface
              page={selectedPage}
              me={{ id: user.id, display_name: user.display_name || '' }}
              onRename={(title) => renameMutation.mutate({ id: selectedPage.id, title })}
            />
          ) : (
            <div className="sl-pages-empty">
              {pagesQuery.data?.length === 0
                ? 'No pages yet. Click "New page" in the left column to create the first one.'
                : 'Select a page on the left.'}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

type SurfaceProps = {
  page: Page;
  me: { id: number; display_name: string };
  onRename: (title: string) => void;
};

function PageSurface({ page, me, onRename }: SurfaceProps): ReactElement {
  const [title, setTitle] = useState(page.title);
  const [panel, setPanel] = useState<'none' | 'comments' | 'history'>('none');
  return (
    <>
      <div className="sl-pages-main">
        <div className="sl-pages-head">
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onBlur={() => {
              const t = title.trim();
              if (t && t !== page.title) onRename(t);
              else setTitle(page.title);
            }}
            className="sl-pages-title-input"
            aria-label="Page title"
          />
          <div className="sl-pages-tabs">
            <button
              type="button"
              onClick={() => setPanel(panel === 'comments' ? 'none' : 'comments')}
              aria-pressed={panel === 'comments'}
              className={`sl-pages-tab ${panel === 'comments' ? 'active' : ''}`}
            >
              Comments
            </button>
            <button
              type="button"
              onClick={() => setPanel(panel === 'history' ? 'none' : 'history')}
              aria-pressed={panel === 'history'}
              className={`sl-pages-tab ${panel === 'history' ? 'active' : ''}`}
            >
              History
            </button>
          </div>
        </div>
        <div className="sl-pages-meta">
          Last edited {new Date(page.updated_at).toLocaleString()}
        </div>
        <PageEditor page={page} me={me} />
      </div>
      {panel !== 'none' && (
        <aside className="sl-pages-aside" aria-label={panel === 'comments' ? 'Comments' : 'History'}>
          {panel === 'comments' && <PageCommentsPanel pageID={page.id} me={{ id: me.id }} />}
          {panel === 'history' && <PageHistoryPanel pageID={page.id} />}
        </aside>
      )}
    </>
  );
}
