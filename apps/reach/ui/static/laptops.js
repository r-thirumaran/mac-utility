// laptops.js — the agents this phone knows: status, pairing, discovery.
import { LOCAL, store, conn, api, call, deviceName, parseAddress, svg, ICONS, on } from './core.js';
import { h, mount, toast, confirmDialog } from './shared/ui.js';

const online = new Map(); // laptop id → true/false/undefined (checking)
let root = null;
let busy = false;

function codeDialog(laptop) {
  return new Promise((resolve) => {
    const input = h('input.input.input-lg.code-input', { type: 'text', inputmode: 'numeric', pattern: '[0-9]*', maxlength: '6', autocomplete: 'one-time-code', 'aria-label': 'Pairing code', placeholder: '······' });
    const ok = h('button.btn.btn-primary', { type: 'button', onclick: () => dialog.close('ok') }, 'Pair');
    const cancel = h('button.btn.btn-secondary', { type: 'button', onclick: () => dialog.close('cancel') }, 'Cancel');
    const dialog = h('dialog.dialog.pair-dialog', { 'aria-labelledby': 'pair-title' },
      h('div.dialog-body.stack.gap-3', {},
        h('h2.dialog-title', { id: 'pair-title' }, `Pair with ${laptop.name}`),
        h('div.dialog-text', {}, 'Type the 6-digit code shown on the laptop (notification, settings window or tray). It is valid for two minutes.'),
        input),
      h('div.dialog-actions', {}, cancel, ok));
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); dialog.close('ok'); } });
    dialog.addEventListener('cancel', (e) => { e.preventDefault(); dialog.close('cancel'); });
    dialog.addEventListener('close', () => { resolve(dialog.returnValue === 'ok' ? input.value.trim() : null); dialog.remove(); });
    document.body.append(dialog);
    dialog.showModal();
    input.focus();
  });
}

async function pair(laptop) {
  if (busy) return;
  busy = true;
  try {
    try {
      await api(laptop, '/api/pair/request', { method: 'POST', body: { device: deviceName() } });
    } catch (e) {
      toast(e.body?.error === 'locked' ? `Pairing is locked on ${laptop.name} after too many wrong codes. Try again in ${Math.ceil((e.body.retryAfterSec || 600) / 60)} min.` : `${laptop.name} is not reachable`, { kind: 'danger', timeout: 6000 });
      return;
    }
    for (let attempt = 0; attempt < 5; attempt++) {
      const code = await codeDialog(laptop);
      if (code == null) return;
      if (!/^\d{6}$/.test(code)) { toast('The code has six digits', { kind: 'warning' }); continue; }
      try {
        const res = await api(laptop, '/api/pair', { method: 'POST', body: { device: deviceName(), code } });
        const updated = store.update(laptop.id, { token: res.token });
        toast(`Paired with ${laptop.name}`, { kind: 'success' });
        conn.connect(updated);
        render();
        location.hash = '#remote';
        return;
      } catch (e) {
        if (e.body?.error === 'locked') { toast('Too many wrong codes — pairing is locked for 10 minutes', { kind: 'danger', timeout: 6000 }); return; }
        if (e.body?.error === 'bad_code') { toast('Wrong code, try again', { kind: 'danger' }); continue; }
        toast(`Pairing failed: ${e.message}`, { kind: 'danger' });
        return;
      }
    }
  } finally { busy = false; }
}

async function checkAll() {
  const list = store.list();
  await Promise.all(list.map(async (l) => {
    online.set(l.id, undefined);
    render();
    try {
      const info = await api(l, '/api/info', { timeout: 3000 });
      online.set(l.id, true);
      store.update(l.id, { name: info.name, platform: info.platform, agentId: info.id });
    } catch { online.set(l.id, false); }
    render();
  }));
  loadPeers();
}

let peers = [];
async function loadPeers() {
  const found = new Map();
  if (LOCAL) {
    try { const st = await call('reach.state'); for (const p of st.peers || []) found.set(p.id, p); } catch { /* ignore */ }
  } else {
    const sources = store.list().filter((l) => online.get(l.id));
    await Promise.all(sources.map(async (l) => {
      try { for (const p of await api(l, '/api/peers', { timeout: 3000 })) found.set(p.id, p); } catch { /* ignore */ }
    }));
  }
  const known = store.list();
  peers = [...found.values()].filter((p) => !known.some((l) => l.agentId === p.id || (l.host === p.host && l.port === p.port)));
  render();
}

function addAddress(text) {
  const addr = parseAddress(text);
  if (!addr) { toast('Enter an address like 192.168.1.20 or laptop.local:7788', { kind: 'warning' }); return; }
  const item = store.add({ host: addr.host, port: addr.port, name: addr.host });
  toast(`Added ${item.name}`);
  render();
  checkAll();
}

function statusBadge(l) {
  const st = online.get(l.id);
  if (st === undefined) return h('span.badge', {}, 'checking…');
  return st ? h('span.badge.badge-success.badge-dot', {}, 'online') : h('span.badge.badge-danger.badge-dot', {}, 'offline');
}

function laptopRow(l) {
  const isCurrent = conn.laptop && conn.laptop.id === l.id;
  const actions = [];
  if (!l.token) {
    actions.push(h('button.btn.btn-primary.btn-sm', { type: 'button', dataset: { action: 'pair' }, onclick: () => pair(l), disabled: online.get(l.id) === false }, 'Pair'));
  } else if (isCurrent && conn.status === 'online') {
    actions.push(h('span.badge.badge-accent.badge-dot', {}, 'connected'));
  } else {
    actions.push(h('button.btn.btn-secondary.btn-sm', { type: 'button', dataset: { action: 'connect' }, onclick: () => { conn.connect(l); location.hash = '#remote'; } }, 'Connect'));
  }
  actions.push(h('button.btn.btn-ghost.btn-sm.btn-icon', { type: 'button', 'aria-label': `Forget ${l.name}`, onclick: async () => {
    if (await confirmDialog({ title: `Forget ${l.name}?`, body: 'The pairing stays on the laptop until you revoke it there.', okLabel: 'Forget', danger: true })) {
      if (conn.laptop?.id === l.id) conn.disconnect();
      store.remove(l.id);
      render();
    }
  } }, svg(ICONS.x, { size: 16 })));
  return h('div.card.laptop-row', { dataset: { laptop: l.id } },
    h('div.laptop-icon', {}, svg(ICONS.laptop, { size: 22 })),
    h('div.laptop-main', {},
      h('div.row.gap-2.wrap', {}, h('span.laptop-name.truncate', {}, l.name), statusBadge(l), l.token ? h('span.badge.badge-outline', {}, 'paired') : null),
      h('div.laptop-addr', {}, `${l.host}:${l.port}`, l.platform ? ` · ${l.platform}` : '')),
    h('div.laptop-actions', {}, actions));
}

function render() {
  if (!root) return;
  const list = store.list();
  const addInput = h('input.input', { type: 'text', placeholder: 'host or host:port', 'aria-label': 'Laptop address', autocapitalize: 'off', autocorrect: 'off', spellcheck: 'false', id: 'laptop-address' });
  const addBtn = h('button.btn.btn-primary', { type: 'button', id: 'laptop-add', onclick: () => { addAddress(addInput.value); addInput.value = ''; } }, svg(ICONS.plus, { size: 16 }), 'Add');
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addBtn.click(); } });
  const nodes = [
    h('div.page-head', {}, h('h1.page-title', {}, 'Laptops'),
      h('button.btn.btn-ghost.btn-sm', { type: 'button', onclick: checkAll, 'aria-label': 'Refresh' }, svg(ICONS.refresh, { size: 16 }), 'Refresh')),
  ];
  if (LOCAL) {
    nodes.push(h('div.card', {}, h('div.card-title', {}, 'This is the agent’s own page'),
      h('p.text-muted.text-sm', { style: 'margin-top:6px' }, 'Phones pair from the Laptops tab of the app they open at one of the addresses on the Settings tab. The agents this computer sees on the network are listed below.')));
  } else {
    nodes.push(list.length ? h('div.stack.gap-3', { id: 'laptop-list' }, list.map(laptopRow))
      : h('div.card.empty-state', {}, svg(ICONS.laptop, { size: 40, className: 'empty-state-icon' }), h('div.empty-state-title', {}, 'No laptops yet'), h('div.empty-state-text', {}, 'Add one by address, or open http://<laptop-ip>:7788 on this phone and it appears here.')));
    nodes.push(h('div.card.stack.gap-2', {}, h('div.card-title', {}, 'Add a laptop'), h('div.input-group', {}, addInput, addBtn),
      h('div.hint.text-sm.text-muted', {}, 'The address is on the laptop’s Reach settings page (default port 7788).')));
  }
  nodes.push(h('div.stack.gap-2', {}, h('div.section-title', {}, 'Nearby'),
    peers.length ? peers.map((p) => h('div.card.laptop-row', { dataset: { peer: p.id } },
      h('div.laptop-icon', {}, svg(ICONS.wifi, { size: 22 })),
      h('div.laptop-main', {}, h('div.laptop-name.truncate', {}, p.name), h('div.laptop-addr.truncate', {}, `${p.host}:${p.port}`, p.platform ? ` · ${p.platform}` : '')),
      LOCAL ? null : h('div.laptop-actions', {}, h('button.btn.btn-secondary.btn-sm', { type: 'button', onclick: () => { store.add({ id: p.id, agentId: p.id, host: p.host, port: p.port, name: p.name, platform: p.platform }); render(); checkAll(); } }, 'Add'))))
      : h('div.text-sm.text-muted', {}, LOCAL ? 'No other Reach agents seen on the network yet.' : 'Laptops that are online share the other agents they see on the network.')));
  mount(root, nodes);
}

on('status', () => render());
on('laptops', () => render());

export default {
  id: 'laptops', title: 'Laptops', icon: ICONS.laptop,
  show(container) { root = container; render(); checkAll(); },
  hide() { root = null; },
};
