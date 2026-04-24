import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, KeyboardEvent, ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import type { Channel, WorkspaceMember } from '../api/workspaces';
import { createMessage, getThread } from '../api/messages';
import type { Message, ThreadResponse } from '../api/messages';
import { useAuth } from '../auth/AuthContext';
import { useRealtime } from '../realtime/useRealtime';
import type { RealtimeEvent } from '../realtime/useRealtime';
import { MessageBody } from './MessageBody';
import { MessageAttachments } from './MessageAttachments';

interface Props {
  workspaceID: number;
  channel: Channel;
  rootID: number;
  members: readonly WorkspaceMember[];
  onClose: () => void;
}

/**
 * Slide-in thread panel. Owns its own TanStack Query for the thread
 * payload + subscribes to the same channel topic so new replies stream in.
 * Composer targets the thread root via parent_id.
 */
export function ThreadPanel({
  workspaceID,
  channel,
  rootID,
  members,
  onClose,
}: Props): ReactElement {
  const qc = useQueryClient();
  const { user } = useAuth();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [body, setBody] = useState('');

  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = 'auto';
    const max = 200;
    el.style.height = Math.min(el.scrollHeight, max) + 'px';
    el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden';
  }, [body]);

  function onComposerKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      (e.currentTarget.form ?? null)?.requestSubmit();
    }
  }

  const queryKey = useMemo(() => ['thread', rootID] as const, [rootID]);
  const threadQuery = useQuery({
    queryKey,
    queryFn: () => getThread(rootID),
  });

  const topic = useMemo(() => `ws:${workspaceID}:ch:${channel.id}`, [workspaceID, channel.id]);
  const onEvent = useCallback(
    (ev: RealtimeEvent) => {
      if (ev.type !== 'message.created') return;
      const msg = ev.data as Message;
      if (msg.thread_root_id !== rootID) return;
      qc.setQueryData<ThreadResponse>(queryKey, (prev) => {
        if (!prev) return prev;
        if (prev.replies.some((r) => r.id === msg.id)) return prev;
        return {
          root: { ...prev.root, reply_count: prev.root.reply_count + 1 },
          replies: [...prev.replies.filter((r) => !(r.id < 0 && r.body_md === msg.body_md)), msg],
        };
      });
    },
    [queryKey, qc, rootID],
  );
  useRealtime([topic], onEvent);

  const sendMutation = useMutation({
    mutationFn: (text: string) => createMessage(channel.id, text, { parentID: rootID }),
    onSuccess: (created) => {
      qc.setQueryData<ThreadResponse>(queryKey, (prev) => {
        if (!prev) return prev;
        if (prev.replies.some((r) => r.id === created.id)) return prev;
        return {
          root: { ...prev.root, reply_count: prev.root.reply_count + 1 },
          replies: [
            ...prev.replies.filter((r) => !(r.id < 0 && r.body_md === created.body_md)),
            created,
          ],
        };
      });
      // Also bump the main channel list's reply_count for the root.
      qc.setQueryData<Message[]>(['channel', channel.id, 'messages'], (prev = []) =>
        prev.map((m) => (m.id === rootID ? { ...m, reply_count: m.reply_count + 1 } : m)),
      );
    },
  });

  useEffect(() => {
    inputRef.current?.focus();
  }, [rootID]);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const text = body.trim();
    if (!text || sendMutation.isPending) return;
    setBody('');

    const optimistic: Message = {
      id: -Date.now(),
      channel_id: channel.id,
      workspace_id: workspaceID,
      author_user_id: user?.id,
      body_md: text,
      body_blocks: [],
      reactions: [],
      reply_count: 0,
      attachments: [],
      thread_root_id: rootID,
      parent_id: rootID,
      created_at: new Date().toISOString(),
    };
    qc.setQueryData<ThreadResponse>(queryKey, (prev) => {
      if (!prev) return prev;
      return { root: prev.root, replies: [...prev.replies, optimistic] };
    });
    sendMutation.mutate(text);
  }

  const data = threadQuery.data;

  return (
    <aside className="sl-thread-panel" aria-label="Thread">
      <header className="sl-thread-header">
        <div>
          <div className="sl-thread-title">Thread</div>
          <div className="sl-thread-subtitle">
            #{channel.name ?? 'channel'}
            {data && (
              <>
                {' · '}
                {data.root.reply_count} {data.root.reply_count === 1 ? 'reply' : 'replies'}
              </>
            )}
          </div>
        </div>
        <button type="button" className="sl-thread-close" onClick={onClose} aria-label="Close thread">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </header>

      <div className="sl-thread-body">
        {threadQuery.isLoading && <div className="sl-muted" style={{ padding: '16px 20px' }}>Loading…</div>}
        {data && (
          <>
            <ThreadMessage message={data.root} members={members} currentUserID={user?.id} />
            {data.replies.length > 0 && (
              <div className="sl-thread-divider">
                <span>
                  {data.replies.length} {data.replies.length === 1 ? 'reply' : 'replies'}
                </span>
              </div>
            )}
            {data.replies.map((r) => (
              <ThreadMessage key={r.id} message={r} members={members} currentUserID={user?.id} />
            ))}
          </>
        )}
      </div>

      <form className="sl-chview-composer" onSubmit={onSubmit}>
        <div className="sl-composer-wrap">
          <textarea
            ref={inputRef}
            className="sl-composer-textarea"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            onKeyDown={onComposerKey}
            placeholder="Reply in thread… (Enter to send, Shift+Enter for newline)"
            disabled={sendMutation.isPending}
            aria-label="Thread reply"
            rows={1}
          />
        </div>
        <button
          type="submit"
          className="sl-primary"
          disabled={!body.trim() || sendMutation.isPending}
        >
          Reply
        </button>
      </form>
    </aside>
  );
}

interface ThreadMessageProps {
  message: Message;
  members: readonly WorkspaceMember[];
  currentUserID?: number;
}

function ThreadMessage({ message: m, members, currentUserID }: ThreadMessageProps): ReactElement {
  const author = members.find((x) => x.user_id === m.author_user_id);
  const name = author?.display_name || author?.email.split('@')[0] || `User ${m.author_user_id ?? '—'}`;
  return (
    <div className="sl-msg" style={{ padding: '8px 20px' }}>
      <div className="sl-msg-avatar-wrap">
        <div
          className="sl-msg-avatar"
          aria-hidden="true"
          style={{ background: threadAvatarGradient(m.author_user_id) }}
        >
          {name.charAt(0).toUpperCase()}
        </div>
      </div>
      <div className="sl-msg-body">
        <div className="sl-msg-meta">
          <span className="sl-msg-author">{name}</span>
          <span className="sl-msg-time">{formatTime(m.created_at)}</span>
          {m.edited_at && <span className="sl-msg-edited">(edited)</span>}
        </div>
        <div className="sl-msg-text">
          <MessageBody body={m.body_md} members={members} currentUserID={currentUserID} />
        </div>
        <MessageAttachments attachments={m.attachments ?? []} />
      </div>
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

const THREAD_GRADIENTS = [
  'linear-gradient(135deg, #3B7DD8, #27A8AC)',
  'linear-gradient(135deg, #5BB85C, #27A8AC)',
  'linear-gradient(135deg, #1A2D43, #3B7DD8)',
  'linear-gradient(135deg, #7B4DD0, #3B7DD8)',
  'linear-gradient(135deg, #E67E42, #D94D6A)',
  'linear-gradient(135deg, #27A8AC, #5BB85C)',
];

function threadAvatarGradient(userID?: number): string {
  if (!userID) return THREAD_GRADIENTS[0]!;
  return THREAD_GRADIENTS[userID % THREAD_GRADIENTS.length]!;
}
