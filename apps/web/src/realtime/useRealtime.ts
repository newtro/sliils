// Thin WebSocket singleton plus targeted hooks. Components call:
//
//   useRealtime(topics, onEvent)      — raw event feed (filter by type inside).
//   usePresence(workspaceID)          — live Set<userID> of online users.
//   useTyping(channelID)              — live Set<userID> of typists (auto-drops after 6s).
//
// One connection per tab is shared across all subscribers via reference-
// counted topic attach/detach. Reconnect happens automatically with
// exponential backoff and `since=<event_id>` replay.

import { useEffect, useRef, useState, useSyncExternalStore } from 'react';

export interface RealtimeEvent {
  id: number;
  type: string;
  topic: string;
  data: unknown;
  ts: string;
}

type Listener = (ev: RealtimeEvent) => void;

interface Envelope {
  v: number;
  type: string;
  id?: string;
  data?: unknown;
}

interface PresenceSnapshotEnvelope {
  workspace_id: number;
  user_ids: number[];
}

interface PresenceChangedEnvelope {
  workspace_id: number;
  user_id: number;
  status: 'online' | 'offline';
}

interface TypingEnvelope {
  workspace_id: number;
  channel_id: number;
  user_id: number;
}

class Connection {
  private ws: WebSocket | null = null;
  private token: string | null = null;
  private opening = false;
  private reconnectAttempts = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private topicListeners = new Map<string, Set<Listener>>();
  private lastEventID = 0;

  // presence[workspaceID] = Set<userID>
  readonly presence = new Map<number, Set<number>>();
  // typing[channelID] = Map<userID, timeoutHandle>
  private typingTimers = new Map<number, Map<number, ReturnType<typeof setTimeout>>>();
  readonly typing = new Map<number, Set<number>>();

  private presenceSubs = new Set<() => void>();
  private typingSubs = new Set<() => void>();

  setToken(token: string | null): void {
    if (token === this.token) return;
    this.token = token;
    if (!token) {
      this.close();
      return;
    }
    this.open();
  }

  addListener(topic: string, listener: Listener): () => void {
    let set = this.topicListeners.get(topic);
    if (!set) {
      set = new Set();
      this.topicListeners.set(topic, set);
      this.subscribe([topic]);
    }
    set.add(listener);
    return () => {
      const s = this.topicListeners.get(topic);
      if (!s) return;
      s.delete(listener);
      if (s.size === 0) {
        this.topicListeners.delete(topic);
        this.send({ v: 1, type: 'unsubscribe', data: { topics: [topic] } });
      }
    };
  }

  send(env: Envelope): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    try {
      this.ws.send(JSON.stringify(env));
    } catch {
      // onclose will drive reconnect.
    }
  }

  // ---- Presence hooks ----------------------------------------------------

  subscribePresence(cb: () => void): () => void {
    this.presenceSubs.add(cb);
    return () => this.presenceSubs.delete(cb);
  }

  private notifyPresence(): void {
    this.presenceSubs.forEach((cb) => cb());
  }

  // ---- Typing hooks ------------------------------------------------------

  subscribeTyping(cb: () => void): () => void {
    this.typingSubs.add(cb);
    return () => this.typingSubs.delete(cb);
  }

  private notifyTyping(): void {
    this.typingSubs.forEach((cb) => cb());
  }

  typingHeartbeat(workspaceID: number, channelID: number): void {
    this.send({
      v: 1,
      type: 'typing.heartbeat',
      data: { workspace_id: workspaceID, channel_id: channelID },
    });
  }

  typingStopped(workspaceID: number, channelID: number): void {
    this.send({
      v: 1,
      type: 'typing.stopped',
      data: { workspace_id: workspaceID, channel_id: channelID },
    });
  }

  // ---- Internals ---------------------------------------------------------

  private open(): void {
    if (this.opening || this.ws) return;
    if (!this.token) return;
    this.opening = true;

    const base = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${base}://${window.location.host}/api/v1/socket?token=${encodeURIComponent(this.token)}`;

    let ws: WebSocket;
    try {
      ws = new WebSocket(url);
    } catch (err) {
      this.opening = false;
      this.scheduleReconnect();
      console.warn('[realtime] open failed:', err);
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.opening = false;
      this.reconnectAttempts = 0;
      const topics = Array.from(this.topicListeners.keys());
      if (topics.length > 0) {
        this.send({
          v: 1,
          type: 'subscribe',
          data: { topics, since: this.lastEventID },
        });
      }
    };

    ws.onmessage = (msg) => {
      let env: Envelope;
      try {
        env = JSON.parse(msg.data);
      } catch {
        return;
      }
      if (env.type === 'hello') {
        const data = env.data as { last_event_id?: number } | undefined;
        if (data?.last_event_id !== undefined && this.lastEventID === 0) {
          this.lastEventID = data.last_event_id;
        }
        return;
      }
      if (env.type === 'presence.snapshot') {
        const data = env.data as PresenceSnapshotEnvelope | undefined;
        if (data) this.applyPresenceSnapshot(data);
        return;
      }
      if (env.type === 'event') {
        const ev = env.data as RealtimeEvent;
        this.lastEventID = Math.max(this.lastEventID, ev.id);
        this.handleInlineEvent(ev);
        const listeners = this.topicListeners.get(ev.topic);
        if (listeners) listeners.forEach((l) => l(ev));
        return;
      }
      if (env.type === 'must_resync') {
        this.lastEventID = 0;
        this.topicListeners.forEach((listeners) =>
          listeners.forEach((l) =>
            l({ id: 0, type: 'must_resync', topic: '', data: null, ts: '' }),
          ),
        );
      }
    };

    ws.onclose = () => {
      this.opening = false;
      this.ws = null;
      if (this.token) this.scheduleReconnect();
    };

    ws.onerror = () => {};
  }

  private handleInlineEvent(ev: RealtimeEvent): void {
    if (ev.type === 'presence.changed') {
      const data = ev.data as PresenceChangedEnvelope;
      let set = this.presence.get(data.workspace_id);
      if (!set) {
        set = new Set();
        this.presence.set(data.workspace_id, set);
      }
      if (data.status === 'online') set.add(data.user_id);
      else set.delete(data.user_id);
      this.notifyPresence();
      return;
    }
    if (ev.type === 'typing.started' || ev.type === 'typing.stopped') {
      const data = ev.data as TypingEnvelope;
      const channelID = data.channel_id;
      const userID = data.user_id;

      let timers = this.typingTimers.get(channelID);
      if (!timers) {
        timers = new Map();
        this.typingTimers.set(channelID, timers);
      }
      let users = this.typing.get(channelID);
      if (!users) {
        users = new Set();
        this.typing.set(channelID, users);
      }

      const existing = timers.get(userID);
      if (existing) clearTimeout(existing);

      if (ev.type === 'typing.started') {
        users.add(userID);
        // Defensive cleanup — server emits stopped at 5s, but if the
        // stopped event is lost (disconnect, slow consumer), auto-drop
        // after 6s so the UI doesn't show a stale typist forever.
        timers.set(
          userID,
          setTimeout(() => {
            users!.delete(userID);
            timers!.delete(userID);
            this.notifyTyping();
          }, 6_000),
        );
      } else {
        users.delete(userID);
        timers.delete(userID);
      }
      this.notifyTyping();
    }
  }

  private applyPresenceSnapshot(data: PresenceSnapshotEnvelope): void {
    this.presence.set(data.workspace_id, new Set(data.user_ids));
    this.notifyPresence();
  }

  private subscribe(topics: string[]): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    this.send({ v: 1, type: 'subscribe', data: { topics, since: this.lastEventID } });
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return;
    const attempt = Math.min(this.reconnectAttempts, 6);
    const delay = Math.min(500 * 2 ** attempt, 15_000) + Math.random() * 250;
    this.reconnectAttempts += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  private close(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.topicListeners.clear();
    this.presence.clear();
    this.typing.clear();
    this.typingTimers.forEach((m) => m.forEach((t) => clearTimeout(t)));
    this.typingTimers.clear();
    this.lastEventID = 0;
    this.notifyPresence();
    this.notifyTyping();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}

const conn = new Connection();

export function setRealtimeToken(token: string | null): void {
  conn.setToken(token);
}

export function useRealtime(topics: string[], onEvent: (ev: RealtimeEvent) => void): void {
  const cbRef = useRef(onEvent);
  useEffect(() => {
    cbRef.current = onEvent;
  }, [onEvent]);

  useEffect(() => {
    const unsubs = topics.map((t) =>
      conn.addListener(t, (ev) => cbRef.current(ev)),
    );
    return () => unsubs.forEach((u) => u());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topics.join('|')]);
}

export function usePresence(workspaceID: number | null): ReadonlySet<number> {
  // setVersion is only used to force a rerender on presence changes; the
  // source of truth is the Connection singleton, not React state.
  const [, setVersion] = useState(0);
  useEffect(() => conn.subscribePresence(() => setVersion((v) => v + 1)), []);
  if (!workspaceID) return EMPTY_SET;
  return conn.presence.get(workspaceID) ?? EMPTY_SET;
}

export function useTypingUsers(channelID: number | null): ReadonlySet<number> {
  const subscribe = (cb: () => void) => conn.subscribeTyping(cb);
  const getSnapshot = () => (channelID ? (conn.typing.get(channelID) ?? EMPTY_SET) : EMPTY_SET);
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function sendTypingHeartbeat(workspaceID: number, channelID: number): void {
  conn.typingHeartbeat(workspaceID, channelID);
}

export function sendTypingStopped(workspaceID: number, channelID: number): void {
  conn.typingStopped(workspaceID, channelID);
}

const EMPTY_SET: ReadonlySet<number> = Object.freeze(new Set<number>());
