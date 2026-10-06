// app.js — Vitals shell: theme, navigation, the connection badge, the one
// vitals.subscribe and the three pages. The backend is reached only through
// ./shared/native.js (docs/ARCHITECTURE.md §4); the same page runs against
// the Go backend (Windows, Linux tests) and the Swift backend (macOS).
import { native } from './shared/native.js';
import { $, tabs, segmented, toast, setTheme, applyStoredTheme } from './shared/ui.js';
import { errorCard } from './common.js';
import { Overview } from './overview.js';
import { Processes } from './processes.js';
import { Power } from './power.js';

applyStoredTheme();

const SAMPLE_INTERVAL_MS = 1000;

/** Test hook and debugging aid: window.__vitals. */
const vt = {
  ready: false,
  info: null,
  samples: 0,
  lastSample: null,
  errors: [],
  subscribed: false,
  call: (method, params) => native.call(method, params),
  on: (event, fn) => native.on(event, fn),
  setTheme,
  pages: {},
  get charts() { return vt.pages.overview?.charts; },
};
window.__vitals = vt;

// ---------------------------------------------------------------- shell

const conn = $('#conn');
function setConn(kind, text) {
  conn.className = `badge badge-dot ml-auto badge-${kind}`;
  conn.textContent = text;
}

const call = vt.call;
const overview = new Overview($('#panel-overview'));
const processes = new Processes($('#panel-processes'), { call });
const power = new Power($('#panel-power'), { call, on: vt.on });
vt.pages = { overview, processes, power };

const nav = tabs('#nav', {
  onChange(id) {
    for (const [name, page] of Object.entries(vt.pages)) {
      if (name === id) page.show(); else page.hide();
    }
    vt.tab = id;
  },
});
vt.tab = 'overview';
vt.selectTab = (id) => nav.select(id);
overview.show();

const themeSwitch = segmented('#theme-switch', { onChange: (v) => setTheme(v) });
themeSwitch.select(applyStoredTheme(), { silent: true });

// Ctrl/Cmd+1..3 switch pages like a native tabbed window.
document.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
  const idx = { 1: 0, 2: 1, 3: 2 }[e.key];
  if (idx == null) return;
  e.preventDefault();
  nav.select(idx);
});

// --------------------------------------------------------------- bridge

native.on('vitals.sample', (s) => {
  vt.samples++;
  vt.lastSample = s;
  overview.update(s);
  power.sample(s);
});

native.on('native.connected', () => {
  setConn('success', 'Live');
  if (vt.subscribed) subscribe(); // the backend forgot us when the socket dropped
});
native.on('native.disconnected', () => setConn('danger', 'Disconnected'));

async function subscribe() {
  try {
    await call('vitals.subscribe', { intervalMs: SAMPLE_INTERVAL_MS });
    vt.subscribed = true;
  } catch (e) {
    vt.errors.push(e);
    if (e.code !== 'disconnected') toast(`Live updates unavailable: ${e.message}`, { kind: 'warning', timeout: 6000 });
  }
}

async function init() {
  try {
    const info = await call('app.info');
    vt.info = info;
    overview.setInfo(info);
    power.setInfo(info);
    if (info.machine?.hostname) document.title = `Vitals — ${info.machine.hostname}`;
    setConn('success', 'Live');
  } catch (e) {
    vt.errors.push(e);
    setConn('danger', 'No backend');
    $('#panel-overview').prepend(errorCard(e, { title: 'Cannot reach the Vitals backend', action: 'Retry', onAction: () => location.reload() }));
    return;
  }
  await subscribe();
  vt.ready = true;
}

window.addEventListener('pagehide', () => {
  if (vt.subscribed && native.transport === 'websocket') return; // the socket closing unsubscribes server-side
  if (vt.subscribed) call('vitals.unsubscribe').catch(() => {});
});

init();
