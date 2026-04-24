import type { ReactElement } from 'react';
import type { Page } from '../api/pages';

type Props = {
  pages: readonly Page[];
  selectedId: number | null;
  onSelect: (id: number) => void;
  onCreate: () => void;
  onArchive: (id: number) => void;
  isCreating?: boolean;
};

export function PageSidebar({
  pages,
  selectedId,
  onSelect,
  onCreate,
  onArchive,
  isCreating,
}: Props): ReactElement {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: 12, borderBottom: '1px solid #eee' }}>
        <button
          type="button"
          onClick={onCreate}
          disabled={isCreating}
          style={{
            width: '100%',
            padding: '8px 12px',
            borderRadius: 4,
            border: '1px solid #ddd',
            background: isCreating ? '#eee' : '#fff',
            cursor: isCreating ? 'wait' : 'pointer',
            textAlign: 'left',
          }}
        >
          {isCreating ? 'Creating…' : '+ New page'}
        </button>
      </div>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, flex: 1, overflowY: 'auto' }}>
        {pages.map((p) => {
          const selected = p.id === selectedId;
          return (
            <li
              key={p.id}
              style={{
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid #f6f6f6',
                background: selected ? '#eef5ff' : 'transparent',
              }}
            >
              <button
                type="button"
                onClick={() => onSelect(p.id)}
                style={{
                  flex: 1,
                  padding: '10px 12px',
                  background: 'transparent',
                  border: 'none',
                  textAlign: 'left',
                  cursor: 'pointer',
                  fontWeight: selected ? 600 : 400,
                }}
              >
                {p.icon ? <span style={{ marginRight: 6 }}>{p.icon}</span> : null}
                {p.title || 'Untitled'}
              </button>
              <button
                type="button"
                onClick={() => {
                  if (window.confirm(`Archive "${p.title || 'Untitled'}"?`)) onArchive(p.id);
                }}
                title="Archive"
                style={{
                  padding: '0 10px',
                  background: 'transparent',
                  border: 'none',
                  color: '#aaa',
                  cursor: 'pointer',
                }}
              >
                ×
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
