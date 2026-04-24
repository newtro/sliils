import { apiFetch } from './client';

// Events API (M9).
//
// The server returns RRULE-expanded occurrences (not series) from GET so
// the client can paint a week/day grid without re-implementing RRULE in
// the browser. series_id is the canonical row — patch/cancel/rsvp take
// series_id, not a per-occurrence id.

export type RSVP = 'yes' | 'no' | 'maybe' | 'pending';

export interface EventAttendee {
  user_id?: number;
  display_name?: string;
  email?: string;
  external_email?: string;
  rsvp: RSVP;
}

export interface EventOccurrence {
  series_id: number;
  instance_start: string;
  instance_end: string;
  title: string;
  description: string;
  location_url?: string;
  channel_id?: number;
  time_zone: string;
  rrule?: string;
  recording_enabled: boolean;
  video_enabled: boolean;
  created_by?: number;
  my_rsvp?: RSVP;
  attendees?: EventAttendee[];
}

export interface Event {
  id: number;
  workspace_id: number;
  channel_id?: number;
  title: string;
  description: string;
  location_url?: string;
  start_at: string;
  end_at: string;
  time_zone: string;
  rrule?: string;
  recording_enabled: boolean;
  video_enabled: boolean;
  created_by?: number;
  created_at: string;
  updated_at: string;
  canceled_at?: string;
  attendees?: EventAttendee[];
  my_rsvp?: RSVP;
}

export interface CreateEventInput {
  channel_id?: number;
  title: string;
  description?: string;
  location_url?: string;
  start_at: string;        // ISO 8601
  end_at: string;
  time_zone?: string;
  rrule?: string;
  recording_enabled?: boolean;
  video_enabled?: boolean;
  attendee_user_ids?: number[];
  external_emails?: string[];
}

export function listEvents(
  slug: string,
  opts: { from?: string; to?: string } = {},
): Promise<EventOccurrence[]> {
  const qs = new URLSearchParams();
  if (opts.from) qs.set('from', opts.from);
  if (opts.to) qs.set('to', opts.to);
  const suffix = qs.toString();
  const path = `/workspaces/${encodeURIComponent(slug)}/events${suffix ? `?${suffix}` : ''}`;
  return apiFetch<EventOccurrence[]>(path);
}

export function createEvent(slug: string, input: CreateEventInput): Promise<Event> {
  return apiFetch<Event>(`/workspaces/${encodeURIComponent(slug)}/events`, {
    method: 'POST',
    body: input,
  });
}

export function rsvpEvent(eventID: number, rsvp: RSVP): Promise<void> {
  return apiFetch<void>(`/events/${eventID}/rsvp`, {
    method: 'POST',
    body: { rsvp },
  });
}

export function cancelEvent(eventID: number): Promise<void> {
  return apiFetch<void>(`/events/${eventID}`, { method: 'DELETE' });
}

export interface EventJoinToken {
  token: string;
  ws_url: string;
  livekit_room: string;
  identity: string;
  expires_at: string;
}

export function joinEventMeeting(eventID: number): Promise<EventJoinToken> {
  return apiFetch<EventJoinToken>(`/events/${eventID}/join`, { method: 'POST' });
}
