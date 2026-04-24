// Schedule-event modal (M9). Three logical sections:
//   1. When + how long
//   2. What — title + description
//   3. Who — attendee picker (workspace members) + external emails
//
// RRULE is exposed as a friendly dropdown (none / daily / weekly / monthly)
// that maps to canonical strings. Power users can flip an "Advanced" toggle
// and hand-edit the RRULE if they want something BYDAY-exotic.

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useMutation } from '@tanstack/react-query';

import { ApiError } from '../api/client';
import { createEvent } from '../api/events';
import type { WorkspaceMember } from '../api/workspaces';

interface Props {
  workspaceSlug: string;
  members: readonly WorkspaceMember[];
  currentUserID: number;
  initialStart?: Date;
  initialEnd?: Date;
  onClose: () => void;
  onCreated: () => void;
}

type Preset = 'none' | 'daily' | 'weekly' | 'monthly' | 'custom';

const presetToRRule: Record<Exclude<Preset, 'none' | 'custom'>, string> = {
  daily: 'FREQ=DAILY',
  weekly: 'FREQ=WEEKLY',
  monthly: 'FREQ=MONTHLY',
};

export function ScheduleEventDialog({
  workspaceSlug,
  members,
  currentUserID,
  initialStart,
  initialEnd,
  onClose,
  onCreated,
}: Props): ReactElement {
  const now = useMemo(() => initialStart ?? nextRoundedHour(new Date()), [initialStart]);
  const end = useMemo(() => initialEnd ?? new Date(now.getTime() + 30 * 60 * 1000), [initialEnd, now]);

  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [startAt, setStartAt] = useState(toDateTimeLocal(now));
  const [endAt, setEndAt] = useState(toDateTimeLocal(end));
  const [preset, setPreset] = useState<Preset>('none');
  const [customRRule, setCustomRRule] = useState('');
  const [videoEnabled, setVideoEnabled] = useState(true);
  const [attendeeIDs, setAttendeeIDs] = useState<number[]>([]);
  const [externalEmails, setExternalEmails] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const handler = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose]);

  const create = useMutation({
    mutationFn: () => {
      const startIso = new Date(startAt).toISOString();
      const endIso = new Date(endAt).toISOString();
      let rrule: string | undefined;
      if (preset === 'custom') rrule = customRRule.trim() || undefined;
      else if (preset !== 'none') rrule = presetToRRule[preset];
      return createEvent(workspaceSlug, {
        title: title.trim(),
        description: description.trim(),
        start_at: startIso,
        end_at: endIso,
        time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        rrule,
        video_enabled: videoEnabled,
        attendee_user_ids: attendeeIDs,
        external_emails: externalEmails
          .split(/[\s,;]+/)
          .map((s) => s.trim())
          .filter(Boolean),
      });
    },
    onSuccess: () => onCreated(),
    onError: (err) => {
      setError(
        err instanceof ApiError ? err.problem.detail ?? err.message : 'Could not create event',
      );
    },
  });

  const toggleAttendee = useCallback((userID: number) => {
    setAttendeeIDs((prev) =>
      prev.includes(userID) ? prev.filter((id) => id !== userID) : [...prev, userID],
    );
  }, []);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!title.trim()) {
      setError('Title is required');
      return;
    }
    if (new Date(endAt) <= new Date(startAt)) {
      setError('End must be after start');
      return;
    }
    create.mutate();
  }

  return (
    <div
      className="sl-modal-backdrop"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <form
        className="sl-modal"
        onMouseDown={(e) => e.stopPropagation()}
        onSubmit={onSubmit}
      >
        <header className="sl-modal-header">
          <h2>New event</h2>
          <button type="button" className="sl-icon-btn" onClick={onClose} aria-label="Close">×</button>
        </header>
        <div className="sl-modal-body">
          <label className="sl-field">
            <span>Title</span>
            <input
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Weekly standup"
              autoFocus
              required
            />
          </label>

          <div style={{ display: 'flex', gap: 8 }}>
            <label className="sl-field" style={{ flex: 1 }}>
              <span>Starts</span>
              <input
                type="datetime-local"
                value={startAt}
                onChange={(e) => setStartAt(e.target.value)}
                required
              />
            </label>
            <label className="sl-field" style={{ flex: 1 }}>
              <span>Ends</span>
              <input
                type="datetime-local"
                value={endAt}
                onChange={(e) => setEndAt(e.target.value)}
                required
              />
            </label>
          </div>

          <label className="sl-field">
            <span>Repeats</span>
            <select value={preset} onChange={(e) => setPreset(e.target.value as Preset)}>
              <option value="none">Doesn&apos;t repeat</option>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
              <option value="custom">Custom (RRULE)</option>
            </select>
          </label>
          {preset === 'custom' && (
            <label className="sl-field">
              <span>RRULE</span>
              <input
                type="text"
                value={customRRule}
                onChange={(e) => setCustomRRule(e.target.value)}
                placeholder="FREQ=WEEKLY;BYDAY=MO,WE,FR"
              />
            </label>
          )}

          <label className="sl-field">
            <span>Description</span>
            <textarea
              className="sl-composer-textarea"
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Agenda, links, context…"
              style={{ minHeight: 70 }}
            />
          </label>

          <label className="sl-field sl-field-row">
            <input
              type="checkbox"
              checked={videoEnabled}
              onChange={(e) => setVideoEnabled(e.target.checked)}
            />
            <span>Add a video call</span>
          </label>

          <div className="sl-field">
            <span>Invite workspace members</span>
            <ul className="sl-ws-dm-picker" style={{ maxHeight: 160 }}>
              {members
                .filter((m) => m.user_id !== currentUserID)
                .map((m) => {
                  const picked = attendeeIDs.includes(m.user_id);
                  return (
                    <li key={m.user_id}>
                      <button
                        type="button"
                        className={`sl-ws-dm-picker-row ${picked ? 'active' : ''}`}
                        onClick={() => toggleAttendee(m.user_id)}
                        style={picked ? { background: 'var(--accent-soft)' } : undefined}
                      >
                        <span>{m.display_name || m.email.split('@')[0]}</span>
                        {picked && <span style={{ marginLeft: 'auto', fontSize: 12 }}>✓</span>}
                      </button>
                    </li>
                  );
                })}
              {members.filter((m) => m.user_id !== currentUserID).length === 0 && (
                <li className="sl-muted" style={{ padding: '4px 8px', fontSize: 13 }}>
                  No other workspace members yet.
                </li>
              )}
            </ul>
          </div>

          <label className="sl-field">
            <span>External email invites (comma-separated)</span>
            <input
              type="text"
              value={externalEmails}
              onChange={(e) => setExternalEmails(e.target.value)}
              placeholder="external@example.com"
            />
          </label>

          {error && <div role="alert" className="sl-error">{error}</div>}

          <div className="sl-modal-actions">
            <button type="button" className="sl-linkbtn" onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className="sl-primary" disabled={create.isPending}>
              {create.isPending ? 'Creating…' : 'Create event'}
            </button>
          </div>
        </div>
      </form>
    </div>
  );
}

function toDateTimeLocal(d: Date): string {
  // <input type="datetime-local"> wants "YYYY-MM-DDTHH:MM" in local time.
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function nextRoundedHour(d: Date): Date {
  const next = new Date(d);
  next.setHours(d.getHours() + 1, 0, 0, 0);
  return next;
}
