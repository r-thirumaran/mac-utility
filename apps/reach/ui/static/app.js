// app.js — bootstrap: tabs, connection badge, permission notices, startup.
import { LOCAL, WRAPPED, conn, store, api, call, on, svg, PERMISSION_LABELS } from './core.js';
import { h, mount, applyStoredTheme, toast } from './shared/ui.js';
import laptops from './laptops.js';
import remote from './remote.js';
import apps from './apps.js';
import screen, { frameCount } from './screen.js';
import power from './power.js';
import settings from './settings.js';

applyStoredTheme();

const pages = [laptops, remote, apps, screen, power, settings];
const view = document.getElementById('view');
const tabbar = document.getElementById('tabbar');
const badge = document.getElementById('conn-badge');
const notices = document.getElementById('notices');
let current = null;
let lastPairCode = null;

// ------------------------------------------------------------------ tabs

mount(tabbar, pages.map((p) => h('button', { type: 'button', dataset: { tab: p.id }, 'aria-label': p.title, onclick: () => { location.hash = '#' + p.id; } }, svg(p.icon, { size: 22 }), h('span', {}, p.title))));

function route() {
  const id = location.hash.replace(/^#/, '') || defaultPage();
  const page = pages.find((p) => p.id === id) || pages[0];
  if (current === page) return;
  current?.hide();
  current = page;
  view.replaceChildren();
  for (const b of tabbar.querySelectorAll('button')) {
    if (b.dataset.tab === page.id) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
  }
  page.show(view);
  window.scrollTo(0, 0);
}

function defaultPage() {
  if (LOCAL) return 'settings';
  return store.list().some((l) => l.token) ? 'remote' : 'laptops';
}

window.addEventListener('hashchange', route);

// ------------------------------------------------------------------ badge

function renderBadge() {
  const name = conn.laptop?.name || '';
  let text, cls;
  switch (conn.status) {
    case 'online': text = name || 'Connected'; cls = 'is-online'; break;
    case 'connecting': text = name ? `Connecting to ${name}…` : 'Connecting…'; cls = 'is-connecting'; break;
    case 'offline': text = name ? `${name}: offline` : 'Offline'; cls = 'is-offline'; break;
    default: text = 'Not connected'; cls = '';
  }
  badge.textContent = text;
  badge.className = `conn-badge badge badge-dot ${cls}`;
  badge.title = conn.status === 'offline' ? 'Tap to pick another laptop or pair again' : text;
}
badge.addEventListener('click', () => { location.hash = '#laptops'; });
on('status', renderBadge);
on('info', renderBadge);
on('laptops', renderBadge);
renderBadge();

let offlineToast = null;
on('status', (s) => {
  if (s === 'offline' && !conn.everConnected && conn.laptop && !conn.laptop.local) {
    offlineToast?.dismiss();
    offlineToast = toast(`${conn.laptop.name} refuses the saved pairing or is unreachable. Pair again from the Laptops tab if it keeps failing.`, { kind: 'warning', timeout: 8000 });
  }
});

// ------------------------------------------------------------------ notices

const shownPermissions = new Set();
on('permission', ({ permission }) => {
  if (shownPermissions.has(permission)) return;
  shownPermissions.add(permission);
  const label = PERMISSION_LABELS[permission] || permission;
  const laptop = conn.laptop?.name || 'the laptop';
  const card = h('div.card.notice.notice-permission', { role: 'alert', dataset: { permission } },
    h('div.grow', {}, h('div.weight-600', {}, `${label} permission needed on ${laptop}`),
      h('div.text-sm', {}, `Reach cannot do that until ${label} is allowed for it: on ${laptop} open System Settings → Privacy & Security → ${label} and switch Reach on, then try again.`)),
    h('button.btn.btn-ghost.btn-sm', { type: 'button', onclick: () => { card.remove(); shownPermissions.delete(permission); } }, 'Dismiss'));
  notices.append(card);
});

on('event:reach.pairCode', (d) => { lastPairCode = d?.code || null; });

// ------------------------------------------------------------------ startup

async function autoAddOrigin() {
  if (LOCAL || WRAPPED || location.protocol === 'file:') return;
  const host = location.hostname.replace(/^\[|\]$/g, '');
  const port = Number(location.port) || (location.protocol === 'https:' ? 443 : 80);
  if (!host) return;
  try {
    const info = await api({ host, port }, '/api/info', { timeout: 3000 });
    if (!info?.id) return;
    const known = store.list().find((l) => l.agentId === info.id);
    if (known) { if (known.host !== host || known.port !== port) store.update(known.id, { host, port }); return; }
    store.add({ id: info.id, agentId: info.id, host, port, name: info.name, platform: info.platform });
  } catch { /* not served by an agent (Capacitor, static hosting) */ }
}

async function start() {
  if (!LOCAL) {
    await autoAddOrigin();
    const list = store.list();
    const chosen = list.find((l) => l.id === conn.currentId && l.token) || list.find((l) => l.token);
    if (chosen) conn.connect(chosen);
  }
  route();
}

if ('serviceWorker' in navigator && window.isSecureContext && !WRAPPED) {
  navigator.serviceWorker.register('./sw.js').catch(() => {});
}
// Inside the Capacitor app only: haptics and the Android back button.
if (WRAPPED) import('./capacitor.js').then((m) => m.init()).catch((e) => console.warn('native layer unavailable', e));

// Test and debugging hooks (read-only).
window.__reach = {
  get mode() { return LOCAL ? 'local' : 'phone'; },
  get status() { return conn.status; },
  get page() { return current?.id || null; },
  get lastPairCode() { return lastPairCode; },
  get frames() { return frameCount(); },
  get laptops() { return store.list(); },
  get info() { return conn.info; },
  call: (method, params) => call(method, params),
};

start();
