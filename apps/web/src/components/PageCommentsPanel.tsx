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
    <div style={{ padding: 12, display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ fontWeight: 600, marginBottom: 8 }}>Comments</div>
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {comments.length === 0 && (
          <div style={{ color: '#888', fontSize: 13 }}>No comments yet.</div>
        )}
        {comments.map((c) => (
          <div
            key={c.id}
            style={{
              padding: 8,
              borderBottom: '1px solid #eee',
              opacity: c.resolved_at ? 0.6 : 1,
            }}
          >
            <div style={{ fontSize: 12, color: '#666' }}>
              {c.author_display_name || `User ${c.author_id ?? ''}`} ·{' '}
              {new Date(c.created_at).toLocaleString()}
              {c.resolved_at ? ' · resolved' : ''}
            </div>
            <div style={{ whiteSpace: 'pre-wrap', marginTop: 4 }}>{c.body_md}</div>
            <div style={{ marginTop: 4, display: 'flex', gap: 8 }}>
              <button
                type="button"
                onClick={() =>
                  resolveMutation.mutate({ id: c.id, resolved: !c.resolved_at })
                }
                style={btnStyle}
              >
                {c.resolved_at ? 'Reopen' : 'Resolve'}
              </button>
              {c.author_id === me.id && (
                <button
                  type="button"
                  onClick={() => deleteMutation.mutate(c.id)}
                  style={btnStyle}
                >
                  Delete
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const trimmed = draft.trim();
          if (trimmed) createMutation.mutate(trimmed);
        }}
        style={{ marginTop: 8, borderTop: '1px solid #eee', paddingTop: 8 }}
      >
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Leave a comment…"
          rows={3}
          style={{ width: '100%', boxSizing: 'border-box', fontFamily: 'inherit' }}
        />
        <button
          type="submit"
          disabled={!draft.trim() || createMutation.isPending}
          style={{ marginTop: 4, ...btnStyle, padding: '6px 12px' }}
        >
          {createMutation.isPending ? 'Posting…' : 'Post'}
        </button>
      </form>
    </div>
  );
}

const btnStyle: React.CSSProperties = {
  padding: '2px 8px',
  border: '1px solid #ddd',
  background: '#fff',
  borderRadius: 4,
  fontSize: 12,
  cursor: 'pointer',
};
