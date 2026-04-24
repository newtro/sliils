import type { ReactElement } from 'react';
import type { Page } from '../api/pages';
import { t } from '../i18n/messages';

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
    <>
      <div className="sl-pages-sidebar-head">
        <button
          type="button"
          onClick={onCreate}
          disabled={isCreating}
          className="sl-pages-new-btn"
        >
          {isCreating ? t('page.creating') : t('page.new')}
        </button>
      </div>
      <ul className="sl-pages-list">
        {pages.map((p) => {
          const selected = p.id === selectedId;
          return (
            <li key={p.id} className={`sl-pages-item ${selected ? 'active' : ''}`}>
              <button
                type="button"
                onClick={() => onSelect(p.id)}
                className="sl-pages-item-btn"
              >
                {p.icon ? <span style={{ marginRight: 6 }}>{p.icon}</span> : null}
                {p.title || t('page.untitled')}
              </button>
              <button
                type="button"
                onClick={() => {
                  const title = p.title || t('page.untitled');
                  if (window.confirm(t('page.archive.confirm', { title }))) onArchive(p.id);
                }}
                aria-label={`${t('page.archive')} ${p.title || t('page.untitled')}`}
                title={t('page.archive')}
                className="sl-pages-item-archive"
              >
                <span aria-hidden="true">×</span>
              </button>
            </li>
          );
        })}
      </ul>
    </>
  );
}
