import { useEffect, useMemo, useState } from 'react';
import type { ReactElement } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  deleteDevice,
  disableWebPush,
  enableWebPush,
  listDevices,
  patchDND,
} from '../api/push';
import type { Device } from '../api/push';

type BrowserPushState = 'unsupported' | 'default' | 'granted' | 'denied';

function getBrowserPushState(): BrowserPushState {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported';
  return Notification.permission as BrowserPushState;
}

export function NotificationsPanel(): ReactElement {
  const qc = useQueryClient();
  const [browserState, setBrowserState] = useState<BrowserPushState>(getBrowserPushState());
  const [error, setError] = useState<string | null>(null);

  const devicesQuery = useQuery({
    queryKey: ['my-devices'],
    queryFn: () => listDevices(),
  });

  const enableMutation = useMutation({
    mutationFn: () => enableWebPush(),
    onSuccess: () => {
      setBrowserState(getBrowserPushState());
      setError(null);
      qc.invalidateQueries({ queryKey: ['my-devices'] });
    },
    onError: (err: Error) => setError(err.message),
  });

  const disableMutation = useMutation({
    mutationFn: async (id: number) => {
      await disableWebPush();
      await deleteDevice(id);
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['my-devices'] }),
  });

  const [qhStart, setQHStart] = useState<number | null>(null);
  const [qhEnd, setQHEnd] = useState<number | null>(null);
  const [qhTz, setQHTz] = useState<string>(
    Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
  );
  const [snoozeMins, setSnoozeMins] = useState<number>(60);

  const dndMutation = useMutation({
    mutationFn: (prefs: Parameters<typeof patchDND>[0]) => patchDND(prefs),
  });

  useEffect(() => {
    setBrowserState(getBrowserPushState());
  }, []);

  const activeWebDevice = useMemo(
    () => devicesQuery.data?.find((d) => d.platform === 'web' || d.platform === 'tauri'),
    [devicesQuery.data],
  );

  return (
    <div style={{ maxWidth: 560 }}>
      <section className="sl-notif-section">
        <h4>Browser push</h4>
        {browserState === 'unsupported' && (
          <p className="sl-notif-muted">This browser does not support web push.</p>
        )}
        {browserState === 'denied' && (
          <p className="sl-notif-status-bad">
            Notifications are blocked in your browser settings. Un-block SliilS to enable push.
          </p>
        )}
        {browserState !== 'unsupported' && browserState !== 'denied' && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {activeWebDevice ? (
              <>
                <span className="sl-notif-status-good">Enabled on this device</span>
                <button
                  type="button"
                  className="sl-notif-btn"
                  onClick={() => disableMutation.mutate(activeWebDevice.id)}
                  disabled={disableMutation.isPending}
                >
                  {disableMutation.isPending ? 'Disabling…' : 'Disable'}
                </button>
              </>
            ) : (
              <button
                type="button"
                className="sl-primary sl-primary-sm"
                onClick={() => enableMutation.mutate()}
                disabled={enableMutation.isPending}
              >
                {enableMutation.isPending ? 'Enabling…' : 'Enable notifications'}
              </button>
            )}
          </div>
        )}
        {error && (
          <p className="sl-notif-status-bad" style={{ marginTop: 8, whiteSpace: 'pre-wrap' }}>
            {error}
          </p>
        )}
      </section>

      <section className="sl-notif-section">
        <h4>Do not disturb</h4>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
          <span className="sl-notif-muted">Snooze for</span>
          <input
            type="number"
            min={5}
            max={1440}
            value={snoozeMins}
            onChange={(e) => setSnoozeMins(Number(e.target.value))}
            className="sl-notif-number"
            aria-label="Snooze minutes"
          />
          <span className="sl-notif-muted">minutes</span>
          <button
            type="button"
            className="sl-notif-btn"
            onClick={() =>
              dndMutation.mutate({
                snooze_until: new Date(Date.now() + snoozeMins * 60_000).toISOString(),
              })
            }
          >
            Snooze
          </button>
          <button
            type="button"
            className="sl-notif-btn"
            onClick={() => dndMutation.mutate({ snooze_until: '' })}
          >
            Clear
          </button>
        </div>

        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'max-content 1fr',
            gap: 8,
            alignItems: 'center',
          }}
        >
          <label className="sl-notif-muted" htmlFor="notif-qh-start">
            Quiet hours start
          </label>
          <input
            id="notif-qh-start"
            type="time"
            value={qhStart !== null ? minutesToHHMM(qhStart) : ''}
            onChange={(e) => setQHStart(e.target.value ? hhmmToMinutes(e.target.value) : null)}
            className="sl-notif-input"
          />
          <label className="sl-notif-muted" htmlFor="notif-qh-end">
            Quiet hours end
          </label>
          <input
            id="notif-qh-end"
            type="time"
            value={qhEnd !== null ? minutesToHHMM(qhEnd) : ''}
            onChange={(e) => setQHEnd(e.target.value ? hhmmToMinutes(e.target.value) : null)}
            className="sl-notif-input"
          />
          <label className="sl-notif-muted" htmlFor="notif-qh-tz">
            Time zone
          </label>
          <input
            id="notif-qh-tz"
            type="text"
            value={qhTz}
            onChange={(e) => setQHTz(e.target.value)}
            className="sl-notif-input"
          />
        </div>

        <div style={{ marginTop: 12, display: 'flex', gap: 8 }}>
          <button
            type="button"
            className="sl-notif-btn"
            onClick={() =>
              dndMutation.mutate({
                quiet_hours_start: qhStart,
                quiet_hours_end: qhEnd,
                quiet_hours_tz: qhTz,
              })
            }
            disabled={(qhStart === null) !== (qhEnd === null)}
          >
            Save quiet hours
          </button>
          <button
            type="button"
            className="sl-notif-btn"
            onClick={() => {
              setQHStart(null);
              setQHEnd(null);
              dndMutation.mutate({ quiet_hours_start: null, quiet_hours_end: null });
            }}
          >
            Clear
          </button>
        </div>
      </section>

      <section className="sl-notif-section">
        <h4>Registered devices</h4>
        {devicesQuery.data && devicesQuery.data.length === 0 && (
          <p className="sl-notif-muted">No devices yet.</p>
        )}
        <ul style={{ margin: 0, padding: 0, listStyle: 'none' }}>
          {(devicesQuery.data ?? []).map((d: Device) => (
            <li key={d.id} className="sl-notif-device">
              <div>
                <strong>{d.label || d.platform}</strong>
                <div className="sl-notif-muted" style={{ fontSize: 12 }}>
                  {d.platform} · added {new Date(d.created_at).toLocaleDateString()}
                </div>
              </div>
              <button
                type="button"
                className="sl-notif-btn"
                onClick={() => disableMutation.mutate(d.id)}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

function minutesToHHMM(m: number): string {
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return `${String(h).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

function hhmmToMinutes(s: string): number {
  const parts = s.split(':').map((n) => Number(n));
  return (parts[0] ?? 0) * 60 + (parts[1] ?? 0);
}
