// In-call surface (M8). Covers the main pane while a call is active.
//
// We lean on @livekit/components-react's prebuilt <VideoConference>
// widget for the bulk of the UX (grid + controls + mute/hangup). The
// one thing we add ourselves is a background-blur toggle — track-
// processors is a separate module that has to be plumbed to the
// LocalParticipant's video track.
//
// Lifecycle:
//   - Mount: fetch a join token, build a Room, connect.
//   - Leave: Room.disconnect() + tell the parent onLeave() which removes
//     the CallSurface from the tree.
//   - End (host only): call POST /meetings/:id/end, which closes the
//     LiveKit room server-side and posts a "Call ended" system message.
//     Everyone else gets auto-disconnected by the room shutdown.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import {
  ControlBar,
  GridLayout,
  LiveKitRoom,
  ParticipantTile,
  RoomAudioRenderer,
  useRoomContext,
  useTracks,
} from '@livekit/components-react';
import '@livekit/components-styles';
import { LocalVideoTrack, Room, Track } from 'livekit-client';
import { BackgroundBlur } from '@livekit/track-processors';

import { endMeeting, joinMeeting } from '../api/calls';

interface Props {
  meetingID: number;
  channelID: number; // surfaced for future per-channel call controls
  channelLabel: string;
  isHost: boolean;
  onLeave: () => void;
}

export function CallSurface({
  meetingID,
  channelID: _channelID,
  channelLabel,
  isHost,
  onLeave,
}: Props): ReactElement {
  const [token, setToken] = useState<string | null>(null);
  const [serverURL, setServerURL] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [ending, setEnding] = useState(false);

  const room = useMemo(
    () =>
      new Room({
        adaptiveStream: true,
        dynacast: true,
        videoCaptureDefaults: { resolution: { width: 1280, height: 720 } },
      }),
    [],
  );

  // Fetch the join token on mount. A single fetch is enough; the
  // LiveKit client keeps its own connection alive afterward.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await joinMeeting(meetingID);
        if (cancelled) return;
        setToken(resp.token);
        setServerURL(resp.ws_url);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Could not join call');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [meetingID]);

  // Ensure we disconnect cleanly on unmount so the browser releases mic + cam.
  useEffect(() => {
    return () => {
      room.disconnect().catch(() => {});
    };
  }, [room]);

  const leave = useCallback(async () => {
    await room.disconnect().catch(() => {});
    onLeave();
  }, [room, onLeave]);

  const end = useCallback(async () => {
    if (ending) return;
    setEnding(true);
    try {
      await endMeeting(meetingID);
    } catch (err) {
      // Non-fatal — room may still close from LiveKit side. Log and
      // surface so the host isn't left wondering.
      console.warn('end meeting failed', err);
    }
    await leave();
  }, [ending, meetingID, leave]);

  if (error) {
    return (
      <div className="sl-call-error">
        <h2>Couldn&apos;t join</h2>
        <p>{error}</p>
        <button type="button" className="sl-primary" onClick={onLeave}>Close</button>
      </div>
    );
  }

  if (!token || !serverURL) {
    return (
      <div className="sl-call-loading">
        <p className="sl-muted">Connecting to call…</p>
      </div>
    );
  }

  return (
    <div className="sl-call-surface" data-lk-theme="default">
      <header className="sl-call-header">
        <div className="sl-call-title">
          <span className="sl-call-dot" aria-hidden="true" />
          Call in {channelLabel}
        </div>
        <div className="sl-call-actions">
          {isHost && (
            <button
              type="button"
              className="sl-call-end"
              onClick={end}
              disabled={ending}
            >
              {ending ? 'Ending…' : 'End for everyone'}
            </button>
          )}
        </div>
      </header>

      <LiveKitRoom
        room={room}
        token={token}
        serverUrl={serverURL}
        connect={true}
        video={true}
        audio={true}
        onDisconnected={() => onLeave()}
      >
        <RoomAudioRenderer />
        <CallStage />
        <ControlBar
          controls={{
            camera: true,
            microphone: true,
            screenShare: true,
            chat: false,
            leave: true,
          }}
        />
        <BlurToggle />
      </LiveKitRoom>
    </div>
  );
}

// Renders the tile grid. Exported inline because it needs the LiveKit
// hooks context from <LiveKitRoom>.
function CallStage(): ReactElement {
  const tracks = useTracks(
    [
      { source: Track.Source.Camera, withPlaceholder: true },
      { source: Track.Source.ScreenShare, withPlaceholder: false },
    ],
    { onlySubscribed: false },
  );
  return (
    <div className="sl-call-stage">
      <GridLayout tracks={tracks} style={{ height: '100%' }}>
        <ParticipantTile />
      </GridLayout>
    </div>
  );
}

// Background blur toggle. BackgroundBlur from track-processors is a
// MediaPipe-backed pipeline applied to the LocalVideoTrack on the fly.
// Toggling on mutates the processor (no republish); toggling off stops
// it. Requires cross-origin isolation headers (set in vite.config.ts).
function BlurToggle(): ReactElement {
  const room = useRoomContext();
  const [blurOn, setBlurOn] = useState(false);
  const [busy, setBusy] = useState(false);
  const processorRef = useRef<ReturnType<typeof BackgroundBlur> | null>(null);

  const toggle = useCallback(async () => {
    if (busy) return;
    const publication = room.localParticipant.getTrackPublication(Track.Source.Camera);
    const track = publication?.track;
    if (!track || !(track instanceof LocalVideoTrack)) return;
    setBusy(true);
    try {
      if (!blurOn) {
        if (!processorRef.current) processorRef.current = BackgroundBlur();
        await track.setProcessor(processorRef.current);
        setBlurOn(true);
      } else {
        await track.stopProcessor();
        setBlurOn(false);
      }
    } catch (err) {
      // Usually means cross-origin isolation isn't active. Log a clear
      // hint so a stumble on production finds its way back here.
      console.warn('blur toggle failed (COOP/COEP headers?):', err);
    } finally {
      setBusy(false);
    }
  }, [blurOn, busy, room]);

  return (
    <button
      type="button"
      className={`sl-blur-toggle ${blurOn ? 'active' : ''}`}
      onClick={toggle}
      aria-pressed={blurOn}
      disabled={busy}
    >
      {busy ? '…' : blurOn ? 'Blur on' : 'Blur off'}
    </button>
  );
}
