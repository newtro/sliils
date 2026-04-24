// SliilS web push service worker (M11).
//
// Receives opaque push envelopes ({ msg_id, type, tenant_url }) and
// renders a browser notification. The actual message body is NEVER in
// the push payload — clicking the notification routes the user to the
// tenant app URL where their signed-in browser fetches the real content.

self.addEventListener('install', (event) => {
  // Activate immediately so new SW versions don't need a page refresh.
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    // Non-JSON payload — still show something so the user knows.
    data = { type: 'unknown' };
  }
  const title = titleFor(data);
  const body = bodyFor(data);

  const notification = self.registration.showNotification(title, {
    body,
    icon: '/favicon.png',
    badge: '/favicon.png',
    tag: data.msg_id || 'sliils',
    renotify: !!data.msg_id,
    data: {
      msg_id: data.msg_id,
      type: data.type,
      tenant_url: data.tenant_url,
    },
  });
  event.waitUntil(notification);
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const target = (event.notification.data && event.notification.data.tenant_url) || '/';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientsArr) => {
      for (const client of clientsArr) {
        if (client.url.includes(new URL(target, self.location.href).origin)) {
          return client.focus();
        }
      }
      return self.clients.openWindow(target);
    }),
  );
});

function titleFor(data) {
  switch (data.type) {
    case 'mention':
      return 'You were mentioned';
    case 'dm':
      return 'New message';
    case 'call':
      return 'Incoming call';
    case 'event':
      return 'Upcoming event';
    default:
      return 'SliilS';
  }
}

function bodyFor(data) {
  // Signal-style: no plaintext in the payload, so the body is a
  // gentle generic prompt. Clicking the notification opens the app
  // which fetches the actual message with the user's own session.
  switch (data.type) {
    case 'mention':
      return 'Open SliilS to read it.';
    case 'dm':
      return 'Open SliilS to read your DM.';
    case 'call':
      return 'Open SliilS to answer.';
    case 'event':
      return 'Your event is starting soon.';
    default:
      return '';
  }
}
