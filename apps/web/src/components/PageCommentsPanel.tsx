import { useState } from 'react';
import type { ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  createPageComment,
  deletePageComment,
  listPageComments,
  patchPageComment,
} from '../api/pages';
import type { PageComment } from '../api/pages';

type Props = {
  pageID: number;
  me: { id: number };
};

export function PageCommentsPanel({ pageID, me }: Props): ReactElement {
  const qc = useQueryClient();
  const [draft, setDraft] = useState('');

  const commentsQuery = useQuery({
    queryKey: ['page-comments', pageID],
    queryFn: () => listPageComments(pageID),
  });

  const createMutation = useMutation({
    mutationFn: (body_md: string) => createPageComment(pageID, { body_md }),
    onSuccess: () => {
      setDraft('');
      qc.invalidateQueries({ queryKey: ['page-comments', pageID] });
    },
  });

  const resolveMutation = useMutation({
    mutationFn: (args: { id: number; resolved: boolean }) =>
      patchPageComment(args.id, { resolved: args.resolved }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['page-comments', pageID] }),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deletePageComment(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['page-comments', pageID] }),
  });

  const comments: PageComment[] = commentsQuery.data ?? [];

  return (
    <>
      <div className="sl-pages-aside-head">
        <span>Comments</span>
        <span className="sl-muted" style={{ fontSize: 12 }}>
          {comments.length}
        </span>
      </div>
      <div className="sl-pages-aside-body">
        {comments.length === 0 && (
          <div style={{ padding: 16 }} className="sl-muted">
            No comments yet.
          </div>
        )}
        {comments.map((c) => (
          <div
            key={c.id}
            className={`sl-pages-comment ${c.resolved_at ? 'resolved' : ''}`}
          >
            <div className="sl-pages-comment-meta">
              {c.author_display_name || `User ${c.author_id ?? ''}`} ·{' '}
              {new Date(c.created_at).toLocaleString()}
              {c.resolved_at ? ' · resolved' : ''}
            </div>
            <div className="sl-pages-comment-body">{c.body_md}</div>
            <div className="sl-pages-comment-actions">
              <button
                type="button"
                onClick={() =>
                  resolveMutation.mutate({ id: c.id, resolved: !c.resolved_at })
                }
              >
                {c.resolved_at ? 'Reopen' : 'Resolve'}
              </button>
              {c.author_id === me.id && (
                <button type="button" onClick={() => deleteMutation.mutate(c.id)}>
                  Delete
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
      <form
        className="sl-pages-aside-foot"
        onSubmit={(e) => {
          e.preventDefault();
          const trimmed = draft.trim();
          if (trimmed) createMutation.mutate(trimmed);
        }}
      >
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Leave a comment…"
          rows={3}
          className="sl-pages-comment-textarea"
          aria-label="Comment body"
        />
        <button
          type="submit"
          className="sl-primary sl-primary-sm"
          style={{ marginTop: 6 }}
          disabled={!draft.trim() || createMutation.isPending}
        >
          {createMutation.isPending ? 'Posting…' : 'Post'}
        </button>
      </form>
    </>
  );
}
