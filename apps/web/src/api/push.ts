import { apiFetch } from './client';

// Web push registration flow (M11).
//
// Steps, in order:
//   1. fetchVAPIDPublicKey() — server tells us which application server
//      key to use when subscribing.
//   2. enableWebPush() — requests Notification permission, registers
//      the service worker, subscribes via PushManager, and POSTs the
//      resulting endpoint + keys to /me/devices.
//   3. disableWebPush() — reverses step 2.

export interface Device {
  id: number;
  platform: string;
  label?: string;
  user_agent?: string;
  created_at: string;
  last_seen_at: string;
}

export interface DNDPrefs {
  snooze_until?: string | null;
  quiet_hours_start?: number | null;
  quiet_hours_end?: number | null;
  quiet_hours_tz?: string;
}

export function fetchVAPIDPublicKey(): Promise<{ public_key: string }> {
  return apiFetch<{ public_key: string }>('/me/push-public-key');
}

export function listDevices(): Promise<Device[]> {
  return apiFetch<Device[]>('/me/devices');
}

export function registerDevice(input: {
  platform: string;
  endpoint: string;
  p256dh?: string;
  auth_secret?: string;
  label?: string;
}): Promise<Device> {
  return apiFetch<Device>('/me/devices', { method: 'POST', body: JSON.stringify(input) });
}

export function deleteDevice(id: number): Promise<void> {
  return apiFetch<void>(`/me/devices/${id}`, { method: 'DELETE' });
}

export function patchDND(prefs: DNDPrefs): Promise<void> {
  return apiFetch<void>('/me/dnd', { method: 'PATCH', body: JSON.stringify(prefs) });
}

// ---- orchestration -----------------------------------------------------

/**
 * Enable web push end-to-end: permission → SW registration → subscription →
 * POST /me/devices. Returns the device DTO on success. Throws with a
 * user-meaningful Error on any failure (permission denied, no SW support,
 * etc.).
 */
export async function enableWebPush(label?: string): Promise<Device> {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator) || !('PushManager' in window)) {
    throw new Error('This browser does not support web push.');
  }
  const perm = await Notification.requestPermission();
  if (perm !== 'granted') {
    throw new Error('Notifications permission was not granted.');
  }

  const { public_key } = await fetchVAPIDPublicKey();
  if (!public_key) {
    throw new Error('Server has not configured VAPID keys.');
  }

  // Make sure the SW is registered before we try to subscribe. We pass
  // updateViaCache: 'none' so dev-cycle changes to sw.js pick up
  // without a hard cache purge.
  const reg = await navigator.serviceWorker.register('/sw.js', { updateViaCache: 'none' });
  await navigator.serviceWorker.ready;

  // Validate VAPID key length up-front so the DOMException we'd
  // otherwise catch becomes a clean actionable error.
  const keyBytes = urlBase64ToUint8Array(public_key);
  if (keyBytes.length !== 65) {
    throw new Error(
      `VAPID public key must decode to 65 bytes (got ${keyBytes.length}). ` +
      `Regenerate keys with: sliils-app genvapid`,
    );
  }

  // Copy through an ArrayBuffer so TypeScript accepts it (the DOM lib
  // types reject Uint8Array<ArrayBufferLike> because it might be a
  // SharedArrayBuffer — which it never is for our base64 decode).
  const keyBuffer = new ArrayBuffer(keyBytes.length);
  new Uint8Array(keyBuffer).set(keyBytes);

  let sub: PushSubscription;
  try {
    // Re-use an existing subscription if one is present for this SW
    // scope; PushManager.subscribe is idempotent per application
    // server key, but re-subscribing after a key change fails with a
    // generic "Push service error" in Chrome. Unsubscribe first when
    // the existing key doesn't match.
    const existing = await reg.pushManager.getSubscription();
    if (existing) {
      const existingKey = existing.options.applicationServerKey;
      const matches =
        existingKey && bufferEqualsBytes(existingKey as ArrayBuffer, keyBytes);
      if (!matches) {
        await existing.unsubscribe();
      }
    }
    sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: keyBuffer,
    });
  } catch (err) {
    // Chrome throws "AbortError: Push service error" when it can't
    // reach the browser's push service (usually a network / Google
    // reachability issue, sometimes an invalid VAPID key). Surface the
    // DOMException name so users have something to search for.
    const dom = err as DOMException;
    throw new Error(
      `Push subscription failed: ${dom.name || 'Error'} — ${dom.message || 'unknown'}.\n` +
      `Check that your browser can reach https://fcm.googleapis.com and that the VAPID ` +
      `key on the server matches the one in this browser (try "Clear site data" if it doesn't).`,
    );
  }

  const subJSON = sub.toJSON();
  const p256dh = subJSON.keys?.p256dh;
  const auth = subJSON.keys?.auth;
  if (!subJSON.endpoint || !p256dh || !auth) {
    throw new Error('Push subscription missing keys.');
  }

  return registerDevice({
    platform: 'web',
    endpoint: subJSON.endpoint,
    p256dh,
    auth_secret: auth,
    label: label ?? inferLabel(),
  });
}

export async function disableWebPush(): Promise<void> {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) return;
  const reg = await navigator.serviceWorker.getRegistration('/sw.js');
  if (!reg) return;
  const sub = await reg.pushManager.getSubscription();
  if (sub) await sub.unsubscribe();
}

// ---- helpers -----------------------------------------------------------

function urlBase64ToUint8Array(b64: string): Uint8Array {
  const padding = '='.repeat((4 - (b64.length % 4)) % 4);
  const base64 = (b64 + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

function bufferEqualsBytes(buf: ArrayBuffer, bytes: Uint8Array): boolean {
  if (buf.byteLength !== bytes.length) return false;
  const view = new Uint8Array(buf);
  for (let i = 0; i < bytes.length; i++) {
    if (view[i] !== bytes[i]) return false;
  }
  return true;
}

function inferLabel(): string {
  const ua = navigator.userAgent || '';
  if (/chrome/i.test(ua) && !/edge|opr/i.test(ua)) return 'Chrome';
  if (/firefox/i.test(ua)) return 'Firefox';
  if (/safari/i.test(ua) && !/chrome/i.test(ua)) return 'Safari';
  if (/edg/i.test(ua)) return 'Edge';
  return 'Browser';
}
