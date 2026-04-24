import type { ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  createPageSnapshot,
  listPageSnapshots,
  restorePageSnapshot,
} from '../api/pages';

type Props = {
  pageID: number;
};

export function PageHistoryPanel({ pageID }: Props): ReactElement {
  const qc = useQueryClient();

  const snapshotsQuery = useQuery({
    queryKey: ['page-snapshots', pageID],
    queryFn: () => listPageSnapshots(pageID),
  });

  const snapshotMutation = useMutation({
    mutationFn: () => createPageSnapshot(pageID),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['page-snapshots', pageID] }),
  });

  const restoreMutation = useMutation({
    mutationFn: (snapshotID: number) => restorePageSnapshot(pageID, snapshotID),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['page-snapshots', pageID] }),
  });

  const snapshots = snapshotsQuery.data ?? [];

  return (
    <>
      <div className="sl-pages-aside-head">
        <span>History</span>
        <button
          type="button"
          className="sl-pages-history-btn"
          onClick={() => snapshotMutation.mutate()}
          disabled={snapshotMutation.isPending}
        >
          {snapshotMutation.isPending ? 'Saving…' : 'Snapshot'}
        </button>
      </div>
      <div className="sl-pages-aside-body">
        {snapshots.length === 0 && (
          <div style={{ padding: 16 }} className="sl-muted">
            No snapshots yet. The server captures one every few minutes when there&rsquo;s activity;
            click Snapshot to make one now.
          </div>
        )}
        {snapshots.map((s) => (
          <div key={s.id} className="sl-pages-history-row">
            <div>
              <div className="sl-pages-history-when">
                {new Date(s.created_at).toLocaleString()}
              </div>
              <div className="sl-pages-history-sub">
                {s.reason} · {s.byte_size.toLocaleString()} B
                {s.creator_display_name ? ` · ${s.creator_display_name}` : ''}
              </div>
            </div>
            <button
              type="button"
              className="sl-pages-history-btn"
              onClick={() => {
                if (
                  window.confirm(
                    'Restore this snapshot? Current unsaved edits will be overwritten.',
                  )
                ) {
                  restoreMutation.mutate(s.id);
                }
              }}
            >
              Restore
            </button>
          </div>
        ))}
      </div>
    </>
  );
}
