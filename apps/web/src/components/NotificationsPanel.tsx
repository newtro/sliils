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
  const [qhTz, setQHTz] = useState<string>(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
  const [snoozeMins, setSnoozeMins] = useState<number>(60);

  const dndMutation = useMutation({
    mutationFn: (prefs: Parameters<typeof patchDND>[0]) => patchDND(prefs),
  });

  // Re-read browser permission on mount in case it changed outside the
  // component (e.g. user toggled it in site settings).
  useEffect(() => {
    setBrowserState(getBrowserPushState());
  }, []);

  const activeWebDevice = useMemo(
    () => devicesQuery.data?.find((d) => d.platform === 'web' || d.platform === 'tauri'),
    [devicesQuery.data],
  );

  return (
    <div style={{ maxWidth: 560 }}>
      <h3 style={{ marginTop: 0 }}>Notifications</h3>

      <section style={sectionStyle}>
        <h4 style={{ margin: '0 0 8px' }}>Browser push</h4>
        {browserState === 'unsupported' && (
          <p style={mutedStyle}>This browser does not support web push.</p>
        )}
        {browserState === 'denied' && (
          <p style={{ color: '#a33' }}>
            Notifications are blocked in your browser settings. Un-block SliilS to enable push.
          </p>
        )}
        {browserState !== 'unsupported' && browserState !== 'denied' && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {activeWebDevice ? (
              <>
                <span style={{ color: '#2a8a2a' }}>✓ Enabled on this device</span>
                <button
                  type="button"
                  onClick={() => disableMutation.mutate(activeWebDevice.id)}
                  disabled={disableMutation.isPending}
                  style={btn}
                >
                  {disableMutation.isPending ? 'Disabling…' : 'Disable'}
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => enableMutation.mutate()}
                disabled={enableMutation.isPending}
                style={btnPrimary}
              >
                {enableMutation.isPending ? 'Enabling…' : 'Enable notifications'}
              </button>
            )}
          </div>
        )}
        {error && <p style={{ color: '#a33', marginTop: 8 }}>{error}</p>}
      </section>

      <section style={sectionStyle}>
        <h4 style={{ margin: '0 0 8px' }}>Do not disturb</h4>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 12 }}>
          <span>Snooze for</span>
          <input
            type="number"
            min={5}
            max={1440}
            value={snoozeMins}
            onChange={(e) => setSnoozeMins(Number(e.target.value))}
            style={{ width: 80 }}
          />
          <span>minutes</span>
          <button
            type="button"
            onClick={() =>
              dndMutation.mutate({
                snooze_until: new Date(Date.now() + snoozeMins * 60_000).toISOString(),
              })
            }
            style={btn}
          >
            Snooze
          </button>
          <button
            type="button"
            onClick={() => dndMutation.mutate({ snooze_until: '' })}
            style={btn}
          >
            Clear
          </button>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, auto)', gap: 8, alignItems: 'center' }}>
          <label>Quiet hours start</label>
          <input
            type="time"
            value={qhStart !== null ? minutesToHHMM(qhStart) : ''}
            onChange={(e) => setQHStart(e.target.value ? hhmmToMinutes(e.target.value) : null)}
          />
          <span />
          <label>Quiet hours end</label>
          <input
            type="time"
            value={qhEnd !== null ? minutesToHHMM(qhEnd) : ''}
            onChange={(e) => setQHEnd(e.target.value ? hhmmToMinutes(e.target.value) : null)}
          />
          <span />
          <label>Time zone</label>
          <input
            type="text"
            value={qhTz}
            onChange={(e) => setQHTz(e.target.value)}
            style={{ minWidth: 200 }}
          />
          <span />
        </div>

        <div style={{ marginTop: 12, display: 'flex', gap: 8 }}>
          <button
            type="button"
            onClick={() =>
              dndMutation.mutate({
                quiet_hours_start: qhStart,
                quiet_hours_end: qhEnd,
                quiet_hours_tz: qhTz,
              })
            }
            style={btn}
            disabled={(qhStart === null) !== (qhEnd === null)}
          >
            Save quiet hours
          </button>
          <button
            type="button"
            onClick={() => {
              setQHStart(null);
              setQHEnd(null);
              dndMutation.mutate({ quiet_hours_start: null, quiet_hours_end: null });
            }}
            style={btn}
          >
            Clear
          </button>
        </div>
      </section>

      <section style={sectionStyle}>
        <h4 style={{ margin: '0 0 8px' }}>Registered devices</h4>
        {devicesQuery.data && devicesQuery.data.length === 0 && (
          <p style={mutedStyle}>No devices yet.</p>
        )}
        <ul style={{ margin: 0, padding: 0, listStyle: 'none' }}>
          {(devicesQuery.data ?? []).map((d: Device) => (
            <li
              key={d.id}
              style={{
                padding: 8,
                border: '1px solid #eee',
                borderRadius: 4,
                marginBottom: 6,
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <div>
                <strong>{d.label || d.platform}</strong>
                <div style={{ fontSize: 11, color: '#888' }}>
                  {d.platform} · added {new Date(d.created_at).toLocaleDateString()}
                </div>
              </div>
              <button
                type="button"
                onClick={() => disableMutation.mutate(d.id)}
                style={btn}
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

const sectionStyle: React.CSSProperties = {
  padding: 16,
  borderTop: '1px solid #eee',
};

const mutedStyle: React.CSSProperties = { color: '#888' };

const btn: React.CSSProperties = {
  padding: '6px 12px',
  border: '1px solid #ccc',
  background: '#fff',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 13,
};

const btnPrimary: React.CSSProperties = {
  ...btn,
  background: '#2a4ea4',
  color: '#fff',
  border: '1px solid #2a4ea4',
};

function minutesToHHMM(m: number): string {
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return `${String(h).padStart(2, '0')}:${String(mm).padStart(2, '0')}`;
}

function hhmmToMinutes(s: string): number {
  const parts = s.split(':').map((n) => Number(n));
  return (parts[0] ?? 0) * 60 + (parts[1] ?? 0);
}
