// Lightweight ring banner shown when a `meeting.started` event arrives on
// the workspace topic and it's for a channel the current user can reach.
// Two actions: Answer (enters the call) and Dismiss (close banner but
// leave the meeting running for others).
//
// This is deliberately a banner and not a modal. A modal would steal
// focus and interrupt typing; a banner lets the user keep doing what
// they're doing until they actually want to answer.

import type { ReactElement } from 'react';

export interface IncomingCall {
  meetingID: number;
  channelID: number;
  channelLabel: string; // "DM with @bob" or "#design"
  startedBy: number;
  startedByName?: string;
}

interface Props {
  call: IncomingCall;
  onAnswer: () => void;
  onDismiss: () => void;
}

export function IncomingCallBanner({ call, onAnswer, onDismiss }: Props): ReactElement {
  return (
    <div className="sl-ring-banner" role="alertdialog" aria-label="Incoming call">
      <div className="sl-ring-icon" aria-hidden="true">📞</div>
      <div className="sl-ring-text">
        <div className="sl-ring-title">Incoming call</div>
        <div className="sl-ring-sub">
          {call.startedByName ? <strong>{call.startedByName}</strong> : 'Someone'} is calling in{' '}
          <strong>{call.channelLabel}</strong>
        </div>
      </div>
      <div className="sl-ring-actions">
        <button type="button" className="sl-linkbtn" onClick={onDismiss}>
          Dismiss
        </button>
        <button type="button" className="sl-primary" onClick={onAnswer}>
          Answer
        </button>
      </div>
    </div>
  );
}
