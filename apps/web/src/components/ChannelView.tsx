import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { FormEvent, KeyboardEvent, ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Virtuoso } from 'react-virtuoso';
import type { VirtuosoHandle } from 'react-virtuoso';

import type { Channel, WorkspaceMember } from '../api/workspaces';
import {
  addReaction,
  createMessage,
  deleteMessage,
  editMessage,
  listMessages,
  markChannelRead,
  removeReaction,
} from '../api/messages';
import type { Message } from '../api/messages';
import { uploadFile } from '../api/files';
import type { FileDTO } from '../api/files';
import { useAuth } from '../auth/AuthContext';
import {
  sendTypingHeartbeat,
  sendTypingStopped,
  useRealtime,
  usePresence,
  useTypingUsers,
} from '../realtime/useRealtime';
import type { RealtimeEvent } from '../realtime/useRealtime';
import { MessageBody } from './MessageBody';
import { MessageAttachments } from './MessageAttachments';
import { MentionPopover, handleMentionKey } from './MentionPopover';

interface Props {
  workspaceID: number;
  channel: Channel;
  members: readonly WorkspaceMember[];
  onOpenThread: (rootID: number) => void;
  /** When set, scroll the list to that message once it's in the loaded
   *  window. Used by the search overlay's "click result → jump" flow. */
  scrollToMessageID?: number | null;
  /** Called after scrollToMessageID has been applied so the parent can
   *  clear its pending state. */
  onScrolledToMessage?: () => void;
}

export function ChannelView({
  workspaceID,
  channel,
  members,
  onOpenThread,
  scrollToMessageID,
  onScrolledToMessage,
}: Props): ReactElement {
  const qc = useQueryClient();
  const { user } = useAuth();
  const listRef = useRef<VirtuosoHandle>(null);
  const [body, setBody] = useState('');
  const [editingID, setEditingID] = useState<number | null>(null);
  const [editingBody, setEditingBody] = useState('');

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const [pendingFiles, setPendingFiles] = useState<FileDTO[]>([]);
  const [uploadingCount, setUploadingCount] = useState(0);
  const [isDragging, setIsDragging] = useState(false);

  const queryKey = useMemo(() => ['channel', channel.id, 'messages'] as const, [channel.id]);
  const presentUserIDs = usePresence(workspaceID);
  const typingUserIDs = useTypingUsers(channel.id);

  const messagesQuery = useQuery({
    queryKey,
    queryFn: async () => {
      const page = await listMessages(channel.id);
      return page.messages.slice().reverse();
    },
  });

  // ---- realtime fan-in ---------------------------------------------------

  const topic = useMemo(() => `ws:${workspaceID}:ch:${channel.id}`, [workspaceID, channel.id]);
  const onEvent = useCallback(
    (ev: RealtimeEvent) => {
      if (ev.type === 'must_resync') {
        qc.invalidateQueries({ queryKey });
        return;
      }
      if (ev.type === 'message.created') {
        const msg = ev.data as Message;
        // Thread replies don't belong in the main channel feed; update the
        // parent's reply_count and poke the thread query if mounted.
        if (msg.thread_root_id) {
          qc.setQueryData<Message[]>(queryKey, (prev = []) =>
            prev.map((m) =>
              m.id === msg.thread_root_id
                ? { ...m, reply_count: m.reply_count + 1 }
                : m,
            ),
          );
          qc.invalidateQueries({ queryKey: ['thread', msg.thread_root_id] });
          return;
        }
        qc.setQueryData<Message[]>(queryKey, (prev = []) => mergeCreatedMessage(prev, msg));
        return;
      }
      if (ev.type === 'message.updated') {
        const msg = ev.data as Message;
        qc.setQueryData<Message[]>(queryKey, (prev = []) =>
          prev.map((m) => (m.id === msg.id ? { ...msg, reply_count: m.reply_count } : m)),
        );
        if (msg.thread_root_id) {
          qc.invalidateQueries({ queryKey: ['thread', msg.thread_root_id] });
        }
        return;
      }
      if (ev.type === 'message.deleted') {
        const msg = ev.data as Message;
        qc.setQueryData<Message[]>(queryKey, (prev = []) => prev.filter((m) => m.id !== msg.id));
        if (msg.thread_root_id) {
          qc.invalidateQueries({ queryKey: ['thread', msg.thread_root_id] });
        }
        return;
      }
      if (ev.type === 'reaction.added' || ev.type === 'reaction.removed') {
        const payload = ev.data as {
          message_id: number;
          user_id: number;
          emoji: string;
        };
        qc.setQueryData<Message[]>(queryKey, (prev = []) =>
          prev.map((m) => {
            if (m.id !== payload.message_id) return m;
            return applyReactionDelta(m, payload.emoji, payload.user_id, ev.type === 'reaction.added');
          }),
        );
      }
    },
    [queryKey, qc],
  );

  useRealtime([topic], onEvent);

  const messages = messagesQuery.data ?? [];
  const lastID = messages.length > 0 ? messages[messages.length - 1]!.id : null;

  useEffect(() => {
    if (!lastID) return;
    // If the caller asked us to jump to a specific message, let that win
    // over the "stick to the bottom on new messages" behavior until it's
    // applied (see next effect).
    if (scrollToMessageID) return;
    listRef.current?.scrollToIndex({ index: messages.length - 1, behavior: 'smooth' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastID, scrollToMessageID]);

  // Scroll-to-message handler for search-result navigation. Finds the
  // target id in the loaded window and scrollToIndex's to it; for messages
  // outside the window we'd need a dedicated API (future M6.1), so we fall
  // back to bottom for now.
  const [highlightedID, setHighlightedID] = useState<number | null>(null);
  useEffect(() => {
    if (!scrollToMessageID) return;
    if (messages.length === 0) return;
    const idx = messages.findIndex((m) => m.id === scrollToMessageID);
    if (idx < 0) {
      // Not yet paged in — future: fetch a page that contains this id.
      // For M6 we just land them at the channel bottom.
      listRef.current?.scrollToIndex({ index: messages.length - 1, behavior: 'smooth' });
    } else {
      listRef.current?.scrollToIndex({ index: idx, behavior: 'smooth' });
      setHighlightedID(scrollToMessageID);
      const id = window.setTimeout(() => setHighlightedID(null), 1500);
      return () => window.clearTimeout(id);
    }
    onScrolledToMessage?.();
    return undefined;
  }, [scrollToMessageID, messages, onScrolledToMessage]);

  // Auto mark-read whenever we have an up-to-date latest id and it's
  // greater than the channel's last_read.
  useEffect(() => {
    if (!lastID) return;
    if (channel.last_read_message_id && channel.last_read_message_id >= lastID) return;
    markChannelRead(channel.id, lastID).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastID, channel.id]);

  // ---- mutations ---------------------------------------------------------

  const sendMutation = useMutation({
    mutationFn: ({ text, attachmentIDs }: { text: string; attachmentIDs: number[] }) =>
      createMessage(channel.id, text, { attachmentIDs }),
    onSuccess: (created) => {
      qc.setQueryData<Message[]>(queryKey, (prev = []) => mergeCreatedMessage(prev, created));
    },
  });

  const editMutation = useMutation({
    mutationFn: ({ id, text }: { id: number; text: string }) => editMessage(id, text),
    onSuccess: (updated) => {
      qc.setQueryData<Message[]>(queryKey, (prev = []) =>
        prev.map((m) => (m.id === updated.id ? { ...updated, reply_count: m.reply_count } : m)),
      );
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteMessage(id),
  });

  // ---- typing broadcast --------------------------------------------------

  const lastHeartbeatRef = useRef(0);
  const heartbeatIfNeeded = useCallback(() => {
    const now = Date.now();
    if (now - lastHeartbeatRef.current < 3_000) return;
    lastHeartbeatRef.current = now;
    sendTypingHeartbeat(workspaceID, channel.id);
  }, [workspaceID, channel.id]);

  // ---- handlers ----------------------------------------------------------

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const text = body.trim();
    const hasContent = text || pendingFiles.length > 0;
    if (!hasContent || sendMutation.isPending || uploadingCount > 0) return;
    setBody('');
    setMentionQuery(null);
    const attachments = pendingFiles;
    setPendingFiles([]);
    sendTypingStopped(workspaceID, channel.id);
    lastHeartbeatRef.current = 0;

    const optimistic: Message = {
      id: -Date.now(),
      channel_id: channel.id,
      workspace_id: workspaceID,
      author_user_id: user?.id,
      body_md: text,
      body_blocks: [],
      reactions: [],
      reply_count: 0,
      attachments,
      created_at: new Date().toISOString(),
    };
    qc.setQueryData<Message[]>(queryKey, (prev = []) => [...prev, optimistic]);
    sendMutation.mutate({ text, attachmentIDs: attachments.map((a) => a.id) });
  }

  async function addFiles(files: FileList | File[]) {
    const list = Array.from(files);
    if (list.length === 0) return;
    setUploadingCount((c) => c + list.length);
    for (const f of list) {
      try {
        const dto = await uploadFile(workspaceID, f);
        setPendingFiles((prev) =>
          prev.some((p) => p.id === dto.id) ? prev : [...prev, dto],
        );
      } catch (err) {
        console.warn('upload failed', err);
      } finally {
        setUploadingCount((c) => Math.max(0, c - 1));
      }
    }
  }

  function removePending(id: number) {
    setPendingFiles((prev) => prev.filter((f) => f.id !== id));
  }

  function onComposerChange(value: string) {
    setBody(value);
    heartbeatIfNeeded();
    const match = /(?:^|\s)@(\w*)$/.exec(value);
    setMentionQuery(match ? (match[1] ?? '') : null);
  }

  function onComposerKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    // Mention popover consumes arrow keys + Enter/Tab when active.
    if (mentionQuery !== null) {
      if (handleMentionKey(e)) return;
    }
    // Enter submits, Shift+Enter inserts a newline. Matches Slack/Teams.
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      // Close over the containing form so submit-button disabled logic still applies.
      (e.currentTarget.form ?? null)?.requestSubmit();
    }
  }

  // Auto-grow the composer textarea: reset to auto to measure, then clamp
  // to a max so the pane doesn't eat the message list on huge pastes. Runs
  // before paint (useLayoutEffect) so the user never sees the jump.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = 'auto';
    const max = 240; // ~12 lines at 14px
    el.style.height = Math.min(el.scrollHeight, max) + 'px';
    el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden';
  }, [body]);

  function selectMention(member: WorkspaceMember) {
    const replaced = body.replace(/(?:^|\s)@(\w*)$/, (full) => {
      const lead = full.startsWith(' ') ? ' ' : '';
      return `${lead}<@${member.user_id}> `;
    });
    setBody(replaced);
    setMentionQuery(null);
    inputRef.current?.focus();
  }

  function startEdit(m: Message) {
    setEditingID(m.id);
    setEditingBody(m.body_md);
  }

  async function saveEdit() {
    if (editingID === null) return;
    const trimmed = editingBody.trim();
    if (!trimmed) {
      setEditingID(null);
      return;
    }
    editMutation.mutate({ id: editingID, text: trimmed });
    setEditingID(null);
  }

  async function onRowKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Escape') {
      setEditingID(null);
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      await saveEdit();
    }
  }

  function toggleReaction(msg: Message, emoji: string) {
    const mine = msg.reactions.find((r) => r.emoji === emoji)?.user_ids.includes(user?.id ?? -1);
    if (mine) {
      qc.setQueryData<Message[]>(queryKey, (prev = []) =>
        prev.map((m) =>
          m.id === msg.id ? applyReactionDelta(m, emoji, user?.id ?? -1, false) : m,
        ),
      );
      removeReaction(msg.id, emoji).catch(() => qc.invalidateQueries({ queryKey }));
    } else {
      qc.setQueryData<Message[]>(queryKey, (prev = []) =>
        prev.map((m) =>
          m.id === msg.id ? applyReactionDelta(m, emoji, user?.id ?? -1, true) : m,
        ),
      );
      addReaction(msg.id, emoji).catch(() => qc.invalidateQueries({ queryKey }));
    }
  }

  // ---- render ------------------------------------------------------------

  const typingNames = Array.from(typingUserIDs)
    .filter((id) => id !== user?.id)
    .map((id) => {
      const m = members.find((x) => x.user_id === id);
      return m?.display_name || m?.email.split('@')[0] || `user ${id}`;
    });

  return (
    <div className="sl-chview">
      <div className="sl-chview-messages">
        {messages.length === 0 && !messagesQuery.isLoading ? (
          <div className="sl-chview-empty">
            <p>No messages yet. Say hello!</p>
          </div>
        ) : (
          <Virtuoso
            ref={listRef}
            data={messages}
            followOutput="auto"
            initialTopMostItemIndex={Math.max(messages.length - 1, 0)}
            itemContent={(_, m) => (
              <div className={highlightedID === m.id ? 'sl-msg-row-highlight' : undefined}>
                <MessageRow
                  key={m.id}
                  message={m}
                  isMine={user?.id === m.author_user_id}
                  editing={editingID === m.id}
                  editingBody={editingBody}
                  members={members}
                  currentUserID={user?.id}
                  onlineUserIDs={presentUserIDs}
                  onStartEdit={() => startEdit(m)}
                  onChangeEdit={setEditingBody}
                  onSubmitEdit={saveEdit}
                  onCancelEdit={() => setEditingID(null)}
                  onEditKey={onRowKey}
                  onDelete={() => deleteMutation.mutate(m.id)}
                  onReact={(emoji) => toggleReaction(m, emoji)}
                  onOpenThread={() => onOpenThread(m.id)}
                />
              </div>
            )}
          />
        )}
      </div>

      {typingNames.length > 0 && (
        <div className="sl-typing-bar" aria-live="polite">
          <span className="sl-typing-dots" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span>{formatTypingNames(typingNames)}</span>
        </div>
      )}

      <form
        className={`sl-chview-composer-region ${isDragging ? 'dragging' : ''}`}
        onSubmit={onSubmit}
        onDragEnter={(e) => {
          e.preventDefault();
          if (e.dataTransfer.types.includes('Files')) setIsDragging(true);
        }}
        onDragOver={(e) => e.preventDefault()}
        onDragLeave={(e) => {
          if (e.currentTarget.contains(e.relatedTarget as Node)) return;
          setIsDragging(false);
        }}
        onDrop={(e) => {
          e.preventDefault();
          setIsDragging(false);
          if (e.dataTransfer.files.length > 0) addFiles(e.dataTransfer.files);
        }}
      >
        {(pendingFiles.length > 0 || uploadingCount > 0) && (
          <div className="sl-pending-attachments">
            {pendingFiles.map((f) => (
              <div key={f.id} className="sl-pending-attach">
                <span className="sl-pending-attach-icon" aria-hidden="true">
                  {f.mime.startsWith('image/') ? '🖼' : '📎'}
                </span>
                <span className="sl-pending-attach-name" title={f.filename}>
                  {f.filename}
                </span>
                <button
                  type="button"
                  className="sl-pending-attach-remove"
                  aria-label={`Remove ${f.filename}`}
                  onClick={() => removePending(f.id)}
                >
                  ×
                </button>
              </div>
            ))}
            {uploadingCount > 0 && (
              <div className="sl-pending-attach sl-pending-attach-loading">
                <span className="sl-typing-dots" aria-hidden="true">
                  <span />
                  <span />
                  <span />
                </span>
                <span>Uploading {uploadingCount}…</span>
              </div>
            )}
          </div>
        )}

        <div className="sl-chview-composer">
          <button
            type="button"
            className="sl-composer-attach"
            onClick={() => fileInputRef.current?.click()}
            title="Attach a file"
            aria-label="Attach a file"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
            </svg>
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            style={{ display: 'none' }}
            onChange={(e) => {
              if (e.target.files) addFiles(e.target.files);
              e.target.value = '';
            }}
          />

          <div className="sl-composer-wrap">
            <MentionPopover
              members={members}
              query={mentionQuery ?? ''}
              visible={mentionQuery !== null}
              onSelect={selectMention}
              onDismiss={() => setMentionQuery(null)}
            />
            <textarea
              ref={inputRef}
              className="sl-composer-textarea"
              value={body}
              onChange={(e) => onComposerChange(e.target.value)}
              onKeyDown={onComposerKey}
              onBlur={() => {
                setTimeout(() => setMentionQuery(null), 100);
                sendTypingStopped(workspaceID, channel.id);
                lastHeartbeatRef.current = 0;
              }}
              placeholder={`Message #${channel.name ?? 'channel'} (Enter to send, Shift+Enter for newline)`}
              disabled={sendMutation.isPending}
              aria-label="Message"
              rows={1}
            />
          </div>
          <button
            type="submit"
            className="sl-primary"
            disabled={(!body.trim() && pendingFiles.length === 0) || sendMutation.isPending || uploadingCount > 0}
          >
            Send
          </button>
        </div>

        {isDragging && (
          <div className="sl-drop-overlay" aria-hidden="true">
            <div className="sl-drop-overlay-inner">Drop to attach</div>
          </div>
        )}
      </form>
    </div>
  );
}

function mergeCreatedMessage(prev: Message[], incoming: Message): Message[] {
  const withoutOptimistic = prev.filter(
    (m) =>
      !(
        m.id < 0 &&
        m.body_md === incoming.body_md &&
        m.author_user_id === incoming.author_user_id
      ),
  );
  if (withoutOptimistic.some((m) => m.id === incoming.id)) return withoutOptimistic;
  return [...withoutOptimistic, incoming];
}

function applyReactionDelta(m: Message, emoji: string, userID: number, add: boolean): Message {
  const next: Message = { ...m, reactions: m.reactions.map((r) => ({ ...r, user_ids: [...r.user_ids] })) };
  const existing = next.reactions.find((r) => r.emoji === emoji);
  if (add) {
    if (existing) {
      if (!existing.user_ids.includes(userID)) existing.user_ids.push(userID);
    } else {
      next.reactions.push({ emoji, user_ids: [userID] });
    }
  } else if (existing) {
    existing.user_ids = existing.user_ids.filter((id) => id !== userID);
    if (existing.user_ids.length === 0) {
      next.reactions = next.reactions.filter((r) => r.emoji !== emoji);
    }
  }
  return next;
}

function formatTypingNames(names: string[]): string {
  if (names.length === 1) return `${names[0]} is typing…`;
  if (names.length === 2) return `${names[0]} and ${names[1]} are typing…`;
  return `${names[0]}, ${names[1]}, and ${names.length - 2} more are typing…`;
}

// ---- MessageRow -------------------------------------------------------

interface MessageRowProps {
  message: Message;
  isMine: boolean;
  editing: boolean;
  editingBody: string;
  members: readonly WorkspaceMember[];
  currentUserID?: number;
  onlineUserIDs: ReadonlySet<number>;
  onStartEdit: () => void;
  onChangeEdit: (s: string) => void;
  onSubmitEdit: () => void;
  onCancelEdit: () => void;
  onEditKey: (e: KeyboardEvent<HTMLTextAreaElement>) => void;
  onDelete: () => void;
  onReact: (emoji: string) => void;
  onOpenThread: () => void;
}

const QUICK_REACTS = ['👍', '❤️', '😂', '🎉', '🚀'];

function MessageRow(props: MessageRowProps): ReactElement {
  const { message: m } = props;
  const authorMember = props.members.find((x) => x.user_id === m.author_user_id);
  const authorName =
    authorMember?.display_name ||
    authorMember?.email.split('@')[0] ||
    `User ${m.author_user_id ?? '—'}`;
  const isOnline = m.author_user_id ? props.onlineUserIDs.has(m.author_user_id) : false;

  // Auto-grow the edit textarea whenever its value changes, mirroring the
  // composer behavior. Cap at a generous height so editing a long message
  // doesn't shove the rest of the channel off-screen.
  const editRef = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    if (!props.editing) return;
    const el = editRef.current;
    if (!el) return;
    el.style.height = 'auto';
    const max = 320;
    el.style.height = Math.min(el.scrollHeight, max) + 'px';
    el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden';
  }, [props.editing, props.editingBody]);

  return (
    <div className={`sl-msg ${props.isMine ? 'sl-msg-mine' : ''}`}>
      <div className="sl-msg-avatar-wrap">
        <div
          className="sl-msg-avatar"
          aria-hidden="true"
          style={{ background: avatarGradient(m.author_user_id) }}
        >
          {avatarInitial(authorName)}
        </div>
        {isOnline && <span className="sl-presence-dot" aria-label="online" />}
      </div>
      <div className="sl-msg-body">
        <div className="sl-msg-meta">
          <span className="sl-msg-author">{authorName}</span>
          <span className="sl-msg-time">{formatTime(m.created_at)}</span>
          {m.edited_at && <span className="sl-msg-edited">(edited)</span>}
        </div>
        {props.editing ? (
          <div className="sl-msg-edit">
            <textarea
              ref={editRef}
              autoFocus
              value={props.editingBody}
              onChange={(e) => props.onChangeEdit(e.target.value)}
              onKeyDown={props.onEditKey}
              rows={1}
            />
            <div className="sl-msg-edit-actions">
              <button type="button" className="sl-linkbtn" onClick={props.onCancelEdit}>
                Cancel
              </button>
              <button
                type="button"
                className="sl-primary sl-primary-sm"
                onClick={props.onSubmitEdit}
              >
                Save
              </button>
            </div>
          </div>
        ) : (
          <div className="sl-msg-text">
            <MessageBody
              body={m.body_md}
              members={props.members}
              currentUserID={props.currentUserID}
            />
          </div>
        )}

        <MessageAttachments attachments={m.attachments ?? []} />

        {m.reactions.length > 0 && (
          <div className="sl-msg-reactions">
            {m.reactions.map((r) => (
              <button
                key={r.emoji}
                type="button"
                className="sl-msg-reaction"
                onClick={() => props.onReact(r.emoji)}
                title={`${r.user_ids.length} ${r.user_ids.length === 1 ? 'person' : 'people'}`}
              >
                <span>{r.emoji}</span>
                <span className="sl-msg-reaction-count">{r.user_ids.length}</span>
              </button>
            ))}
          </div>
        )}

        {m.reply_count > 0 && (
          <button type="button" className="sl-thread-pill" onClick={props.onOpenThread}>
            <span className="sl-thread-pill-count">{m.reply_count}</span>
            <span>{m.reply_count === 1 ? 'reply' : 'replies'}</span>
            <span className="sl-thread-pill-cta">View thread →</span>
          </button>
        )}
      </div>

      <div className="sl-msg-actions" aria-hidden={props.editing}>
        {QUICK_REACTS.map((emoji) => (
          <button
            key={emoji}
            type="button"
            className="sl-msg-action"
            onClick={() => props.onReact(emoji)}
            aria-label={`React with ${emoji}`}
          >
            {emoji}
          </button>
        ))}
        <button
          type="button"
          className="sl-msg-action sl-msg-action-text"
          onClick={props.onOpenThread}
          title="Reply in thread"
        >
          Reply
        </button>
        {props.isMine && !props.editing && (
          <>
            <button
              type="button"
              className="sl-msg-action sl-msg-action-text"
              onClick={props.onStartEdit}
            >
              Edit
            </button>
            <button
              type="button"
              className="sl-msg-action sl-msg-action-text sl-msg-action-danger"
              onClick={props.onDelete}
            >
              Delete
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch {
    return '';
  }
}

function avatarInitial(name: string): string {
  const c = name.trim().charAt(0).toUpperCase();
  return c || '?';
}

const AVATAR_GRADIENTS = [
  'linear-gradient(135deg, #3B7DD8, #27A8AC)',
  'linear-gradient(135deg, #5BB85C, #27A8AC)',
  'linear-gradient(135deg, #1A2D43, #3B7DD8)',
  'linear-gradient(135deg, #7B4DD0, #3B7DD8)',
  'linear-gradient(135deg, #E67E42, #D94D6A)',
  'linear-gradient(135deg, #27A8AC, #5BB85C)',
];

function avatarGradient(userID?: number): string {
  if (!userID) return AVATAR_GRADIENTS[0]!;
  return AVATAR_GRADIENTS[userID % AVATAR_GRADIENTS.length]!;
}
