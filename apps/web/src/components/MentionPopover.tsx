import { useEffect, useMemo, useState } from 'react';
import type { KeyboardEvent, ReactElement } from 'react';
import type { WorkspaceMember } from '../api/workspaces';

interface Props {
  members: readonly WorkspaceMember[];
  query: string;
  visible: boolean;
  onSelect: (member: WorkspaceMember) => void;
  onDismiss: () => void;
}

/**
 * Small filterable list that appears above the composer when the user
 * types `@`. Keyboard arrows move highlight; Enter selects; Escape closes.
 * The parent composer owns the query string (everything after the `@`
 * trigger) and calls onSelect / onDismiss as the user navigates.
 */
export function MentionPopover({
  members,
  query,
  visible,
  onSelect,
  onDismiss,
}: Props): ReactElement | null {
  const [highlight, setHighlight] = useState(0);

  const filtered = useMemo(() => {
    const q = query.toLowerCase();
    if (!q) return members.slice(0, 8);
    return members
      .filter((m) => {
        const name = (m.display_name || m.email.split('@')[0] || '').toLowerCase();
        return name.includes(q) || m.email.toLowerCase().includes(q);
      })
      .slice(0, 8);
  }, [members, query]);

  // Reset highlight when list changes so the visible item stays first.
  useEffect(() => {
    setHighlight(0);
  }, [query, filtered.length]);

  // Key handling is delegated from the parent composer via a ref-captured
  // fn below so we keep tab order inside the textarea.
  (MentionPopover as unknown as { lastKeyHandler?: (e: KeyboardEvent) => boolean }).lastKeyHandler = (
    e: KeyboardEvent,
  ) => {
    if (!visible) return false;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setHighlight((h) => (filtered.length === 0 ? 0 : (h + 1) % filtered.length));
      return true;
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlight((h) => (filtered.length === 0 ? 0 : (h - 1 + filtered.length) % filtered.length));
      return true;
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      if (filtered.length > 0) {
        e.preventDefault();
        onSelect(filtered[highlight]!);
        return true;
      }
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      onDismiss();
      return true;
    }
    return false;
  };

  if (!visible) return null;
  if (filtered.length === 0) {
    return (
      <div className="sl-mention-popover" role="listbox">
        <div className="sl-mention-empty">No matches</div>
      </div>
    );
  }

  return (
    <div className="sl-mention-popover" role="listbox" aria-label="Mention workspace member">
      {filtered.map((m, i) => (
        <button
          key={m.user_id}
          type="button"
          role="option"
          aria-selected={i === highlight}
          className={`sl-mention-option ${i === highlight ? 'active' : ''}`}
          onMouseEnter={() => setHighlight(i)}
          onMouseDown={(e) => {
            // mouseDown instead of click so the textarea doesn't lose
            // focus+selection before we insert.
            e.preventDefault();
            onSelect(m);
          }}
        >
          <span className="sl-mention-option-name">{m.display_name || m.email.split('@')[0]}</span>
          <span className="sl-mention-option-email">{m.email}</span>
        </button>
      ))}
    </div>
  );
}

// Helper for the composer to route keys into the popover.
export function handleMentionKey(e: KeyboardEvent): boolean {
  const fn = (MentionPopover as unknown as { lastKeyHandler?: (e: KeyboardEvent) => boolean })
    .lastKeyHandler;
  return fn ? fn(e) : false;
}
