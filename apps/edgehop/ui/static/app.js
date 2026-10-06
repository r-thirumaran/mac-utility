// app.js — Edgehop's UI: Machines, Layout and Settings pages, the status
// strip and the log panel, all driven by edgehop.state events from the
// backend (docs/ARCHITECTURE.md §5).
import { native } from './shared/native.js';
import { h, $, tabs, toast, confirmDialog, sliderFill, debounce, applyStoredTheme } from './shared/ui.js';
import { LayoutEditor, screenGlyph } from './layout.js';
import { PairDialog, CodeDialog, platName } from './pairing.js';

applyStoredTheme();

const app = {
  state: null,
  settings: null,
  info: null,
  ready: false,
  logs: [],
  page: 'machines',
};
window.__edgehop = app; // read by the Playwright test

const pairDialog = new PairDialog();
const codeDialog = new CodeDialog();
const shortId = (id) => (id || '').slice(0, 8);
const PERMISSION_LABELS = { accessibility: 'Accessibility', inputMonitoring: 'Input Monitoring', screenRecording: 'Screen Recording' };

// ------------------------------------------------------------------ nav

const nav = tabs('#nav', { onChange: (id) => { app.page = id; } });

// --------------------------------------------------------------- status

function activeMachine(st) {
  if (st.self.active) return st.self;
  return st.peers.find((p) => p.id === st.active) || null;
}

function renderStatus() {
  const st = app.state;
  const strip = $('#status-strip');
  if (!st) return;
  const act = activeMachine(st);
  const items = [];
  if (st.self.active) {
    items.push(h('span.badge.badge-success.badge-dot', { id: 'status-active' }, st.role === 'receiving' ? 'Receiving input here' : 'Pointer is here'));
  } else {
    items.push(h('span.badge.badge-accent.badge-dot', { id: 'status-active' }, `Controlling ${act?.name || shortId(st.active)}`));
    items.push(h('button.btn.btn-sm', { type: 'button', id: 'status-take-back', onclick: () => call('edgehop.switchTo', { peerId: st.self.id }) }, 'Bring pointer back'));
  }
  for (const [key, val] of Object.entries(st.permissions || {})) {
    const granted = typeof val === 'object' && val ? val.granted !== false : val !== false;
    if (granted) continue;
    items.push(h('span.badge.badge-danger.badge-dot', { dataset: { permission: key } }, `${PERMISSION_LABELS[key] || key} not granted`));
    items.push(h('button.btn.btn-sm.btn-primary', { type: 'button', onclick: () => call('app.openSettingsPane', { pane: key }) }, 'Open Settings'));
  }
  const secure = st.peers.find((p) => p.secureInput);
  if (secure) items.push(h('span.badge.badge-warning.badge-dot', {}, `${secure.name}: secure input, keys blocked`));
  strip.replaceChildren(...items);
}

// ------------------------------------------------------------- machines

function statusBadge(m) {
  if (m.active) return h('span.badge.badge-accent.badge-dot', {}, 'Active');
  if (m.status === 'online') return h('span.badge.badge-success.badge-dot', {}, 'Online');
  if (m.status === 'offline') return h('span.badge.badge-outline.badge-dot', {}, 'Offline');
  return h('span.badge.badge-warning.badge-dot', {}, 'Not paired');
}

function displayText(displays) {
  if (!displays?.length) return 'no displays';
  return displays.map((d) => `${d.w}×${d.h}${d.scale && d.scale !== 1 ? ` @${d.scale}x` : ''}`).join(' + ');
}

function selfCard(st) {
  const s = st.self;
  const addr = st.port ? `port ${st.port}` : '';
  return h('div.card.eh-self', { id: 'self-card' },
    h('div.card-header', {},
      h('div.card-title', {}, 'This machine'),
      s.active ? h('span.badge.badge-success.badge-dot', {}, 'Pointer is here') : h('span.badge.badge-outline.badge-dot', {}, 'Pointer is elsewhere')),
    h('div.eh-machine', {},
      screenGlyph(s, { large: true }),
      h('div.eh-machine-body', {},
        h('div.eh-machine-name', {}, h('span', { id: 'self-name' }, s.name), h('span.badge', {}, platName(s.plat))),
        h('div.eh-machine-meta', {}, [displayText(s.displays), addr, `id ${shortId(s.id)}`, st.version ? `v${st.version}` : null].filter(Boolean).join(' · ')),
        h('div.eh-machine-meta', { id: 'self-hint' }, st.peers.length
          ? 'Other machines pair with this one by its address; the pairing code shows up here.'
          : 'Install Edgehop on another Mac or PC on this network, then pair it from here or from there.'))));
}

function peerCard(st, p) {
  const actions = [];
  if (p.paired && p.status === 'online' && !p.active) {
    actions.push(h('button.btn.btn-sm.btn-primary', { type: 'button', dataset: { action: 'switch' }, onclick: () => call('edgehop.switchTo', { peerId: p.id }) }, 'Send pointer there'));
  }
  if (!p.paired) {
    actions.push(h('button.btn.btn-sm.btn-primary', { type: 'button', dataset: { action: 'pair' }, onclick: () => pairDialog.show({ peerId: p.id, name: p.name }) }, 'Pair…'));
  } else {
    actions.push(h('button.btn.btn-sm.btn-ghost.btn-danger', { type: 'button', dataset: { action: 'unpair' }, onclick: () => unpair(p) }, 'Unpair'));
  }
  const meta = [platName(p.plat), p.addr, p.status === 'online' || p.displays?.length ? displayText(p.displays) : null, p.version ? `v${p.version}` : null].filter(Boolean).join(' · ');
  return h('div.card.eh-peer', { class: [p.active && 'is-active', p.status === 'offline' && 'is-offline'], dataset: { id: p.id, status: p.status, paired: String(p.paired) } },
    h('div.eh-machine', {},
      screenGlyph(p),
      h('div.eh-machine-body', {},
        h('div.eh-machine-name', {}, h('span', {}, p.name || shortId(p.id)), statusBadge(p)),
        h('div.eh-machine-meta', {}, meta),
        h('div.eh-machine-actions', {}, ...actions))));
}

async function unpair(p) {
  const ok = await confirmDialog({ title: `Unpair ${p.name}?`, body: 'This machine will forget it and drop it from the layout. You can pair again any time.', okLabel: 'Unpair', danger: true });
  if (!ok) return;
  await call('edgehop.unpair', { peerId: p.id });
  toast(`Unpaired ${p.name}`);
}

function renderMachines() {
  const st = app.state;
  const page = $('#page-machines');
  if (!st) return;
  const peers = st.peers.slice().sort((a, b) => (Number(b.paired) - Number(a.paired)) || (a.name || '').localeCompare(b.name || ''));
  const header = h('div.row.gap-2.items-center.wrap', {},
    h('h2.section-title.grow', {}, 'Machines'),
    h('button.btn.btn-primary', { type: 'button', id: 'btn-pair-address', onclick: () => pairDialog.show() }, 'Pair by address…'));
  const list = peers.length
    ? h('div.grid.eh-peers', { id: 'peer-list' }, ...peers.map((p) => peerCard(st, p)))
    : h('div.card', { id: 'peer-list' }, h('div.empty-state', {},
      h('div.empty-state-title', {}, 'No machines paired yet'),
      h('div.empty-state-text', {}, 'Machines running Edgehop on this network show up here automatically. If one does not, pair it by its address.'),
      h('button.btn.btn-primary', { type: 'button', onclick: () => pairDialog.show() }, 'Pair by address…')));
  page.replaceChildren(selfCard(st), header, list);
}

// --------------------------------------------------------------- layout

const layoutEditor = new LayoutEditor($('#page-layout'), {
  onSave: (layout) => call('edgehop.layout.set', { layout }),
});

// ------------------------------------------------------------- settings

const HOTKEYS = [['ctrl+alt', 'Ctrl + Alt'], ['ctrl+shift', 'Ctrl + Shift'], ['ctrl+alt+shift', 'Ctrl + Alt + Shift'], ['cmd+alt', '⌘/Win + Alt'], ['cmd+shift', '⌘/Win + Shift']];
let settingsBuilt = false;
const controls = {};

function settingRow(label, desc, control) {
  return h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, label), desc ? h('div.setting-desc', {}, desc) : null), h('div.setting-control', {}, control));
}

function toggle(id, onChange) {
  const input = h('input', { type: 'checkbox', role: 'switch', id, onchange: () => onChange(input.checked) });
  return { input, el: h('label.toggle', {}, input, h('span.toggle-track'), h('span.sr-only', {}, id)) };
}

async function setSetting(patch, revert) {
  try {
    app.settings = await native.call('edgehop.settings.set', patch);
    fillSettings();
  } catch (e) {
    toast(e.message || 'Could not save the setting', { kind: e.code === 'not_supported' ? 'warning' : 'danger' });
    if (revert) revert();
    fillSettings();
  }
}

function buildSettings() {
  const page = $('#page-settings');
  const name = h('input.input.is-wide', { id: 'set-name', maxlength: 64, 'aria-label': 'Machine name' });
  const nameSave = h('button.btn.btn-sm', { type: 'button', id: 'set-name-save', onclick: () => setSetting({ name: name.value.trim() }) }, 'Rename');
  name.addEventListener('keydown', (e) => { if (e.key === 'Enter') nameSave.click(); });

  const delay = h('input.slider', { type: 'range', id: 'set-delay', min: 0, max: 1000, step: 25, 'aria-label': 'Switch delay' });
  const delayValue = h('span.slider-value', { id: 'set-delay-value' });
  const delayFill = sliderFill(delay);
  delay.addEventListener('input', () => { delayValue.textContent = `${delay.value} ms`; });
  delay.addEventListener('change', () => setSetting({ switchDelayMs: +delay.value }));

  const dead = h('input.input', { type: 'number', id: 'set-deadzone', min: 0, max: 500, step: 1, inputmode: 'numeric', 'aria-label': 'Corner dead zone in pixels' });
  dead.addEventListener('change', () => setSetting({ edgeCornerDeadZonePx: Math.max(0, Math.min(500, +dead.value || 0)) }));

  const swap = toggle('set-swap', (v) => setSetting({ swapCmdCtrl: v }, () => { swap.input.checked = !v; }));
  const clip = toggle('set-clipboard', (v) => setSetting({ clipboardSync: v }, () => { clip.input.checked = !v; }));
  const lock = toggle('set-lock', (v) => setSetting({ lockCursorWhileRemote: v }, () => { lock.input.checked = !v; }));
  const login = toggle('set-login', (v) => setSetting({ launchAtLogin: v }, () => { login.input.checked = !v; }));

  const hotkey = h('select', { id: 'set-hotkey', 'aria-label': 'Hotkey modifiers' }, ...HOTKEYS.map(([v, l]) => h('option', { value: v }, l)));
  hotkey.addEventListener('change', () => setSetting({ hotkeySwitch: hotkey.value }));

  const port = h('input.input', { type: 'number', id: 'set-port', min: 0, max: 65535, inputmode: 'numeric', 'aria-label': 'Port' });
  port.addEventListener('change', () => setSetting({ port: Math.max(0, Math.min(65535, +port.value || 0)) }));

  Object.assign(controls, { name, delay, delayValue, delayFill, dead, swap, clip, lock, login, hotkey, port });
  const isWindows = app.info?.platform === 'windows';
  page.replaceChildren(
    h('h2.section-title', {}, 'Settings'),
    h('div.settings.eh-settings', {},
      settingRow('Machine name', 'How this computer appears on the other machines.', h('div.row.gap-2', {}, name, nameSave)),
      settingRow('Switch delay', 'Hold the pointer against the edge this long before control moves. 0 switches the instant the edge is touched.', h('div.row.gap-2', {}, delay, delayValue)),
      settingRow('Corner dead zone', 'Pixels at each end of an edge that never switch, so macOS Hot Corners and window buttons stay reachable.', h('div.row.gap-2', {}, dead, h('span.text-muted.text-sm', {}, 'px'))),
      settingRow('Swap ⌘ and Ctrl', 'When the other machine runs a different OS: ⌘C from a Mac keyboard becomes Ctrl+C on Windows, and Ctrl from a PC keyboard becomes ⌘ on a Mac.', swap.el),
      settingRow('Sync clipboard', 'Copy on one machine, paste on the next: the text clipboard follows the pointer (text only).', clip.el),
      settingRow('Hotkey', h('span', {}, 'Hold these with ', h('span.eh-kbd-row', {}, h('span.kbd', {}, '←'), h('span.kbd', {}, '→'), h('span.kbd', {}, '↑'), h('span.kbd', {}, '↓')), ' to move control to a neighbour, or with ', h('span.kbd', {}, 'Home'), ' to bring the pointer back to the machine you are typing on.'), h('span.select', {}, hotkey)),
      settingRow('Hide the pointer here while it is on another machine', 'Parks and hides this machine’s pointer so a second cursor does not sit on screen.', lock.el),
      settingRow('Launch at login', isWindows ? 'Start Edgehop in the notification area when you sign in.' : 'Available in the Windows and macOS apps.', login.el),
      settingRow('Port', 'TCP port other machines connect to. Takes effect after a restart; 0 picks any free port.', h('div.row.gap-2', {}, port, h('span.hint', { id: 'set-port-now' })))));
  settingsBuilt = true;
}

function fillSettings() {
  const s = app.settings;
  if (!settingsBuilt || !s) return;
  const c = controls;
  if (document.activeElement !== c.name) c.name.value = s.name || '';
  c.delay.value = s.switchDelayMs ?? 0;
  c.delayValue.textContent = `${c.delay.value} ms`;
  c.delayFill.update();
  c.dead.value = s.edgeCornerDeadZonePx ?? 32;
  c.swap.input.checked = !!s.swapCmdCtrl;
  c.clip.input.checked = !!s.clipboardSync;
  c.lock.input.checked = !!s.lockCursorWhileRemote;
  c.login.input.checked = !!s.launchAtLogin;
  c.login.input.disabled = app.info?.platform === 'linux';
  if (![...c.hotkey.options].some((o) => o.value === s.hotkeySwitch)) c.hotkey.append(h('option', { value: s.hotkeySwitch }, s.hotkeySwitch));
  c.hotkey.value = s.hotkeySwitch || 'ctrl+alt';
  c.port.value = s.port ?? 24800;
  $('#set-port-now').textContent = app.state?.port ? `listening on ${app.state.port} now` : '';
}

// ------------------------------------------------------------------ log

const LOG_MAX = 300;
function logLine(e) {
  const kind = { warn: 'badge-warning', error: 'badge-danger', info: 'badge-accent' }[e.level] || '';
  const time = new Date(e.ts || Date.now()).toLocaleTimeString([], { hour12: false });
  return h('div.eh-log-line', {}, h('span.badge', { class: kind }, e.level || 'info'), h('span.eh-log-time', {}, time), h('span.eh-log-msg', {}, e.msg));
}

function renderLog() {
  const list = $('#log-list');
  $('#log-count').textContent = String(app.logs.length);
  list.replaceChildren(...(app.logs.length ? app.logs.map(logLine) : [h('div.eh-log-empty', {}, 'Nothing logged yet.')]));
  list.scrollTop = list.scrollHeight;
}

function addLog(e) {
  app.logs.push(e);
  if (app.logs.length > LOG_MAX) app.logs.splice(0, app.logs.length - LOG_MAX);
  const list = $('#log-list');
  if (app.logs.length === 1) list.replaceChildren();
  list.append(logLine(e));
  while (list.children.length > LOG_MAX) list.firstElementChild.remove();
  $('#log-count').textContent = String(app.logs.length);
  if ($('#log-panel').open) list.scrollTop = list.scrollHeight;
}

$('#log-clear').addEventListener('click', (e) => { e.preventDefault(); e.stopPropagation(); app.logs = []; renderLog(); });
$('#log-panel').addEventListener('toggle', () => { const l = $('#log-list'); l.scrollTop = l.scrollHeight; });

// ---------------------------------------------------------------- wiring

async function call(method, params) {
  try {
    return await native.call(method, params);
  } catch (e) {
    toast(e.message || `${method} failed`, { kind: 'danger' });
    throw e;
  }
}

function applyState(st) {
  app.state = st;
  renderStatus();
  renderMachines();
  layoutEditor.setState(st);
  if (st.pairing) pairDialog.update(st.pairing);
  if (app.settings) {
    app.settings.launchAtLogin = st.launchAtLogin;
    app.settings.name = st.self.name;
    fillSettings();
  }
  document.title = st.self.active ? 'Edgehop' : `Edgehop — controlling ${activeMachine(st)?.name || ''}`;
}

native.on('edgehop.state', applyState);
native.on('edgehop.log', addLog);
native.on('edgehop.pairCode', (pc) => codeDialog.show(pc));
native.on('edgehop.pairResult', (r) => codeDialog.result(r));
native.on('edgehop.pairing', (p) => pairDialog.update(p));
native.on('native.connected', () => { if (app.ready) refresh(); });
native.on('native.disconnected', () => { $('#status-strip').replaceChildren(h('span.badge.badge-danger.badge-dot', {}, 'Backend disconnected')); });

async function refresh() {
  const [info, settings, state, logs] = await Promise.all([
    native.call('app.info'), native.call('edgehop.settings.get'), native.call('edgehop.state'), native.call('edgehop.log.recent'),
  ]);
  app.info = info;
  app.settings = settings;
  if (!settingsBuilt) buildSettings();
  fillSettings();
  app.logs = (logs.entries || []).slice(-LOG_MAX);
  renderLog();
  applyState(state);
}

refresh().then(() => { app.ready = true; }).catch((e) => {
  console.error(e);
  $('#page-machines').replaceChildren(h('div.card', {}, h('div.empty-state', {}, h('div.empty-state-title', {}, 'Cannot reach the Edgehop backend'), h('div.empty-state-text', {}, e.message || String(e)))));
});

export { app, nav, layoutEditor, pairDialog, codeDialog };
