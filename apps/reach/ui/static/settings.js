// settings.js — the phone's own settings, and on the laptop the agent's
// state: name, port, addresses + QR, pairing code, paired devices, peers.
import { LOCAL, conn, store, call, tryCall, deviceName, setDeviceName, svg, ICONS, on } from './core.js';
import { h, mount, toast, confirmDialog, segmented, setTheme, getTheme, relativeTime } from './shared/ui.js';
import { qrSVG } from './qr.js';

let root = null;
let state = null;
let pairCode = null; // {code, expiresAt}
let codeTimer = 0;
let urlIndex = 0;

function themeCard() {
  const seg = h('div.segmented', { id: 'theme-switch' },
    h('button', { dataset: { value: 'auto' } }, 'Auto'), h('button', { dataset: { value: 'light' } }, 'Light'), h('button', { dataset: { value: 'dark' } }, 'Dark'));
  const ctl = segmented(seg, { onChange: (v) => setTheme(v) });
  ctl.select(getTheme() || 'auto', { silent: true });
  return h('div.settings', {}, h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, 'Appearance'), h('div.setting-desc', {}, 'Follows the system unless pinned')), h('div.setting-control', {}, seg)));
}

// ------------------------------------------------------------------ phone

function renderPhone() {
  const nameInput = h('input.input', { type: 'text', value: deviceName(), 'aria-label': 'This phone’s name', id: 'device-name', maxlength: '64' });
  nameInput.addEventListener('change', () => { setDeviceName(nameInput.value); toast('Name saved'); });
  const laptops = store.list();
  mount(root, [
    h('div.page-head', {}, h('h1.page-title', {}, 'Settings')),
    h('div.settings', {},
      h('div.setting.is-stacked', {}, h('div.setting-main', {}, h('div.setting-label', {}, 'This phone’s name'), h('div.setting-desc', {}, 'Shown on the laptop when you pair and in its list of paired devices')), h('div.setting-control', {}, nameInput))),
    themeCard(),
    h('div.card.stack.gap-2', {}, h('div.card-title', {}, 'Install'),
      h('p.text-sm.text-muted', {}, 'Add Reach to your home screen for a full-screen app: Safari → Share → Add to Home Screen; Chrome → ⋮ → Add to Home screen.')),
    h('div.card.stack.gap-2', {}, h('div.card-title', {}, 'About'),
      h('dl.kv', {}, h('dt', {}, 'App'), h('dd', {}, 'Reach phone app'), h('dt', {}, 'Laptops'), h('dd', {}, `${laptops.length} known, ${laptops.filter((l) => l.token).length} paired`),
        conn.info ? [h('dt', {}, 'Connected to'), h('dd', {}, `${conn.info.name} · ${conn.info.platform} · agent ${conn.info.version}`)] : null),
      h('p.text-sm.text-muted', {}, 'Traffic between phone and laptop is plain HTTP on your local network. Use Reach only on networks you trust.')),
    laptops.length ? h('button.btn.btn-danger.btn-block', { type: 'button', onclick: async () => {
      if (await confirmDialog({ title: 'Forget all laptops?', body: 'Pairings stay on the laptops until revoked there.', okLabel: 'Forget all', danger: true })) { conn.disconnect(); store.save([]); renderPhone(); }
    } }, 'Forget all laptops') : null,
  ]);
}

// ------------------------------------------------------------------ local

function fmtCountdown() {
  if (!pairCode) return '';
  const left = Math.max(0, Math.round((new Date(pairCode.expiresAt).getTime() - Date.now()) / 1000));
  return left ? `expires in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}` : 'expired';
}

async function showCode() {
  const r = await tryCall('reach.pair.showCode');
  if (r) { pairCode = r; renderLocal(); }
}

function startCountdown() {
  clearInterval(codeTimer);
  codeTimer = setInterval(() => {
    const el = root?.querySelector('#pair-countdown');
    if (!el || !pairCode) { clearInterval(codeTimer); return; }
    el.textContent = fmtCountdown();
    if (new Date(pairCode.expiresAt).getTime() < Date.now()) { pairCode = null; renderLocal(); }
  }, 1000);
}

async function loadState() {
  try { state = await call('reach.state'); } catch (e) { state = null; if (root) mount(root, h('div.card.empty-state', {}, h('div.empty-state-title', {}, 'Settings unavailable'), h('div.empty-state-text', {}, e.message))); return; }
  renderLocal();
}

function renderLocal() {
  if (!root || !state) return;
  const nameInput = h('input.input', { type: 'text', value: state.name, 'aria-label': 'Laptop name', id: 'agent-name', maxlength: '64' });
  const saveName = h('button.btn.btn-secondary', { type: 'button', id: 'agent-name-save', onclick: async () => {
    const r = await tryCall('reach.settings.set', { name: nameInput.value.trim() });
    if (r) { state = r; toast('Name saved', { kind: 'success' }); renderLocal(); }
  } }, 'Save');
  const portInput = h('input.input', { type: 'number', min: '1', max: '65535', value: String(state.configuredPort || state.port), 'aria-label': 'Port', id: 'agent-port', style: 'width: 110px' });
  const savePort = h('button.btn.btn-secondary', { type: 'button', onclick: async () => {
    const r = await tryCall('reach.settings.set', { port: Number(portInput.value) });
    if (r) { state = r; toast(r.restartRequired ? 'Port saved — restart Reach to apply it' : 'Port saved', { kind: 'success', timeout: 5000 }); renderLocal(); }
  } }, 'Save');
  const launch = h('input', { type: 'checkbox', role: 'switch', id: 'launch-at-login', checked: !!state.launchAtLogin });
  launch.addEventListener('change', async () => {
    const r = await tryCall('reach.settings.set', { launchAtLogin: launch.checked });
    if (r) state = r; else launch.checked = !launch.checked;
  });
  const urls = state.urls || [];
  if (urlIndex >= urls.length) urlIndex = 0;
  let qrNode = null;
  if (urls.length) {
    try { qrNode = qrSVG(urls[urlIndex]); } catch { qrNode = h('div.text-sm.text-muted', {}, 'Address too long for a QR code — type it on the phone.'); }
  }
  const urlPicker = urls.length > 1 ? h('span.select.select-sm', {}, h('select', { 'aria-label': 'Address', onchange: (e) => { urlIndex = Number(e.target.value); renderLocal(); } }, urls.map((u, i) => h('option', { value: String(i), selected: i === urlIndex }, u)))) : null;
  const pairings = state.pairings || [];
  mount(root, [
    h('div.page-head', {}, h('h1.page-title', {}, 'Settings'), h('span.badge.badge-outline', {}, `Reach ${state.version} · ${state.platform}`)),
    h('div.settings', { id: 'agent-settings' },
      h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, 'Name'), h('div.setting-desc', {}, 'How phones see this computer')), h('div.setting-control', {}, nameInput, saveName)),
      h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, 'Port'), h('div.setting-desc', {}, state.port !== (state.configuredPort || state.port) ? `Listening on ${state.port} this run` : 'Phones connect to this port')), h('div.setting-control', {}, portInput, savePort)),
      h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, 'Launch at login'), h('div.setting-desc', {}, 'Start Reach when you sign in')), h('div.setting-control', {}, h('label.toggle', {}, launch, h('span.toggle-track'))))),
    h('div.card.stack.gap-3', { id: 'connect-card' },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, 'Connect a phone'), h('div.card-subtitle', {}, 'Open one of these addresses on the phone (same Wi-Fi), then tap Pair')), urlPicker),
      urls.length ? h('div.qr-box', {}, qrNode, h('div.url-list', { id: 'agent-urls' }, urls.map((u) => h('code.selectable', {}, u)))) : h('p.text-sm.text-muted', {}, 'No network address yet — join a network and reopen this page.'),
      pairCode ? h('div.card.stack.gap-1', { id: 'pair-code-card', style: 'background: var(--accent-soft); border-color: transparent' }, h('div.text-sm.text-center.text-muted', {}, 'Pairing code'), h('div.pair-code', { id: 'pair-code' }, pairCode.code), h('div.text-sm.text-center.text-muted', { id: 'pair-countdown' }, fmtCountdown())) : null,
      h('button.btn.btn-primary', { type: 'button', id: 'show-code', onclick: showCode }, pairCode ? 'New pairing code' : 'Show pairing code')),
    h('div.stack.gap-2', {}, h('div.section-title', {}, 'Paired devices'),
      pairings.length ? h('div.settings', { id: 'pairings' }, pairings.map((p) => h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, p.device), h('div.setting-desc', {}, `Paired ${relativeTime(new Date(p.createdAt))} · last seen ${relativeTime(new Date(p.lastSeen))}`)),
        h('div.setting-control', {}, h('button.btn.btn-secondary.btn-sm', { type: 'button', dataset: { revoke: p.device }, onclick: async () => {
          if (await confirmDialog({ title: `Revoke ${p.device}?`, body: 'The phone will have to pair again.', okLabel: 'Revoke', danger: true })) {
            const r = await tryCall('reach.pairings.revoke', { device: p.device });
            if (r) { toast(`${p.device} revoked`); loadState(); }
          }
        } }, 'Revoke')))))
        : h('div.card.text-sm.text-muted', {}, 'No phone is paired yet.')),
    h('div.stack.gap-2', {}, h('div.section-title', {}, 'Nearby agents'),
      (state.peers || []).length ? h('div.settings', {}, state.peers.map((p) => h('div.setting', {}, h('div.setting-main', {}, h('div.setting-label', {}, p.name), h('div.setting-desc', {}, `${p.host}:${p.port} · ${p.platform}`))))) : h('div.card.text-sm.text-muted', {}, 'No other Reach agents seen on the network.')),
    themeCard(),
    h('div.card.stack.gap-2', {}, h('div.card-title', {}, 'About'),
      h('dl.kv', {}, h('dt', {}, 'Computer'), h('dd', {}, state.hostname), h('dt', {}, 'Agent id'), h('dd.mono', {}, state.agentId), h('dt', {}, 'Backend'), h('dd', {}, state.fake ? 'fake (recording)' : state.platform)),
      h('p.text-sm.text-muted', {}, 'Phones talk to this agent in plain HTTP on the local network; use it on networks you trust and revoke devices you no longer use.')),
  ]);
  if (pairCode) startCountdown();
}

on('event:reach.pairCode', (d) => { pairCode = d; if (LOCAL && root) renderLocal(); });
on('event:reach.paired', () => { if (LOCAL && root) { pairCode = null; loadState(); } });
on('status', (s) => { if (LOCAL && root && s === 'online' && !state) loadState(); });

export default {
  id: 'settings', title: 'Settings', icon: ICONS.settings,
  show(container) { root = container; if (LOCAL) { if (state) renderLocal(); loadState(); } else renderPhone(); },
  hide() { root = null; clearInterval(codeTimer); },
};
