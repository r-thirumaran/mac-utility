// apps.js — installed apps grid (tap to launch) and the running row.
import { conn, call, tryCall, svg, ICONS, on, LOCAL } from './core.js';
import { h, mount, toast, confirmDialog } from './shared/ui.js';

let root = null;
let gridEl = null;
let runningEl = null;
let searchEl = null;
let apps = [];
let running = [];
const memCache = new Map();

function cacheKey() { return 'reach.apps.' + (conn.laptop?.agentId || conn.laptop?.id || 'local'); }

function readCache() {
  const key = cacheKey();
  if (memCache.has(key)) return memCache.get(key);
  try { const v = JSON.parse(localStorage.getItem(key) || 'null'); if (Array.isArray(v)) { memCache.set(key, v); return v; } } catch { /* ignore */ }
  return null;
}
function writeCache(list) {
  const key = cacheKey();
  memCache.set(key, list);
  try { localStorage.setItem(key, JSON.stringify(list)); } catch { /* quota */ }
}

function tile(a) {
  const iconNode = a.iconB64 ? h('img', { src: `data:image/png;base64,${a.iconB64}`, alt: '', loading: 'lazy', width: 56, height: 56 }) : (a.name[0] || '?').toUpperCase();
  return h('button.app-tile', { type: 'button', dataset: { app: a.id }, title: a.name, onclick: async () => {
    const r = await tryCall('apps.launch', { id: a.id });
    if (r !== undefined) toast(`Launching ${a.name}`, { kind: 'success', timeout: 1800 });
  } }, h('div.app-icon', {}, iconNode), h('div.app-name', {}, a.name));
}

function renderGrid() {
  if (!gridEl) return;
  const q = (searchEl?.value || '').trim().toLowerCase();
  const list = q ? apps.filter((a) => a.name.toLowerCase().includes(q)) : apps;
  mount(gridEl, list.length ? list.map(tile) : [h('div.empty-state', {}, h('div.empty-state-title', {}, apps.length ? 'No match' : 'No apps yet'), h('div.empty-state-text', {}, apps.length ? '' : 'The list arrives once the laptop is connected.'))]);
}

function renderRunning() {
  if (!runningEl) return;
  mount(runningEl, running.length ? running.map((r) => h('div.running-chip', { class: r.active ? 'is-active' : '', dataset: { pid: r.pid } },
    h('button', { type: 'button', title: `Focus ${r.name}`, onclick: () => tryCall('apps.focus', { pid: r.pid, id: r.id }) }, h('span.running-dot'), r.name),
    h('button.btn.btn-ghost.btn-sm.btn-icon', { type: 'button', 'aria-label': `Quit ${r.name}`, onclick: async () => {
      if (await confirmDialog({ title: `Quit ${r.name}?`, body: 'Unsaved work in that app may be lost.', okLabel: 'Quit', danger: true })) {
        if (await tryCall('apps.quit', { pid: r.pid }) !== undefined) loadRunning();
      }
    } }, svg(ICONS.x, { size: 14 })))) : [h('span.text-sm.text-muted', {}, 'Nothing running that has a window.')]);
}

async function loadApps() {
  const cached = readCache();
  if (cached && !apps.length) { apps = cached; renderGrid(); }
  try {
    const list = await call('apps.list', { icons: true });
    apps = list;
    writeCache(list);
    renderGrid();
  } catch (e) {
    if (!cached) renderGrid();
    if (e.code !== 'disconnected' && e.code !== 'not_connected') toast(`Could not list apps: ${e.message}`, { kind: 'danger' });
  }
}

async function loadRunning() {
  const list = await tryCall('apps.running', {}, { quiet: true });
  if (Array.isArray(list)) { running = list; renderRunning(); }
}

function render() {
  if (!root) return;
  searchEl = h('input.input', { type: 'search', placeholder: 'Search apps', 'aria-label': 'Search apps', id: 'apps-search', oninput: renderGrid });
  gridEl = h('div.app-grid', { id: 'app-grid' });
  runningEl = h('div.running-row', { id: 'running-row' });
  mount(root, [
    h('div.page-head', {}, h('h1.page-title', {}, 'Apps'), h('button.btn.btn-ghost.btn-sm', { type: 'button', 'aria-label': 'Refresh', onclick: () => { loadApps(); loadRunning(); } }, svg(ICONS.refresh, { size: 16 }), 'Refresh')),
    h('div.stack.gap-2', {}, h('div.section-title', {}, 'Running'), runningEl),
    h('div.stack.gap-2', {}, h('div.section-title', {}, 'Installed'), searchEl, gridEl),
  ]);
  if (!LOCAL && !conn.laptop) {
    mount(gridEl, h('div.empty-state', {}, h('div.empty-state-title', {}, 'No laptop selected'), h('a.btn.btn-primary', { href: '#laptops' }, 'Go to Laptops')));
    return;
  }
  renderGrid();
  renderRunning();
  if (conn.status === 'online') { loadApps(); loadRunning(); }
}

on('status', (s) => { if (root && s === 'online') { loadApps(); loadRunning(); } });
on('event:apps.running', (list) => { if (Array.isArray(list)) { running = list; renderRunning(); } });

export default {
  id: 'apps', title: 'Apps', icon: ICONS.apps,
  show(container) { root = container; render(); },
  hide() { root = null; gridEl = null; runningEl = null; },
};
