// core.js — shared state of the Reach app: the laptops the phone knows, the
// live connection to one of them (or to the local agent on its own settings
// page), the bridge call wrapper and small helpers used by every page.
import { native } from './shared/native.js';
import { h, toast } from './shared/ui.js';

// ------------------------------------------------------------------ mode

const pageURL = new URL(location.href);
const LOOPBACK = ['127.0.0.1', 'localhost', '::1', '[::1]'].includes(pageURL.hostname);
const urlToken = pageURL.searchParams.get('token');
if (urlToken && LOOPBACK) { try { localStorage.setItem('native.token', urlToken); } catch { /* private mode */ } }
const storedToken = (() => { try { return localStorage.getItem('native.token'); } catch { return null; } })();
const WK = !!window.webkit?.messageHandlers?.native;

/** True when this page is the agent's own settings UI (WKWebView, or loopback with the local token). */
export const LOCAL = WK || (LOOPBACK && !!(urlToken || storedToken));

// ------------------------------------------------------------------ storage

function read(key, fallback) {
  try { const v = localStorage.getItem(key); return v == null ? fallback : JSON.parse(v); } catch { return fallback; }
}
function write(key, value) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* ignore */ }
}

/** Laptops known to this phone: [{id, name, host, port, token, platform}]. */
export const store = {
  list() { return read('reach.laptops', []); },
  save(list) { write('reach.laptops', list); emit('laptops', list); },
  get(id) { return this.list().find((l) => l.id === id) || null; },
  find(host, port) { return this.list().find((l) => l.host === host && l.port === port) || null; },
  add(entry) {
    const list = this.list();
    const existing = list.find((l) => l.host === entry.host && l.port === entry.port);
    if (existing) { Object.assign(existing, entry, { token: entry.token || existing.token }); this.save(list); return existing; }
    const item = { id: entry.id || `${entry.host}:${entry.port}`, name: entry.name || entry.host, host: entry.host, port: entry.port, token: entry.token || '', platform: entry.platform || '' };
    list.push(item);
    this.save(list);
    return item;
  },
  update(id, patch) {
    const list = this.list();
    const item = list.find((l) => l.id === id);
    if (!item) return null;
    Object.assign(item, patch);
    this.save(list);
    return item;
  },
  remove(id) { this.save(this.list().filter((l) => l.id !== id)); },
};

/** The phone's name shown to laptops when pairing. */
export function deviceName() {
  let name = read('reach.device', '');
  if (!name) {
    const ua = navigator.userAgent;
    name = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android phone' : /Mac/.test(ua) ? 'Mac browser' : /Windows/.test(ua) ? 'Windows browser' : 'Phone';
    write('reach.device', name);
  }
  return name;
}
export function setDeviceName(name) { write('reach.device', name.trim() || 'Phone'); }

/** Parse "host", "host:port", "[v6]:port" or a full URL into {host, port}. */
export function parseAddress(text) {
  let s = text.trim();
  if (!s) return null;
  if (!/^[a-z]+:\/\//i.test(s)) s = 'http://' + s;
  try {
    const u = new URL(s);
    const host = u.hostname.replace(/^\[|\]$/g, '');
    if (!host) return null;
    return { host, port: u.port ? Number(u.port) : 7788 };
  } catch { return null; }
}

export function baseURL(laptop) {
  const host = laptop.host.includes(':') ? `[${laptop.host}]` : laptop.host;
  return `http://${host}:${laptop.port}`;
}
export function wsURL(laptop) {
  const host = laptop.host.includes(':') ? `[${laptop.host}]` : laptop.host;
  return `ws://${host}:${laptop.port}/ws?token=${encodeURIComponent(laptop.token)}`;
}

/** GET /api/… of a laptop with a timeout. Resolves to parsed JSON or throws. */
export async function api(laptop, path, { method = 'GET', body, timeout = 4000 } = {}) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeout);
  try {
    const res = await fetch(baseURL(laptop) + path, {
      method, signal: ctrl.signal, mode: 'cors', cache: 'no-store',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let json = null;
    try { json = text ? JSON.parse(text) : null; } catch { json = null; }
    if (!res.ok) { const e = new Error(json?.error || `HTTP ${res.status}`); e.status = res.status; e.body = json; throw e; }
    return json;
  } finally { clearTimeout(timer); }
}

// ------------------------------------------------------------------ events

const listeners = new Map();
export function on(event, fn) {
  if (!listeners.has(event)) listeners.set(event, new Set());
  listeners.get(event).add(fn);
  return () => listeners.get(event)?.delete(fn);
}
function emit(event, data) {
  for (const fn of [...(listeners.get(event) || [])]) { try { fn(data); } catch (e) { console.error(e); } }
}

// ------------------------------------------------------------------ connection

/**
 * conn.status: 'idle' (nothing selected) | 'connecting' | 'online' | 'offline'.
 * conn.laptop: the selected laptop (phone mode) or {name} of the local agent.
 * conn.info: the last `info` event of the agent.
 */
export const conn = {
  status: LOCAL ? 'connecting' : 'idle',
  laptop: LOCAL ? { id: 'local', name: 'This computer', local: true } : null,
  info: null,
  failures: 0,
  everConnected: false,
  connect(laptop) {
    if (!laptop?.token) return;
    this.laptop = laptop;
    this.failures = 0;
    this.everConnected = false;
    this.info = null;
    setStatus('connecting');
    write('reach.current', laptop.id);
    native.connect(wsURL(laptop));
  },
  disconnect() {
    if (LOCAL) return;
    const t = native.connect('ws://invalid.invalid/'); // replaces the socket; closed right away
    t.close?.();
    this.laptop = null;
    this.info = null;
    write('reach.current', null);
    setStatus('idle');
  },
  get platform() { return this.info?.platform || this.laptop?.platform || ''; },
  get isMac() { return this.platform === 'macos'; },
  get currentId() { return read('reach.current', null); },
};

function setStatus(s) {
  if (conn.status === s) return;
  conn.status = s;
  emit('status', s);
}

native.on('native.connected', () => { conn.failures = 0; conn.everConnected = true; setStatus('online'); });
native.on('native.disconnected', () => {
  if (conn.status === 'idle') return;
  conn.failures++;
  setStatus(conn.everConnected || conn.failures < 2 ? 'connecting' : 'offline');
  if (conn.failures >= 3 && !conn.everConnected) setStatus('offline');
});
native.on('info', (info) => {
  conn.info = info;
  if (conn.laptop && !conn.laptop.local) {
    store.update(conn.laptop.id, { name: info.name, platform: info.platform, agentId: info.id });
    conn.laptop.name = info.name;
    conn.laptop.platform = info.platform;
  }
  emit('info', info);
});
native.on('*', (name, data) => emit('event:' + name, data));

/** Whether calls can be made right now. */
export function connected() { return conn.status === 'online'; }

/** Call a method on the connected agent. Rejects with .code; needs_permission is also surfaced as a notice. */
export async function call(method, params = {}) {
  if (!LOCAL && !conn.laptop) { const e = new Error('Pick a laptop first'); e.code = 'not_connected'; throw e; }
  try {
    return await native.call(method, params);
  } catch (e) {
    if (e?.code === 'needs_permission') emit('permission', { permission: e.details?.permission || 'accessibility', method, message: e.message });
    throw e;
  }
}

/** Call and report failures with a toast; resolves undefined on failure. */
export async function tryCall(method, params, { quiet = false } = {}) {
  try { return await call(method, params); } catch (e) {
    if (!quiet && e.code !== 'needs_permission') toast(describeError(e), { kind: e.code === 'disconnected' || e.code === 'not_connected' ? 'warning' : 'danger' });
    return undefined;
  }
}

export function describeError(e) {
  switch (e?.code) {
    case 'not_connected': return 'Not connected to a laptop';
    case 'disconnected': return 'Lost the connection to the laptop';
    case 'not_supported': return e.message || 'Not supported on this laptop';
    case 'not_authorized': return 'Not allowed';
    default: return e?.message || 'Something went wrong';
  }
}

// ------------------------------------------------------------------ helpers

/** Inline SVG icon from a path (24-unit viewBox, stroked). */
export function svg(d, { size = 20, label } = {}) {
  const ns = 'http://www.w3.org/2000/svg';
  const el = document.createElementNS(ns, 'svg');
  el.setAttribute('viewBox', '0 0 24 24');
  el.setAttribute('width', String(size));
  el.setAttribute('height', String(size));
  el.setAttribute('fill', 'none');
  el.setAttribute('stroke', 'currentColor');
  el.setAttribute('stroke-width', '1.8');
  el.setAttribute('stroke-linecap', 'round');
  el.setAttribute('stroke-linejoin', 'round');
  if (label) { el.setAttribute('role', 'img'); el.setAttribute('aria-label', label); } else el.setAttribute('aria-hidden', 'true');
  for (const part of Array.isArray(d) ? d : [d]) {
    const p = document.createElementNS(ns, 'path');
    p.setAttribute('d', part);
    el.append(p);
  }
  return el;
}

export const ICONS = {
  laptop: 'M4 6.5A1.5 1.5 0 015.5 5h13A1.5 1.5 0 0120 6.5V16H4V6.5zM2 19h20',
  remote: ['M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1z', 'M3 15h18', 'M12 15v4'],
  apps: 'M4 4h6v6H4V4zm10 0h6v6h-6V4zM4 14h6v6H4v-6zm10 0h6v6h-6v-6z',
  screen: ['M3 5h18v12H3V5z', 'M8 21h8M12 17v4'],
  power: 'M12 3v9m6.36-5.36a9 9 0 11-12.72 0',
  settings: ['M12 15.5a3.5 3.5 0 100-7 3.5 3.5 0 000 7z', 'M19.4 15a1.7 1.7 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.8-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 11-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.8 1.7 1.7 0 00-1.5-1H3a2 2 0 110-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.8.3H9a1.7 1.7 0 001-1.5V3a2 2 0 114 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.8V9a1.7 1.7 0 001.5 1H21a2 2 0 110 4h-.1a1.7 1.7 0 00-1.5 1z'],
  keyboard: ['M3 7h18v10H3V7z', 'M7 10h.01M11 10h.01M15 10h.01M7 14h10'],
  prev: 'M19 5v14L8 12l11-7zM5 5v14',
  play: 'M7 4l12 8-12 8V4z',
  next: 'M5 5v14l11-7L5 5zm14 0v14',
  volDown: ['M4 10v4h4l5 4V6l-5 4H4z', 'M16 10v4'],
  volUp: ['M4 10v4h4l5 4V6l-5 4H4z', 'M16 9a4 4 0 010 6M18.5 6.5a8 8 0 010 11'],
  mute: ['M4 10v4h4l5 4V6l-5 4H4z', 'M16 9l5 6M21 9l-5 6'],
  lock: ['M6 11h12v9H6v-9z', 'M8 11V8a4 4 0 018 0v3'],
  moon: 'M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z',
  display: ['M3 5h18v12H3V5z', 'M8 21h8', 'M12 17v4'],
  restart: 'M20 12a8 8 0 11-2.34-5.66M20 4v5h-5',
  x: 'M6 6l12 12M18 6L6 18',
  plus: 'M12 5v14M5 12h14',
  check: 'M5 12.5l4.5 4.5L19 7.5',
  refresh: 'M20 12a8 8 0 11-2.34-5.66M20 4v5h-5',
  arrowUp: 'M12 19V5m-6 6l6-6 6 6',
  arrowDown: 'M12 5v14m6-6l-6 6-6-6',
  arrowLeft: 'M19 12H5m6 6l-6-6 6-6',
  arrowRight: 'M5 12h14m-6-6l6 6-6 6',
  backspace: ['M9 5h11a1 1 0 011 1v12a1 1 0 01-1 1H9l-6-7 6-7z', 'M12 9l5 5m0-5l-5 5'],
  enter: 'M20 6v6a2 2 0 01-2 2H6m3-4l-4 4 4 4',
  wifi: ['M5 12.5a10 10 0 0114 0', 'M8.5 16a5 5 0 017 0', 'M12 19.5h.01', 'M2 9a14 14 0 0120 0'],
};

export function spinner() { return h('span.badge.badge-outline', {}, '…'); }

/** Keep the screen on while fn's page is visible (best effort; needs a secure context). */
export function wakeLock() {
  let lock = null, active = false;
  const request = async () => {
    if (!active || lock || !navigator.wakeLock || document.visibilityState !== 'visible') return;
    try { lock = await navigator.wakeLock.request('screen'); lock.addEventListener('release', () => { lock = null; }); } catch { lock = null; }
  };
  const onVis = () => { if (document.visibilityState === 'visible') request(); };
  return {
    acquire() { active = true; document.addEventListener('visibilitychange', onVis); request(); },
    release() { active = false; document.removeEventListener('visibilitychange', onVis); try { lock?.release(); } catch { /* ignore */ } lock = null; },
  };
}

export const PERMISSION_LABELS = { accessibility: 'Accessibility', inputMonitoring: 'Input Monitoring', screenRecording: 'Screen Recording' };
