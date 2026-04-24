// Cmd+K / Ctrl+K overlay. Mounted from WorkspacePage because search is
// workspace-scoped; opening it outside a workspace is a no-op.
//
// Flow:
//   1. User types. Every keystroke kicks a 150ms debounce.
//   2. After the debounce settles, TanStack Query fires POST /search.
//   3. Results render as a keyboard-navigable list.
//   4. Enter or click jumps to the channel + message. The parent handles
//      the actual navigation via onNavigate so WorkspacePage's channel
//      selection + future scroll-to-message hook can live in one place.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { KeyboardEvent, ReactElement } from 'react';
import { useQuery } from '@tanstack/react-query';

import { search } from '../api/search';
import type { SearchHit } from '../api/search';

export interface SearchOverlayProps {
  workspaceID: number;
  open: boolean;
  onClose: () => void;
  onNavigate: (args: { channelID: number; messageID: number }) => void;
}

export function SearchOverlay({
  workspaceID,
  open,
  onClose,
  onNavigate,
}: SearchOverlayProps): ReactElement | null {
  const [rawQuery, setRawQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [cursor, setCursor] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  // Autofocus whenever the overlay opens.
  useEffect(() => {
    if (!open) return;
    // Defer one tick so the input is mounted.
    const id = window.requestAnimationFrame(() => inputRef.current?.focus());
    return () => window.cancelAnimationFrame(id);
  }, [open]);

  // Reset state whenever we close.
  useEffect(() => {
    if (open) return;
    setRawQuery('');
    setDebounced('');
    setCursor(0);
  }, [open]);

  // 150ms debounce before we hit the server.
  useEffect(() => {
    if (!open) return;
    const id = window.setTimeout(() => setDebounced(rawQuery.trim()), 150);
    return () => window.clearTimeout(id);
  }, [rawQuery, open]);

  const queryEnabled = open && debounced.length > 0;
  const q = useQuery({
    queryKey: ['search', workspaceID, debounced],
    queryFn: () => search({ workspaceID, query: debounced, limit: 20 }),
    enabled: queryEnabled,
    staleTime: 30_000,
  });

  const hits: SearchHit[] = useMemo(() => q.data?.hits ?? [], [q.data]);

  // Clamp cursor whenever hits change so it never points past the list.
  useEffect(() => {
    if (cursor >= hits.length) setCursor(Math.max(0, hits.length - 1));
  }, [hits.length, cursor]);

  const activate = useCallback(
    (hit: SearchHit) => {
      onNavigate({ channelID: hit.channel_id, messageID: hit.message_id });
      onClose();
    },
    [onNavigate, onClose],
  );

  const onInputKey = useCallback(
    (ev: KeyboardEvent<HTMLInputElement>) => {
      if (ev.key === 'Escape') {
        ev.preventDefault();
        onClose();
        return;
      }
      if (hits.length === 0) return;
      if (ev.key === 'ArrowDown') {
        ev.preventDefault();
        setCursor((c) => Math.min(c + 1, hits.length - 1));
      } else if (ev.key === 'ArrowUp') {
        ev.preventDefault();
        setCursor((c) => Math.max(c - 1, 0));
      } else if (ev.key === 'Enter') {
        ev.preventDefault();
        const hit = hits[cursor];
        if (hit) activate(hit);
      }
    },
    [hits, cursor, onClose, activate],
  );

  if (!open) return null;

  const showEmpty = queryEnabled && !q.isLoading && hits.length === 0;
  const showHint = !queryEnabled;

  return (
    <div
      className="sl-search-overlay"
      role="dialog"
      aria-modal="true"
      aria-label="Search"
      onMouseDown={(e) => {
        // Click outside the panel closes the overlay. The inner panel
        // stops propagation so inner clicks don't.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        className="sl-search-panel"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="sl-search-input-row">
          <span className="sl-search-icon" aria-hidden="true">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="7" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
          </span>
          <input
            ref={inputRef}
            value={rawQuery}
            onChange={(e) => setRawQuery(e.target.value)}
            onKeyDown={onInputKey}
            placeholder="Search messages — try from:@alice has:link in:#general"
            className="sl-search-input"
            spellCheck={false}
            aria-controls="sl-search-results"
          />
          <kbd className="sl-search-esc">esc</kbd>
        </div>

        <ul className="sl-search-results" id="sl-search-results" role="listbox">
          {showHint && (
            <li className="sl-search-hint">
              Type to search. Operators: <code>from:@user</code>, <code>in:#channel</code>,{' '}
              <code>has:link</code>, <code>has:file</code>, <code>mentions:@user</code>.
            </li>
          )}
          {q.isLoading && !q.data && (
            <li className="sl-search-hint">Searching…</li>
          )}
          {q.isError && (
            <li className="sl-search-error">Search failed. Try again.</li>
          )}
          {showEmpty && (
            <li className="sl-search-hint">No matches for “{debounced}”.</li>
          )}
          {hits.map((hit, i) => (
            <li key={hit.message_id}>
              <button
                type="button"
                className={`sl-search-hit ${i === cursor ? 'active' : ''}`}
                onMouseEnter={() => setCursor(i)}
                onClick={() => activate(hit)}
              >
                <div className="sl-search-hit-meta">
                  <span className="sl-search-hit-channel">
                    #{hit.channel_name ?? 'dm'}
                  </span>
                  {hit.author_display_name && (
                    <span className="sl-search-hit-author">{hit.author_display_name}</span>
                  )}
                  <span className="sl-search-hit-time">{formatAgo(hit.created_at)}</span>
                </div>
                <div
                  className="sl-search-hit-snippet"
                  // Meilisearch's highlight tags are fixed to <mark>...</mark>
                  // so we render them as HTML. We control the tag choice at
                  // the server call site (see backend SearchRequest), and
                  // Meili escapes all other markup, so this is safe.
                  dangerouslySetInnerHTML={{
                    __html: hit.snippet || escapeHTML(truncate(hit.body_md, 200)),
                  }}
                />
              </button>
            </li>
          ))}
        </ul>

        {q.data && hits.length > 0 && (
          <div className="sl-search-footer">
            {hits.length} of {q.data.estimated_total_hits} · {q.data.processing_time_ms} ms
            <span className="sl-search-footer-keys">
              <kbd>↑</kbd>
              <kbd>↓</kbd> navigate <kbd>↵</kbd> open
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

// formatAgo is a cheap relative-time formatter good enough for search
// results. Avoids pulling in a dateFns dependency for one use.
function formatAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const s = Math.max(1, Math.floor(ms / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}

function escapeHTML(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max - 1) + '…';
}
