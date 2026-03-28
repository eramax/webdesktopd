/**
 * Service worker for webdesktopd port proxy.
 * Intercepts fetch requests to /_proxy/{port}/{path} and tunnels them
 * through the WS connection held by the main desktop window.
 */

const PROXY_RE = /^\/_proxy\/(\d+)(\/.*)?$/;

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  const m = url.pathname.match(PROXY_RE);
  if (!m) return;

  const port = m[1];
  const rest = (m[2] || '/') + (url.search || '');

  event.respondWith(handleProxyRequest(event.request, port, rest));
});

async function handleProxyRequest(request, port, path) {
  // Keep the service worker transparent: let the server-side reverse proxy
  // handle the mount path for any app. This preserves direct refreshes and
  // external navigation without depending on a desktop bridge window.
  return fetch(request);
}
