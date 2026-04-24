// Calendar page (M9). Mounts under /w/:slug/calendar.
//
// Uses react-big-calendar for the grid — week is the default view because
// that's the "see your week at a glance" UX every demo keeps landing on.
// Day view is one toolbar click away.
//
// Event data: GET /workspaces/:slug/events returns RRULE-expanded
// occurrences; we map each occurrence to one visual block. Clicking a
// block opens a details popover with RSVP + Join-call. Clicking "+ New
// event" opens the schedule modal.

import { useCallback, useMemo, useState } from 'react';
import type { ReactElement } from 'react';
import { Navigate, useNavigate, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Calendar, dateFnsLocalizer } from 'react-big-calendar';
import type { SlotInfo, View } from 'react-big-calendar';
import 'react-big-calendar/lib/css/react-big-calendar.css';
import { enUS } from 'date-fns/locale/en-US';
import { format, getDay, parse, startOfWeek } from 'date-fns';

import { ApiError } from '../api/client';
import {
  cancelEvent,
  joinEventMeeting,
  listEvents,
  rsvpEvent,
} from '../api/events';
import type { EventOccurrence, RSVP } from '../api/events';
import { listMyWorkspaces, listWorkspaceMembers } from '../api/workspaces';
import type { WorkspaceMember } from '../api/workspaces';
import { useAuth } from '../auth/AuthContext';
import { ScheduleEventDialog } from '../components/ScheduleEventDialog';

const locales = { 'en-US': enUS };
const localizer = dateFnsLocalizer({
  format,
  parse,
  startOfWeek: (date: Date) => startOfWeek(date, { weekStartsOn: 1 }),
  getDay,
  locales,
});

type BigCalendarEvent = {
  title: string;
  start: Date;
  end: Date;
  allDay?: boolean;
  resource: EventOccurrence;
};

export function CalendarPage(): ReactElement {
  const { user, loading: authLoading } = useAuth();
  const { slug = '' } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [view, setView] = useState<View>('week');
  const [currentDate, setCurrentDate] = useState<Date>(new Date());
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [schedulePrefill, setSchedulePrefill] = useState<{ start?: Date; end?: Date }>({});
  const [selected, setSelected] = useState<EventOccurrence | null>(null);

  // Memberships drive the workspace header. Reuse the same query key the
  // WorkspacePage uses so we share cache.
  const mshipQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: !!user,
    staleTime: 30_000,
  });
  const current = mshipQuery.data?.find((m) => m.workspace.slug === slug) ?? null;

  const membersQuery = useQuery({
    queryKey: ['workspace', slug, 'members'],
    queryFn: () => listWorkspaceMembers(slug),
    enabled: !!user && !!slug,
  });
  const members: readonly WorkspaceMember[] = membersQuery.data ?? [];

  // Range is derived from the visible view — react-big-calendar emits an
  // onRangeChange, but the default week view always covers the week of
  // `currentDate`. Pad by a day on each side so a Sunday event that
  // spans into Monday still renders.
  const range = useMemo(() => {
    const from = new Date(currentDate);
    from.setDate(from.getDate() - 10);
    const to = new Date(currentDate);
    to.setDate(to.getDate() + 42); // +6 weeks
    return { from: from.toISOString(), to: to.toISOString() };
  }, [currentDate]);

  const eventsQuery = useQuery({
    queryKey: ['workspace', slug, 'events', range.from, range.to],
    queryFn: () => listEvents(slug, range),
    enabled: !!user && !!slug,
    staleTime: 30_000,
  });

  const calEvents = useMemo<BigCalendarEvent[]>(() => {
    const occs = eventsQuery.data ?? [];
    return occs.map((o) => ({
      title: o.title,
      start: new Date(o.instance_start),
      end: new Date(o.instance_end),
      resource: o,
    }));
  }, [eventsQuery.data]);

  const rsvpMutation = useMutation({
    mutationFn: ({ id, rsvp }: { id: number; rsvp: RSVP }) => rsvpEvent(id, rsvp),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workspace', slug, 'events'] });
    },
  });

  const cancelMutation = useMutation({
    mutationFn: (id: number) => cancelEvent(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workspace', slug, 'events'] });
      setSelected(null);
    },
  });

  const onSlotSelect = useCallback((info: SlotInfo) => {
    // Drag-select on the calendar grid pre-fills the schedule modal.
    setSchedulePrefill({ start: new Date(info.start), end: new Date(info.end) });
    setScheduleOpen(true);
  }, []);

  const onEventSelect = useCallback((event: BigCalendarEvent) => {
    setSelected(event.resource);
  }, []);

  const joinCall = useCallback(async (occ: EventOccurrence) => {
    try {
      const resp = await joinEventMeeting(occ.series_id);
      // Full calendar → workspace-pane handoff is a v1 polish item. For
      // now, surface the room id so a curious dev can verify JWT flow.
      window.alert(`Call ready: ${resp.livekit_room} (open ${slug} workspace to join)`);
    } catch (err) {
      window.alert(err instanceof ApiError ? err.problem.detail ?? err.message : 'Join failed');
    }
  }, [slug]);

  if (authLoading || mshipQuery.isLoading) {
    return <div className="sl-placeholder">Loading…</div>;
  }
  if (!user) return <Navigate to="/login" replace />;
  if (!current) return <Navigate to="/" replace />;

  return (
    <div className="sl-calendar-shell">
      <header className="sl-calendar-header">
        <button
          type="button"
          className="sl-linkbtn"
          onClick={() => navigate(`/w/${slug}`)}
        >
          ← {current.workspace.name}
        </button>
        <h1 className="sl-calendar-title">Calendar</h1>
        <button
          type="button"
          className="sl-primary"
          onClick={() => {
            setSchedulePrefill({});
            setScheduleOpen(true);
          }}
        >
          + New event
        </button>
      </header>

      <div className="sl-calendar-grid">
        <Calendar
          localizer={localizer}
          events={calEvents}
          defaultView="week"
          view={view}
          views={['day', 'week', 'month']}
          onView={setView}
          date={currentDate}
          onNavigate={setCurrentDate}
          startAccessor="start"
          endAccessor="end"
          selectable
          onSelectSlot={onSlotSelect}
          onSelectEvent={onEventSelect}
          style={{ height: 'calc(100vh - 120px)' }}
          eventPropGetter={(event: BigCalendarEvent) => ({
            style: {
              backgroundColor: event.resource.video_enabled
                ? 'var(--accent)'
                : 'var(--brand-teal)',
              borderRadius: '4px',
              border: 0,
              color: '#fff',
              padding: '2px 6px',
              fontSize: '12px',
            },
          })}
        />
      </div>

      {selected && (
        <EventDetailsPopover
          occurrence={selected}
          canEdit={selected.created_by === user.id}
          onClose={() => setSelected(null)}
          onRSVP={(rsvp) =>
            rsvpMutation.mutate(
              { id: selected.series_id, rsvp },
              { onSuccess: () => setSelected({ ...selected, my_rsvp: rsvp }) },
            )
          }
          onCancel={() => cancelMutation.mutate(selected.series_id)}
          onJoin={() => joinCall(selected)}
        />
      )}

      {scheduleOpen && (
        <ScheduleEventDialog
          workspaceSlug={slug}
          members={members}
          currentUserID={user.id}
          initialStart={schedulePrefill.start}
          initialEnd={schedulePrefill.end}
          onClose={() => setScheduleOpen(false)}
          onCreated={() => {
            qc.invalidateQueries({ queryKey: ['workspace', slug, 'events'] });
            setScheduleOpen(false);
          }}
        />
      )}
    </div>
  );
}

// ---- event details popover ---------------------------------------------

interface PopoverProps {
  occurrence: EventOccurrence;
  canEdit: boolean;
  onClose: () => void;
  onRSVP: (rsvp: RSVP) => void;
  onCancel: () => void;
  onJoin: () => void;
}

function EventDetailsPopover({
  occurrence,
  canEdit,
  onClose,
  onRSVP,
  onCancel,
  onJoin,
}: PopoverProps): ReactElement {
  const start = new Date(occurrence.instance_start);
  const end = new Date(occurrence.instance_end);
  return (
    <div
      className="sl-modal-backdrop"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="sl-modal" onMouseDown={(e) => e.stopPropagation()}>
        <header className="sl-modal-header">
          <h2>{occurrence.title}</h2>
          <button type="button" className="sl-icon-btn" onClick={onClose} aria-label="Close">×</button>
        </header>
        <div className="sl-modal-body">
          <div className="sl-muted">
            {start.toLocaleString()} — {end.toLocaleTimeString()}
          </div>
          {occurrence.rrule && (
            <div className="sl-muted" style={{ fontSize: 12 }}>
              Recurring: <code>{occurrence.rrule}</code>
            </div>
          )}
          {occurrence.description && (
            <p style={{ whiteSpace: 'pre-wrap' }}>{occurrence.description}</p>
          )}
          {occurrence.attendees && occurrence.attendees.length > 0 && (
            <div>
              <div className="sl-prefs-section-label">Attendees</div>
              <ul style={{ listStyle: 'none', padding: 0, margin: '4px 0 0' }}>
                {occurrence.attendees.map((a, i) => (
                  <li key={i} style={{ fontSize: 13, padding: '2px 0' }}>
                    {a.display_name || a.external_email || `user ${a.user_id}`}{' '}
                    <span className="sl-muted">· {a.rsvp}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          <div className="sl-modal-actions" style={{ flexWrap: 'wrap' }}>
            {(['yes', 'maybe', 'no'] as RSVP[]).map((r) => (
              <button
                key={r}
                type="button"
                className={`sl-linkbtn ${occurrence.my_rsvp === r ? 'active' : ''}`}
                onClick={() => onRSVP(r)}
              >
                {labelForRSVP(r)}
              </button>
            ))}
            {occurrence.video_enabled && occurrence.channel_id && (
              <button type="button" className="sl-primary" onClick={onJoin}>
                Join call
              </button>
            )}
            {canEdit && (
              <button
                type="button"
                className="sl-linkbtn sl-danger-link"
                onClick={onCancel}
              >
                Cancel event
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function labelForRSVP(r: RSVP): string {
  switch (r) {
    case 'yes': return 'Going';
    case 'maybe': return 'Maybe';
    case 'no': return 'Can’t go';
    default: return r;
  }
}
