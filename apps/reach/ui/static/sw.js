// Reach service worker: the static shell is served cache-first (and refreshed
// in the background); /api/ and /ws are never touched — connections are live.
const CACHE = 'reach-shell-v1';
const SHELL = [
  './', './index.html', './app.css', './app.js', './core.js', './qr.js',
  './laptops.js', './remote.js', './apps.js', './screen.js', './power.js', './settings.js',
  './manifest.webmanifest', './icon-192.png', './icon-512.png',
  './shared/base.css', './shared/ui.js', './shared/native.js',
];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/api/') || url.pathname === '/ws') return;
  event.respondWith(caches.open(CACHE).then(async (cache) => {
    const cached = await cache.match(req, { ignoreSearch: true });
    const refresh = fetch(req).then((res) => {
      if (res && res.ok) cache.put(req, res.clone());
      return res;
    }).catch(() => null);
    if (cached) { refresh.catch(() => {}); return cached; }
    const fresh = await refresh;
    return fresh || new Response('offline', { status: 503, headers: { 'content-type': 'text/plain' } });
  }));
});
