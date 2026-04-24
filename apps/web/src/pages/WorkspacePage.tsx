import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactElement } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { useAuth } from '../auth/AuthContext';
import {
  findOrCreateDM,
  listDMs,
  listMyWorkspaces,
  listWorkspaceChannels,
  listWorkspaceMembers,
} from '../api/workspaces';
import type { Channel, DM, WorkspaceMembership } from '../api/workspaces';
import { startOrGetMeeting } from '../api/calls';
import { ApiError } from '../api/client';
import { CallSurface } from '../components/CallSurface';
import { ChannelView } from '../components/ChannelView';
import { IncomingCallBanner } from '../components/IncomingCallBanner';
import type { IncomingCall } from '../components/IncomingCallBanner';
import { SearchOverlay } from '../components/SearchOverlay';
import { ThreadPanel } from '../components/ThreadPanel';
import { WorkspacePrefs } from '../components/WorkspacePrefs';
import { ThemeToggle } from '../theme/ThemeToggle';
import { useRealtime } from '../realtime/useRealtime';
import type { RealtimeEvent } from '../realtime/useRealtime';

export function WorkspacePage(): ReactElement {
  const { user, loading: authLoading, logout } = useAuth();
  const { slug = '' } = useParams();
  const navigate = useNavigate();

  const [selectedChannelID, setSelectedChannelID] = useState<number | null>(null);
  const [openThreadID, setOpenThreadID] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [prefsOpen, setPrefsOpen] = useState(false);
  const [pendingScrollMessageID, setPendingScrollMessageID] = useState<number | null>(null);

  // Call state (M8). `activeCall` is the meeting the user is currently
  // in; when set, the CallSurface replaces the channel pane. `ringingCall`
  // is an incoming invitation we've received but haven't answered.
  const [activeCall, setActiveCall] = useState<{
    meetingID: number;
    channelID: number;
    channelLabel: string;
    isHost: boolean;
  } | null>(null);
  const [ringingCall, setRingingCall] = useState<IncomingCall | null>(null);
  const [newDMPickerOpen, setNewDMPickerOpen] = useState(false);

  // Memberships are a TanStack query now so PATCH /me/workspaces/:slug/*
  // can invalidate ['my-workspaces'] and the sidebar picks up status +
  // notify_pref changes without a page reload.
  const membershipsQuery = useQuery({
    queryKey: ['my-workspaces'],
    queryFn: () => listMyWorkspaces(),
    enabled: !!user,
    staleTime: 30_000,
  });
  const memberships: WorkspaceMembership[] | null = membershipsQuery.data ?? null;

  const channelsQuery = useQuery({
    queryKey: ['workspace', slug, 'channels'],
    queryFn: () => listWorkspaceChannels(slug),
    enabled: !!user && !!slug,
    // Poll unread counts every 15s so sidebar badges update even when the
    // user isn't actively watching a channel. Realtime events drive
    // in-channel freshness already.
    refetchInterval: 15_000,
  });

  const membersQuery = useQuery({
    queryKey: ['workspace', slug, 'members'],
    queryFn: () => listWorkspaceMembers(slug),
    enabled: !!user && !!slug,
  });

  const dmsQuery = useQuery({
    queryKey: ['workspace', slug, 'dms'],
    queryFn: () => listDMs(slug),
    enabled: !!user && !!slug,
    staleTime: 15_000,
  });

  useEffect(() => {
    if (membershipsQuery.error) {
      const err = membershipsQuery.error;
      setError(err instanceof ApiError ? (err.problem.detail ?? err.message) : 'Load failed');
    }
  }, [membershipsQuery.error]);

  const channels: Channel[] = channelsQuery.data ?? [];

  // Auto-select #general (or first channel) once we know the list.
  useEffect(() => {
    if (selectedChannelID !== null) return;
    if (channels.length === 0) return;
    const pick = channels.find((c) => c.name === 'general') ?? channels[0]!;
    setSelectedChannelID(pick.id);
  }, [channels, selectedChannelID]);

  // Reset selected channel + thread when switching workspace.
  useEffect(() => {
    setSelectedChannelID(null);
    setOpenThreadID(null);
  }, [slug]);

  const members = useMemo(() => membersQuery.data ?? [], [membersQuery.data]);
  const current = memberships?.find((m) => m.workspace.slug === slug) ?? null;
  // The channels endpoint only returns public channels; DMs are in
  // `dmsQuery.data`. When the user selects a DM we synthesize a Channel
  // shape so ChannelView can render it without knowing about DMs.
  const selectedChannel = useMemo(() => {
    const inChannels = channels.find((c) => c.id === selectedChannelID);
    if (inChannels) return inChannels;
    const dmsData = dmsQuery.data ?? [];
    const dm = dmsData.find((d) => d.channel_id === selectedChannelID);
    if (!dm || !current) return null;
    return {
      id: dm.channel_id,
      workspace_id: current.workspace.id,
      type: 'dm' as const,
      name: undefined,
      topic: '',
      description: '',
      default_join: false,
      created_at: dm.created_at,
      unread_count: 0,
      mention_count: 0,
    };
  }, [channels, selectedChannelID, dmsQuery.data, current]);

  // Workspace-level subscription for presence + mention events. Channel
  // subscriptions are owned by ChannelView / ThreadPanel as they mount.
  const qc = useQueryClient();
  const workspaceTopic = current ? `ws:${current.workspace.id}` : '';
  const dms: DM[] = useMemo(() => dmsQuery.data ?? [], [dmsQuery.data]);

  const onWorkspaceEvent = useCallback(
    (ev: RealtimeEvent) => {
      if (ev.type === 'mention.created') {
        qc.invalidateQueries({ queryKey: ['workspace', slug, 'channels'] });
        return;
      }
      if (ev.type === 'meeting.started') {
        const data = ev.data as {
          meeting_id: number;
          channel_id: number;
          started_by: number;
        };
        // Don't ring ourselves. If we started the call, the button
        // click path handed us an activeCall already.
        if (user && data.started_by === user.id) return;
        // If we're already in a call, ignore the ring — don't prompt.
        if (activeCall) return;

        // Build a human-friendly label for the channel: DM counterpart
        // name when possible, otherwise `#channel_name`.
        const dm = dms.find((d) => d.channel_id === data.channel_id);
        const channel = channels.find((c) => c.id === data.channel_id);
        const channelLabel = dm
          ? `DM with ${dm.other_display_name}`
          : channel?.name
          ? `#${channel.name}`
          : `channel ${data.channel_id}`;
        const starterMember = members.find((m) => m.user_id === data.started_by);
        setRingingCall({
          meetingID: data.meeting_id,
          channelID: data.channel_id,
          channelLabel,
          startedBy: data.started_by,
          startedByName: starterMember?.display_name || starterMember?.email,
        });
        return;
      }
      if (ev.type === 'meeting.ended') {
        const data = ev.data as { meeting_id: number };
        if (ringingCall && ringingCall.meetingID === data.meeting_id) {
          setRingingCall(null);
        }
        if (activeCall && activeCall.meetingID === data.meeting_id) {
          // Host ended the call remotely — leave the surface up for
          // LiveKit's own disconnect flow to unmount it.
        }
        return;
      }
    },
    [qc, slug, user, activeCall, ringingCall, dms, channels, members],
  );
  useRealtime(workspaceTopic ? [workspaceTopic] : [], onWorkspaceEvent);

  // Global cmd+K / ctrl+K to open search. Ignored if a form field other
  // than the search overlay already has focus handling it — browsers route
  // the event to the active element first but defaultPrevented=false means
  // we still act on it. `e.metaKey || e.ctrlKey` covers both platforms.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setSearchOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // onSearchNavigate jumps to a channel + marks a message id for scrolling.
  const onSearchNavigate = useCallback(
    (args: { channelID: number; messageID: number }) => {
      setSelectedChannelID(args.channelID);
      setOpenThreadID(null);
      setPendingScrollMessageID(args.messageID);
    },
    [],
  );

  // Start a call from the current channel header. Idempotent server-side;
  // if the channel already has a meeting in progress, we just join it.
  const onStartCall = useCallback(
    async (channelID: number, channelLabel: string) => {
      try {
        const meeting = await startOrGetMeeting(channelID);
        const isHost = user != null && meeting.started_by === user.id;
        setActiveCall({
          meetingID: meeting.id,
          channelID,
          channelLabel,
          isHost,
        });
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not start call');
      }
    },
    [user],
  );

  // Answer a ringing call.
  const onAnswerRing = useCallback(() => {
    if (!ringingCall) return;
    setActiveCall({
      meetingID: ringingCall.meetingID,
      channelID: ringingCall.channelID,
      channelLabel: ringingCall.channelLabel,
      isHost: false,
    });
    setRingingCall(null);
  }, [ringingCall]);

  // Select a DM. DMs live under `dms` state and use the same channel
  // infrastructure, so we just flip selectedChannelID.
  const onSelectDM = useCallback(
    (dm: DM) => {
      setSelectedChannelID(dm.channel_id);
      setOpenThreadID(null);
    },
    [],
  );

  const onCreateDM = useCallback(
    async (userID: number) => {
      if (!slug) return;
      try {
        const dm = await findOrCreateDM(slug, userID);
        // Refresh the list so it appears under Direct Messages even if
        // we just created the first pair.
        qc.invalidateQueries({ queryKey: ['workspace', slug, 'dms'] });
        setSelectedChannelID(dm.channel_id);
        setOpenThreadID(null);
        setNewDMPickerOpen(false);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not start DM');
      }
    },
    [qc, slug],
  );

  if (authLoading) {
    return <div className="sl-placeholder">Loading…</div>;
  }
  if (!user) return <Navigate to="/login" replace />;
  if (user.needs_setup) return <Navigate to="/setup" replace />;

  if (error) {
    return (
      <div className="sl-placeholder">
        <div className="sl-card" style={{ maxWidth: 480 }}>
          <h1 className="sl-auth-heading" aria-label="SliilS">
            {'Sl'}
            <span className="sl-i-green" aria-hidden="true">i</span>
            <span className="sl-i-blue" aria-hidden="true">i</span>
            {'lS'}
          </h1>
          <div role="alert" className="sl-error" style={{ margin: '16px 0' }}>
            {error}
          </div>
          <Link to="/">Go home</Link>
        </div>
      </div>
    );
  }

  return (
    <div className={`sl-ws-shell ${openThreadID !== null ? 'with-thread' : ''}`}>
      <aside className="sl-ws-rail" aria-label="Workspace switcher">
        <button
          type="button"
          className="sl-ws-brand"
          title="SliilS home"
          aria-label="SliilS home"
          onClick={() => navigate('/')}
        >
          <img src="/favicon.png" alt="" />
        </button>
        <div className="sl-ws-rail-sep" aria-hidden="true" />
        {memberships?.map((m) => (
          <Link
            key={m.workspace.id}
            to={`/w/${m.workspace.slug}`}
            className={`sl-ws-rail-icon ${m.workspace.slug === slug ? 'active' : ''}`}
            title={m.workspace.name}
            aria-current={m.workspace.slug === slug ? 'true' : undefined}
          >
            {m.workspace.name[0]?.toUpperCase() ?? '?'}
          </Link>
        ))}
        <div className="sl-ws-rail-spacer" />
        <div className="sl-ws-rail-bottom">
          <ThemeToggle />
          <button
            type="button"
            className="sl-ws-rail-icon sl-ws-rail-icon-btn"
            title="Sign out"
            aria-label="Sign out"
            onClick={logout}
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
          </button>
        </div>
      </aside>

      <nav className="sl-ws-pane" aria-label={`${current?.workspace.name ?? slug} navigation`}>
        <header className="sl-ws-pane-header">
          <div className="sl-ws-pane-name">{current?.workspace.name ?? slug}</div>
          {current && (
            <button
              type="button"
              className="sl-ws-pane-user"
              onClick={() => setPrefsOpen((v) => !v)}
              aria-expanded={prefsOpen}
              aria-haspopup="dialog"
            >
              {current.custom_status?.emoji && (
                <span className="sl-ws-status-emoji" aria-hidden="true">
                  {current.custom_status.emoji}
                </span>
              )}
              <span className="sl-ws-pane-user-text">
                <span className="sl-ws-pane-user-name">
                  {user.display_name || user.email}
                </span>
                <span className="sl-ws-pane-subtle">
                  {current.custom_status?.text
                    ? current.custom_status.text
                    : `${current.role}${
                        current.notify_pref !== 'all'
                          ? ` · ${notifyPrefShort(current.notify_pref)}`
                          : ''
                      }`}
                </span>
              </span>
            </button>
          )}
          {prefsOpen && current && (
            <WorkspacePrefs
              workspaceSlug={current.workspace.slug}
              currentStatus={current.custom_status}
              currentNotifyPref={current.notify_pref}
              onClose={() => setPrefsOpen(false)}
            />
          )}
        </header>

        {current && (
          <section className="sl-ws-section">
            <div className="sl-ws-section-label">Workspace</div>
            <ul className="sl-ws-navlist">
              <li>
                <button
                  type="button"
                  className="sl-ws-navitem"
                  onClick={() => navigate(`/w/${current.workspace.slug}/pages`)}
                >
                  Pages
                </button>
              </li>
              <li>
                <button
                  type="button"
                  className="sl-ws-navitem"
                  onClick={() => navigate(`/w/${current.workspace.slug}/calendar`)}
                >
                  Calendar
                </button>
              </li>
              {(current.role === 'owner' || current.role === 'admin') && (
                <li>
                  <button
                    type="button"
                    className="sl-ws-navitem"
                    onClick={() => navigate(`/w/${current.workspace.slug}/admin`)}
                  >
                    Admin
                  </button>
                </li>
              )}
            </ul>
          </section>
        )}

        <section className="sl-ws-section">
          <div className="sl-ws-section-label">Channels</div>
          <ul className="sl-ws-channel-list">
            {channelsQuery.isLoading && <li className="sl-muted">Loading…</li>}
            {channels.map((c) => {
              const isActive = c.id === selectedChannelID;
              const hasUnread = c.unread_count > 0 && !isActive;
              const hasMention = c.mention_count > 0 && !isActive;
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    onClick={() => setSelectedChannelID(c.id)}
                    className={`sl-ws-channel sl-ws-channel-btn ${isActive ? 'active' : ''} ${
                      hasUnread ? 'unread' : ''
                    }`}
                    aria-current={isActive ? 'true' : undefined}
                  >
                    <span className="sl-ws-channel-hash">#</span>
                    <span className="sl-ws-channel-name">{c.name ?? '—'}</span>
                    {hasMention && (
                      <span className="sl-ws-channel-badge sl-ws-channel-badge-mention">
                        {c.mention_count}
                      </span>
                    )}
                    {hasUnread && !hasMention && (
                      <span className="sl-ws-channel-badge">{c.unread_count}</span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>

        <section className="sl-ws-section">
          <div className="sl-ws-section-header">
            <div className="sl-ws-section-label">Direct messages</div>
            <button
              type="button"
              className="sl-ws-section-action"
              onClick={() => setNewDMPickerOpen((v) => !v)}
              aria-expanded={newDMPickerOpen}
              title="New direct message"
            >
              +
            </button>
          </div>
          {newDMPickerOpen && (
            <ul className="sl-ws-dm-picker" role="listbox" aria-label="Pick someone">
              {members
                .filter((m) => m.user_id !== user.id)
                .filter((m) => !dms.some((d) => d.other_user_id === m.user_id))
                .map((m) => (
                  <li key={m.user_id}>
                    <button
                      type="button"
                      className="sl-ws-dm-picker-row"
                      onClick={() => onCreateDM(m.user_id)}
                    >
                      <span className="sl-ws-dm-dot" aria-hidden="true" />
                      <span>{m.display_name || m.email.split('@')[0]}</span>
                    </button>
                  </li>
                ))}
              {members.filter((m) => m.user_id !== user.id && !dms.some((d) => d.other_user_id === m.user_id)).length === 0 && (
                <li className="sl-muted" style={{ padding: '4px 8px', fontSize: 13 }}>
                  Everyone in this workspace already has a DM with you.
                </li>
              )}
            </ul>
          )}
          <ul className="sl-ws-channel-list">
            {dms.map((d) => {
              const isActive = d.channel_id === selectedChannelID;
              return (
                <li key={d.channel_id}>
                  <button
                    type="button"
                    onClick={() => onSelectDM(d)}
                    className={`sl-ws-channel sl-ws-channel-btn ${isActive ? 'active' : ''}`}
                    aria-current={isActive ? 'true' : undefined}
                  >
                    <span className="sl-ws-dm-dot" aria-hidden="true" />
                    <span className="sl-ws-channel-name">{d.other_display_name}</span>
                  </button>
                </li>
              );
            })}
            {dms.length === 0 && (
              <li className="sl-muted" style={{ padding: '4px 8px', fontSize: 13 }}>
                No DMs yet.
              </li>
            )}
          </ul>
        </section>
      </nav>

      <main className="sl-ws-main">
        {activeCall && current ? (
          <CallSurface
            key={activeCall.meetingID}
            meetingID={activeCall.meetingID}
            channelID={activeCall.channelID}
            channelLabel={activeCall.channelLabel}
            isHost={activeCall.isHost}
            onLeave={() => setActiveCall(null)}
          />
        ) : selectedChannel && current ? (
          <>
            <header className="sl-ws-main-header">
              <div>
                <h1 className="sl-ws-main-title">
                  {selectedChannel.type === 'dm' ? (
                    <>
                      <span className="sl-ws-dm-dot" aria-hidden="true" style={{ marginRight: 8 }} />
                      {(() => {
                        const dm = dms.find((d) => d.channel_id === selectedChannel.id);
                        return dm ? dm.other_display_name : 'Direct message';
                      })()}
                    </>
                  ) : (
                    <>
                      <span className="hash">#</span>
                      {selectedChannel.name ?? ''}
                    </>
                  )}
                </h1>
                <div className="sl-ws-main-topic">
                  {selectedChannel.topic || (selectedChannel.type === 'dm' ? 'Just the two of you.' : 'No topic yet.')}
                </div>
              </div>
              <div className="sl-ws-main-actions">
                <button
                  type="button"
                  className="sl-call-btn"
                  onClick={() => {
                    const label =
                      selectedChannel.type === 'dm'
                        ? (() => {
                            const dm = dms.find((d) => d.channel_id === selectedChannel.id);
                            return dm ? `DM with ${dm.other_display_name}` : 'direct message';
                          })()
                        : `#${selectedChannel.name ?? ''}`;
                    onStartCall(selectedChannel.id, label);
                  }}
                  title="Start call"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M23 7l-7 5 7 5V7z" />
                    <rect x="1" y="5" width="15" height="14" rx="2" ry="2" />
                  </svg>
                  <span>Call</span>
                </button>
              </div>
            </header>
            <ChannelView
              key={selectedChannel.id}
              workspaceID={current.workspace.id}
              channel={selectedChannel}
              members={members}
              onOpenThread={setOpenThreadID}
              scrollToMessageID={
                pendingScrollMessageID &&
                selectedChannel.id === selectedChannelID
                  ? pendingScrollMessageID
                  : null
              }
              onScrolledToMessage={() => setPendingScrollMessageID(null)}
            />
          </>
        ) : (
          <div className="sl-ws-main-body">
            <div className="sl-ws-empty">
              <h2>Pick a channel</h2>
              <p className="sl-muted">Select a channel from the left to start reading.</p>
            </div>
          </div>
        )}
      </main>

      {openThreadID !== null && selectedChannel && current && (
        <ThreadPanel
          workspaceID={current.workspace.id}
          channel={selectedChannel}
          rootID={openThreadID}
          members={members}
          onClose={() => setOpenThreadID(null)}
        />
      )}

      {current && (
        <SearchOverlay
          workspaceID={current.workspace.id}
          open={searchOpen}
          onClose={() => setSearchOpen(false)}
          onNavigate={onSearchNavigate}
        />
      )}

      {ringingCall && !activeCall && (
        <IncomingCallBanner
          call={ringingCall}
          onAnswer={onAnswerRing}
          onDismiss={() => setRingingCall(null)}
        />
      )}
    </div>
  );
}

function notifyPrefShort(p: 'all' | 'mentions' | 'mute'): string {
  switch (p) {
    case 'mentions':
      return 'mentions only';
    case 'mute':
      return 'muted';
    default:
      return '';
  }
}
