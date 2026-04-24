import { apiFetch } from './client';

// The server's /search payload shape. Kept narrow — we ignore fields the
// cmd+K overlay doesn't render (thread_root_id, etc.) until a feature needs
// them, at which point they become additive.

export interface SearchHit {
  message_id: number;
  channel_id: number;
  channel_name?: string;
  channel_type: 'public' | 'private' | 'dm' | 'group_dm';
  workspace_id: number;
  author_user_id?: number;
  author_display_name?: string;
  /** Meilisearch-highlighted snippet (contains <mark> tags). */
  snippet: string;
  body_md: string;
  created_at: string;
  thread_root_id?: number;
}

export interface ParsedQuery {
  Text: string;
  From: string[];
  InChannels: string[];
  HasLink: boolean;
  HasFile: boolean;
  Mentions: string[];
}

export interface TenantToken {
  token: string;
  expires_at: string;
  index_uid: string;
}

export interface SearchResult {
  hits: SearchHit[];
  estimated_total_hits: number;
  processing_time_ms: number;
  parsed: ParsedQuery;
  token?: TenantToken;
}

export interface SearchParams {
  workspaceID: number;
  query: string;
  limit?: number;
  offset?: number;
  /** Request a fresh tenant token in the response. */
  issueToken?: boolean;
}

export function search(params: SearchParams): Promise<SearchResult> {
  return apiFetch<SearchResult>('/search', {
    method: 'POST',
    body: {
      workspace_id: params.workspaceID,
      query: params.query,
      limit: params.limit,
      offset: params.offset,
      issue_token: params.issueToken,
    },
  });
}
