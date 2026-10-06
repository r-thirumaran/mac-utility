// remote.js — trackpad, keyboard sheet and media bar.
import { conn, call, tryCall, svg, ICONS, on, wakeLock, LOCAL, hooks } from './core.js';
import { h, mount, sliderFill, debounce } from './shared/ui.js';

const wake = wakeLock();
let root = null;
let padEl = null;
let sheet = null;
let volumeRow = null;
let volumeSlider = null;
let volumeFill = null;
const latched = new Set();
let modButtons = [];

// ------------------------------------------------------------------ trackpad

const pointers = new Map();
let gesture = null;
let lastTap = { t: 0, x: 0, y: 0 };
const pending = { mx: 0, my: 0, sx: 0, sy: 0 };
let raf = 0;
const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

function flush() {
  raf = 0;
  if (Math.abs(pending.mx) + Math.abs(pending.my) >= 0.5) {
    if (conn.status === 'online') call('mouse.move', { dx: round1(pending.mx), dy: round1(pending.my) }).catch(() => {});
    pending.mx = pending.my = 0;
  }
  if (Math.abs(pending.sx) + Math.abs(pending.sy) >= 1) {
    if (conn.status === 'online') call('mouse.scroll', { dx: round1(pending.sx), dy: round1(pending.sy) }).catch(() => {});
    pending.sx = pending.sy = 0;
  }
}
const round1 = (v) => Math.round(v * 10) / 10;
function schedule() { if (!raf) raf = requestAnimationFrame(flush); }

function click(button, count = 1) {
  hooks.haptic?.();
  tryCall('mouse.click', { button, count });
}

function onDown(e) {
  if (e.pointerType === 'mouse' && e.button !== 0) return; // right/middle mouse → contextmenu/auxclick
  e.preventDefault();
  try { padEl.setPointerCapture(e.pointerId); } catch { /* ignore */ }
  const now = performance.now();
  pointers.set(e.pointerId, { lx: e.clientX, ly: e.clientY, t: now });
  if (!gesture) {
    const nearTap = now - lastTap.t < 320 && Math.hypot(e.clientX - lastTap.x, e.clientY - lastTap.y) < 40;
    gesture = { start: now, maxPointers: 1, moved: 0, dragging: false, dragCandidate: nearTap, x: e.clientX, y: e.clientY };
  } else gesture.maxPointers = Math.max(gesture.maxPointers, pointers.size);
  padEl.classList.add('is-active');
}

function onMove(e) {
  const p = pointers.get(e.pointerId);
  if (!p || !gesture) return;
  e.preventDefault();
  const now = performance.now();
  const dx = e.clientX - p.lx, dy = e.clientY - p.ly;
  const dt = Math.max(1, now - p.t);
  p.lx = e.clientX; p.ly = e.clientY; p.t = now;
  gesture.moved += Math.abs(dx) + Math.abs(dy);
  if (pointers.size === 1) {
    if (gesture.dragCandidate && !gesture.dragging && gesture.moved > 6) {
      gesture.dragging = true;
      tryCall('mouse.down', { button: 'left' });
    }
    // Acceleration: slow strokes map ~1:1, quick flicks up to 3.5×.
    const speed = Math.hypot(dx, dy) / dt; // px per ms
    const gain = clamp(0.9 + speed * 1.6, 0.9, 3.5);
    pending.mx += dx * gain;
    pending.my += dy * gain;
    schedule();
  } else if (pointers.size === 2) {
    // Two fingers: natural scrolling, each finger contributes half.
    pending.sx += -dx / 2;
    pending.sy += -dy / 2;
    schedule();
  }
}

function onUp(e) {
  if (!pointers.has(e.pointerId)) return;
  e.preventDefault();
  pointers.delete(e.pointerId);
  if (pointers.size > 0) return;
  const g = gesture;
  gesture = null;
  padEl.classList.remove('is-active');
  flush();
  if (!g) return;
  if (g.dragging) { tryCall('mouse.up', { button: 'left' }); return; }
  const now = performance.now();
  const quick = now - g.start < 320;
  if (!quick) return;
  if (g.maxPointers >= 3 && g.moved < 30) click('middle');
  else if (g.maxPointers === 2 && g.moved < 30) click('right');
  else if (g.maxPointers === 1 && g.moved < 12) {
    const dbl = now - lastTap.t < 320 && Math.hypot(g.x - lastTap.x, g.y - lastTap.y) < 40;
    // macOS needs an explicit click count; Windows merges two quick clicks itself.
    click('left', dbl && conn.isMac ? 2 : 1);
    lastTap = dbl ? { t: 0, x: 0, y: 0 } : { t: now, x: g.x, y: g.y };
  }
}

function onCancel(e) {
  pointers.delete(e.pointerId);
  if (pointers.size === 0 && gesture) {
    if (gesture.dragging) tryCall('mouse.up', { button: 'left' });
    gesture = null;
    padEl.classList.remove('is-active');
  }
}

function buildTrackpad() {
  padEl = h('div.trackpad', { id: 'trackpad', role: 'application', 'aria-label': 'Trackpad', tabindex: '0' },
    h('div.trackpad-hint', {}, h('b', {}, 'Trackpad'), 'Move with one finger · tap to click · two fingers scroll · two-finger tap right-clicks · tap-tap-hold drags'));
  padEl.addEventListener('pointerdown', onDown);
  padEl.addEventListener('pointermove', onMove);
  padEl.addEventListener('pointerup', onUp);
  padEl.addEventListener('pointercancel', onCancel);
  padEl.addEventListener('lostpointercapture', (e) => { if (pointers.has(e.pointerId)) onUp(e); });
  padEl.addEventListener('contextmenu', (e) => { e.preventDefault(); if (e.pointerType !== 'touch' && e.button === 2) click('right'); });
  padEl.addEventListener('auxclick', (e) => { if (e.button === 1) click('middle'); });
  padEl.addEventListener('wheel', (e) => { e.preventDefault(); pending.sx += e.deltaX; pending.sy += e.deltaY; schedule(); }, { passive: false });
  padEl.addEventListener('dblclick', (e) => e.preventDefault());
  return padEl;
}

// ------------------------------------------------------------------ keyboard

function modLabel() { return conn.isMac ? '⌘' : 'Ctrl'; }

async function press(key, extraMods = []) {
  const mods = [...latched, ...extraMods];
  clearLatches();
  await tryCall('key.press', mods.length ? { key, mods } : { key });
}

async function sendText(text) {
  if (!text) return;
  if (latched.size) {
    for (const ch of text) await press(ch);
    return;
  }
  await tryCall('key.text', { text });
}

function clearLatches() {
  latched.clear();
  for (const b of modButtons) b.setAttribute('aria-pressed', 'false');
}

function modButton(mod, label) {
  const b = h('button.btn.btn-secondary', { type: 'button', 'aria-pressed': 'false', dataset: { mod }, onclick: () => {
    if (latched.has(mod)) latched.delete(mod); else latched.add(mod);
    b.setAttribute('aria-pressed', String(latched.has(mod)));
    keepFocus();
  } }, label);
  modButtons.push(b);
  return b;
}

let inputEl = null;
function keepFocus() { inputEl?.focus({ preventScroll: true }); }

function keyButton(label, key, { icon, mods } = {}) {
  return h('button.btn.btn-secondary', { type: 'button', dataset: { key }, 'aria-label': typeof label === 'string' ? label : key, onclick: () => { press(key, mods); keepFocus(); } }, icon ? svg(icon, { size: 18 }) : label);
}

function buildSheet() {
  modButtons = [];
  inputEl = h('input.keyboard-input', { type: 'text', id: 'keyboard-input', placeholder: 'Type here — it goes to the laptop', autocomplete: 'off', autocapitalize: 'off', autocorrect: 'off', spellcheck: 'false', 'aria-label': 'Text to type on the laptop' });
  inputEl.addEventListener('beforeinput', (e) => {
    if (e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph') { e.preventDefault(); press('Enter'); }
  });
  inputEl.addEventListener('input', (e) => {
    if (e.isComposing || e.inputType === 'insertCompositionText') return;
    const t = e.inputType;
    if (t === 'deleteContentBackward') press('Backspace');
    else if (t === 'deleteContentForward') press('Delete');
    else if (t === 'insertText' && e.data != null) sendText(e.data);
    else if (inputEl.value) sendText(inputEl.value);
    inputEl.value = '';
  });
  inputEl.addEventListener('compositionend', (e) => { if (e.data) sendText(e.data); inputEl.value = ''; });
  inputEl.addEventListener('keydown', (e) => {
    const special = { Enter: 'Enter', Tab: 'Tab', Escape: 'Escape', ArrowUp: 'ArrowUp', ArrowDown: 'ArrowDown', ArrowLeft: 'ArrowLeft', ArrowRight: 'ArrowRight' };
    if (e.key === 'Backspace' && inputEl.value === '') { e.preventDefault(); press('Backspace'); return; }
    if (special[e.key] && !e.isComposing) { e.preventDefault(); press(special[e.key]); return; }
    if ((e.ctrlKey || e.metaKey) && e.key.length === 1) { e.preventDefault(); press(e.key, ['cmdOrCtrl']); }
  });

  const fkeys = h('div.fkeys', { hidden: true, id: 'fkeys' }, Array.from({ length: 12 }, (_, i) => keyButton(`F${i + 1}`, `F${i + 1}`)));
  const fnBtn = h('button.btn.btn-secondary', { type: 'button', 'aria-pressed': 'false', 'aria-controls': 'fkeys', onclick: () => { fkeys.hidden = !fkeys.hidden; fnBtn.setAttribute('aria-pressed', String(!fkeys.hidden)); keepFocus(); } }, 'Fn');
  const cmdBtn = modButton('cmdOrCtrl', modLabel());
  on('info', () => { cmdBtn.textContent = modLabel(); });

  const done = h('button.btn.btn-secondary', { type: 'button', onclick: () => sheet.close() }, 'Done');
  sheet = h('dialog.sheet', { id: 'keyboard-sheet', 'aria-label': 'Keyboard' },
    h('div.sheet-grabber'),
    h('div.sheet-body.stack.gap-3', {},
      inputEl,
      h('div.key-rows', {},
        h('div.key-row', {}, keyButton('Esc', 'Escape'), keyButton('Tab', 'Tab'), cmdBtn, modButton('alt', 'Alt'), modButton('shift', 'Shift'), fnBtn),
        fkeys,
        h('div.key-row', {}, keyButton('Left', 'ArrowLeft', { icon: ICONS.arrowLeft }), keyButton('Up', 'ArrowUp', { icon: ICONS.arrowUp }), keyButton('Down', 'ArrowDown', { icon: ICONS.arrowDown }), keyButton('Right', 'ArrowRight', { icon: ICONS.arrowRight }),
          keyButton('Backspace', 'Backspace', { icon: ICONS.backspace }), keyButton('Enter', 'Enter', { icon: ICONS.enter }))),
      h('div.sheet-actions', {}, done)));
  sheet.addEventListener('close', clearLatches);
  sheet.addEventListener('click', (e) => { if (e.target === sheet) sheet.close(); });
  return sheet;
}

function openKeyboard() {
  if (!sheet.open) sheet.showModal();
  keepFocus();
}

// ------------------------------------------------------------------ media

function mediaButton(action, icon, label) {
  return h('button.btn.btn-secondary.btn-icon', { type: 'button', dataset: { media: action }, 'aria-label': label, onclick: async () => {
    await tryCall('media', { action });
    if (action.startsWith('volume') || action === 'mute') setTimeout(refreshVolume, 150);
  } }, svg(icon));
}

async function refreshVolume() {
  const v = await tryCall('volume.get', {}, { quiet: true });
  if (!volumeRow) return;
  if (!v) { volumeRow.hidden = true; return; }
  volumeRow.hidden = false;
  volumeSlider.value = String(v.level);
  volumeSlider.disabled = false;
  volumeFill?.update();
  volumeRow.querySelector('.slider-value').textContent = v.muted ? 'muted' : `${v.level}%`;
}

function buildMedia() {
  volumeSlider = h('input.slider', { type: 'range', min: '0', max: '100', value: '50', id: 'volume', 'aria-label': 'Volume' });
  const setVol = debounce(() => tryCall('volume.set', { level: Number(volumeSlider.value) }), 120);
  volumeSlider.addEventListener('input', () => { volumeRow.querySelector('.slider-value').textContent = `${volumeSlider.value}%`; setVol(); });
  volumeFill = sliderFill(volumeSlider);
  volumeRow = h('div.volume-row.slider-row', { hidden: true }, svg(ICONS.volDown, { size: 18 }), volumeSlider, h('span.slider-value', {}, '50%'));
  return h('div.stack.gap-2', {},
    h('div.media-bar', {},
      mediaButton('previous', ICONS.prev, 'Previous track'), mediaButton('playPause', ICONS.play, 'Play or pause'), mediaButton('next', ICONS.next, 'Next track'),
      mediaButton('volumeDown', ICONS.volDown, 'Volume down'), mediaButton('mute', ICONS.mute, 'Mute'), mediaButton('volumeUp', ICONS.volUp, 'Volume up')),
    volumeRow);
}

// ------------------------------------------------------------------ page

function render() {
  if (!root) return;
  root.classList.add('is-fill');
  const kb = h('button.btn.btn-primary', { type: 'button', id: 'open-keyboard', onclick: openKeyboard }, svg(ICONS.keyboard, { size: 18 }), 'Keyboard');
  if (!LOCAL && !conn.laptop) {
    mount(root, h('div.card.empty-state', {}, svg(ICONS.remote, { size: 40, className: 'empty-state-icon' }), h('div.empty-state-title', {}, 'No laptop selected'), h('div.empty-state-text', {}, 'Pair or connect a laptop on the Laptops tab to use the trackpad.'), h('a.btn.btn-primary', { href: '#laptops' }, 'Go to Laptops')));
    return;
  }
  mount(root, [buildTrackpad(), h('div.remote-bar', {}, kb, h('span.text-sm.text-muted.grow.truncate', {}, conn.laptop.name || '')), buildMedia(), buildSheet()]);
}

export default {
  id: 'remote', title: 'Remote', icon: ICONS.remote,
  show(container) {
    root = container;
    render();
    wake.acquire();
    if (conn.status === 'online') refreshVolume();
  },
  hide() {
    wake.release();
    if (sheet?.open) sheet.close();
    root?.classList.remove('is-fill');
    root = null; volumeRow = null;
  },
};

on('status', (s) => { if (!root) return; if (!padEl?.isConnected) render(); if (s === 'online') refreshVolume(); });
