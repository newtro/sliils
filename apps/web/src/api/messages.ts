import { apiFetch } from './client';
import type { FileDTO } from './files';

export interface Reaction {
  emoji: string;
  user_ids: number[];
}

export interface Message {
  id: number;
  channel_id: number;
  workspace_id: number;
  author_user_id?: number;
  body_md: string;
  body_blocks: unknown[];
  reactions: Reaction[];
  thread_root_id?: number;
  parent_id?: number;
  reply_count: number;
  mentions?: number[];
  attachments: FileDTO[];
  edited_at?: string;
  deleted_at?: string;
  created_at: string;
}

export interface MessagePage {
  messages: Message[];
  next_cursor?: string;
}

export interface ThreadResponse {
  root: Message;
  replies: Message[];
}

export function listMessages(channelID: number, cursor?: string): Promise<MessagePage> {
  const q = cursor ? `?cursor=${encodeURIComponent(cursor)}` : '';
  return apiFetch<MessagePage>(`/channels/${channelID}/messages${q}`);
}

export function createMessage(
  channelID: number,
  bodyMd: string,
  opts: { parentID?: number; attachmentIDs?: number[] } = {},
): Promise<Message> {
  const body: { body_md: string; parent_id?: number; attachment_ids?: number[] } = {
    body_md: bodyMd,
  };
  if (opts.parentID) body.parent_id = opts.parentID;
  if (opts.attachmentIDs && opts.attachmentIDs.length > 0) {
    body.attachment_ids = opts.attachmentIDs;
  }
  return apiFetch<Message>(`/channels/${channelID}/messages`, {
    method: 'POST',
    body,
  });
}

export function editMessage(messageID: number, bodyMd: string): Promise<Message> {
  return apiFetch<Message>(`/messages/${messageID}`, {
    method: 'PATCH',
    body: { body_md: bodyMd },
  });
}

export function deleteMessage(messageID: number): Promise<Message> {
  return apiFetch<Message>(`/messages/${messageID}`, { method: 'DELETE' });
}

export function addReaction(messageID: number, emoji: string): Promise<void> {
  return apiFetch<void>(`/messages/${messageID}/reactions`, {
    method: 'POST',
    body: { emoji },
  });
}

export function removeReaction(messageID: number, emoji: string): Promise<void> {
  return apiFetch<void>(`/messages/${messageID}/reactions`, {
    method: 'DELETE',
    body: { emoji },
  });
}

export function getThread(messageID: number): Promise<ThreadResponse> {
  return apiFetch<ThreadResponse>(`/messages/${messageID}/thread`);
}

export function markChannelRead(channelID: number, messageID: number): Promise<void> {
  return apiFetch<void>(`/channels/${channelID}/mark-read`, {
    method: 'POST',
    body: { message_id: messageID },
  });
}
