import { apiFetch } from './client';

// Call API (M8).
//
//   POST /channels/:id/meetings   — find-or-create the active meeting
//   POST /meetings/:id/join       — get a LiveKit JWT + WS URL
//   POST /meetings/:id/end        — close the room + post "Call ended" msg
//
// The join token is sensitive and short-lived — treat it like a password.
// We never put it in localStorage; it lives in React state for the duration
// of the call UI's mount.

export interface Meeting {
  id: number;
  channel_id: number;
  workspace_id: number;
  livekit_room: string;
  started_by?: number;
  started_at: string;
  ended_at?: string;
  participant_count: number;
}

export interface JoinToken {
  token: string;
  ws_url: string;
  livekit_room: string;
  identity: string;
  expires_at: string;
}

export function startOrGetMeeting(channelID: number): Promise<Meeting> {
  return apiFetch<Meeting>(`/channels/${channelID}/meetings`, { method: 'POST' });
}

export function joinMeeting(meetingID: number): Promise<JoinToken> {
  return apiFetch<JoinToken>(`/meetings/${meetingID}/join`, { method: 'POST' });
}

export function endMeeting(meetingID: number): Promise<Meeting> {
  return apiFetch<Meeting>(`/meetings/${meetingID}/end`, { method: 'POST' });
}
