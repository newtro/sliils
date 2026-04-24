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
    <div style={{ padding: 12, display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 8,
        }}
      >
        <div style={{ fontWeight: 600 }}>History</div>
        <button
          type="button"
          onClick={() => snapshotMutation.mutate()}
          disabled={snapshotMutation.isPending}
          style={btn}
        >
          {snapshotMutation.isPending ? 'Saving…' : 'Snapshot'}
        </button>
      </div>
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {snapshots.length === 0 && (
          <div style={{ color: '#888', fontSize: 13 }}>
            No snapshots yet. The server creates one every few minutes when there&rsquo;s activity; click Snapshot to make one now.
          </div>
        )}
        {snapshots.map((s) => (
          <div
            key={s.id}
            style={{
              padding: 8,
              borderBottom: '1px solid #eee',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <div>
              <div style={{ fontSize: 13 }}>{new Date(s.created_at).toLocaleString()}</div>
              <div style={{ fontSize: 11, color: '#888' }}>
                {s.reason} · {s.byte_size.toLocaleString()} B
                {s.creator_display_name ? ` · ${s.creator_display_name}` : ''}
              </div>
            </div>
            <button
              type="button"
              onClick={() => {
                if (window.confirm('Restore this snapshot? Current unsaved edits will be overwritten.')) {
                  restoreMutation.mutate(s.id);
                }
              }}
              style={btn}
            >
              Restore
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}

const btn: React.CSSProperties = {
  padding: '4px 10px',
  border: '1px solid #ddd',
  background: '#fff',
  borderRadius: 4,
  fontSize: 12,
  cursor: 'pointer',
};
