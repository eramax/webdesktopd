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
  const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  const desktopClient = clients.find((client) => {
    try {
      return new URL(client.url).pathname === '/desktop';
    } catch {
      return false;
    }
  });

  if (!desktopClient) {
    return new Response('No desktop window found. Open webdesktopd first.', { status: 503 });
  }

  const method = request.method;
  let bodyBytes = null;
  if (method !== 'GET' && method !== 'HEAD') {
    const buf = await request.arrayBuffer();
    if (buf.byteLength > 0) bodyBytes = new Uint8Array(buf);
  }

  // Build raw HTTP/1.1 request bytes
  const enc = new TextEncoder();
  const reqLines = [
    `${method} ${path} HTTP/1.1`,
    `Host: 127.0.0.1:${port}`,
    `Connection: close`,
  ];
  for (const [k, v] of request.headers.entries()) {
    // Skip hop-by-hop headers
    if (['connection', 'upgrade', 'keep-alive'].includes(k.toLowerCase())) continue;
    reqLines.push(`${k}: ${v}`);
  }
  if (bodyBytes) reqLines.push(`Content-Length: ${bodyBytes.length}`);
  const headerStr = reqLines.join('\r\n') + '\r\n\r\n';
  const headerBytes = enc.encode(headerStr);

  let requestBytes;
  if (bodyBytes) {
    requestBytes = new Uint8Array(headerBytes.length + bodyBytes.length);
    requestBytes.set(headerBytes);
    requestBytes.set(bodyBytes, headerBytes.length);
  } else {
    requestBytes = headerBytes;
  }

  const { port1, port2 } = new MessageChannel();

  return new Promise((resolve) => {
    const timeout = setTimeout(() => {
      resolve(new Response('Proxy timeout', { status: 504 }));
    }, 30000);

    port1.onmessage = (e) => {
      clearTimeout(timeout);
      if (e.data.error) {
        resolve(new Response(e.data.error, { status: 502 }));
        return;
      }
      const { status, headers, body } = e.data;
      resolve(new Response(body, { status, headers: new Headers(headers) }));
    };

    // Send the request to the desktop window that owns the WebSocket bridge.
    desktopClient.postMessage(
      { type: 'proxy-http-request', port, requestBytes },
      [port2]
    );
  });
}
