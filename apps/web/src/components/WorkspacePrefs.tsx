// Small popover mounted next to the workspace header's user row. Lets the
// current member:
//   - set/clear their custom_status (emoji + optional text) for THIS workspace
//   - pick the workspace's default notification pref (all | mentions | mute)
//
// M7 scope: functional and accessible, not fancy. No emoji picker yet —
// one text input accepts any emoji you can paste or type. That keeps the
// component small and sidesteps the picker-library dependency churn.

import { useEffect, useRef, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import {
  clearMyStatus,
  setMyNotifyPref,
  setMyStatus,
} from '../api/workspaces';
import type { CustomStatus, NotifyPref } from '../api/workspaces';

interface Props {
  workspaceSlug: string;
  currentStatus?: CustomStatus;
  currentNotifyPref: NotifyPref;
  onClose: () => void;
}

export function WorkspacePrefs({
  workspaceSlug,
  currentStatus,
  currentNotifyPref,
  onClose,
}: Props): ReactElement {
  const qc = useQueryClient();
  const panelRef = useRef<globalThis.HTMLDivElement>(null);

  const [emoji, setEmoji] = useState(currentStatus?.emoji ?? '');
  const [text, setText] = useState(currentStatus?.text ?? '');
  const [pref, setPref] = useState<NotifyPref>(currentNotifyPref);

  // Clicking outside or pressing Escape dismisses the popover.
  useEffect(() => {
    function onDocClick(e: globalThis.MouseEvent) {
      if (!panelRef.current) return;
      if (panelRef.current.contains(e.target as Node)) return;
      onClose();
    }
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  const invalidateMe = () =>
    qc.invalidateQueries({ queryKey: ['my-workspaces'] });

  const saveStatus = useMutation({
    mutationFn: async (body: { emoji: string; text: string }) => {
      if (!body.emoji && !body.text) {
        return clearMyStatus(workspaceSlug);
      }
      return setMyStatus(workspaceSlug, {
        emoji: body.emoji || undefined,
        text: body.text || undefined,
      });
    },
    onSuccess: () => {
      invalidateMe();
    },
  });

  const savePref = useMutation({
    mutationFn: (next: NotifyPref) => setMyNotifyPref(workspaceSlug, next),
    onSuccess: () => {
      invalidateMe();
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    saveStatus.mutate({ emoji: emoji.trim(), text: text.trim() });
  }

  function onChangePref(next: NotifyPref) {
    setPref(next);
    savePref.mutate(next);
  }

  return (
    <div className="sl-prefs-popover" ref={panelRef} role="dialog" aria-label="Workspace preferences">
      <form className="sl-prefs-section" onSubmit={onSubmit}>
        <div className="sl-prefs-section-label">Status in this workspace</div>
        <div className="sl-prefs-status-row">
          <input
            type="text"
            className="sl-prefs-emoji"
            maxLength={8}
            value={emoji}
            placeholder=":)"
            onChange={(e) => setEmoji(e.target.value)}
            aria-label="Status emoji"
            title="Paste or type an emoji"
          />
          <input
            type="text"
            className="sl-prefs-text"
            maxLength={140}
            value={text}
            placeholder="What's going on?"
            onChange={(e) => setText(e.target.value)}
            aria-label="Status text"
          />
        </div>
        <div className="sl-prefs-actions">
          {(currentStatus?.emoji || currentStatus?.text) && (
            <button
              type="button"
              className="sl-linkbtn"
              onClick={() => {
                setEmoji('');
                setText('');
                saveStatus.mutate({ emoji: '', text: '' });
              }}
            >
              Clear
            </button>
          )}
          <button type="submit" className="sl-primary sl-primary-sm" disabled={saveStatus.isPending}>
            {saveStatus.isPending ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      <div className="sl-prefs-section">
        <div className="sl-prefs-section-label">Notifications</div>
        <div className="sl-prefs-pref-group" role="radiogroup" aria-label="Notification preference">
          {(['all', 'mentions', 'mute'] as NotifyPref[]).map((p) => (
            <label key={p} className={`sl-prefs-pref ${pref === p ? 'active' : ''}`}>
              <input
                type="radio"
                name="notify-pref"
                value={p}
                checked={pref === p}
                onChange={() => onChangePref(p)}
              />
              <span>{prefLabel(p)}</span>
            </label>
          ))}
        </div>
        <p className="sl-muted sl-prefs-hint">
          Channel-level overrides still apply on top of this default.
        </p>
      </div>
    </div>
  );
}

function prefLabel(p: NotifyPref): string {
  switch (p) {
    case 'all':
      return 'All messages';
    case 'mentions':
      return 'Mentions only';
    case 'mute':
      return 'Nothing';
  }
}
