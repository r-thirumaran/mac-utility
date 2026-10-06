// screen.js — live preview over binary frames; tap to click; pinch to zoom.
import { conn, call, tryCall, svg, ICONS, on, wakeLock, LOCAL } from './core.js';
import { h, mount, segmented } from './shared/ui.js';

const wake = wakeLock();
let root = null, viewEl = null, canvas = null, ctx = null, overlay = null;
let displays = [];
let display = 0;
let quality = { quality: 0.5, fps: 5 };
let streaming = false;
let offBinary = null;
let decoding = false, latest = null;
let frames = 0;
let scale = 1, tx = 0, ty = 0;
let lastFrameW = 0, lastFrameH = 0;

export function frameCount() { return frames; }

function applyTransform() {
  canvas.style.transform = scale === 1 ? '' : `translate(${tx}px, ${ty}px) scale(${scale})`;
  const reset = root?.querySelector('#reset-zoom');
  if (reset) reset.hidden = scale === 1;
}

async function paint(buf) {
  const bytes = new Uint8Array(buf);
  if (bytes.length < 6 || bytes[0] !== 0x01) return;
  const w = (bytes[2] << 8) | bytes[3], hgt = (bytes[4] << 8) | bytes[5];
  if (decoding) { latest = buf; return; } // keep only the newest frame while decoding
  decoding = true;
  try {
    const bmp = await createImageBitmap(new Blob([bytes.subarray(6)], { type: 'image/jpeg' }));
    if (canvas.width !== w || canvas.height !== hgt) { canvas.width = w; canvas.height = hgt; if (viewEl) viewEl.style.aspectRatio = `${w} / ${hgt}`; }
    ctx.drawImage(bmp, 0, 0, w, hgt);
    bmp.close?.();
    lastFrameW = w; lastFrameH = hgt;
    frames++;
    if (overlay) overlay.hidden = true;
  } catch { /* corrupt frame */ } finally {
    decoding = false;
    if (latest) { const next = latest; latest = null; paint(next); }
  }
}

function maxWidth() {
  const w = viewEl?.clientWidth || window.innerWidth;
  return Math.max(160, Math.min(1280, Math.round(w * (window.devicePixelRatio || 1))));
}

async function start() {
  if (streaming || !viewEl || !conn.laptop) return;
  if (conn.status !== 'online') { setOverlay('Connecting…'); return; }
  streaming = true;
  setOverlay('Starting stream…');
  offBinary?.();
  offBinary = on('event:native.binary', paint);
  try {
    await call('screen.stream.start', { display, maxWidth: maxWidth(), quality: quality.quality, fps: quality.fps });
  } catch (e) {
    streaming = false;
    setOverlay(e.code === 'needs_permission' ? 'Screen Recording permission needed on the laptop' : `Cannot stream: ${e.message}`);
  }
}

function stop() {
  if (!streaming) return;
  streaming = false;
  offBinary?.(); offBinary = null;
  tryCall('screen.stream.stop', {}, { quiet: true });
}

function setOverlay(text) {
  if (!overlay) return;
  overlay.hidden = !text;
  overlay.textContent = text || '';
}

// Pointer handling: tap → click at the normalised point; two fingers → pinch zoom; drag pans when zoomed.
const pts = new Map();
let pinch = null, tapStart = null;
function onDown(e) {
  e.preventDefault();
  try { viewEl.setPointerCapture(e.pointerId); } catch { /* ignore */ }
  pts.set(e.pointerId, { x: e.clientX, y: e.clientY });
  if (pts.size === 1) tapStart = { x: e.clientX, y: e.clientY, t: performance.now(), moved: 0 };
  if (pts.size === 2) {
    const [a, b] = [...pts.values()];
    pinch = { d: Math.hypot(a.x - b.x, a.y - b.y), scale, tx, ty, cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2 };
    tapStart = null;
  }
}
function onMove(e) {
  const p = pts.get(e.pointerId);
  if (!p) return;
  const dx = e.clientX - p.x, dy = e.clientY - p.y;
  p.x = e.clientX; p.y = e.clientY;
  if (tapStart) tapStart.moved += Math.abs(dx) + Math.abs(dy);
  if (pts.size === 2 && pinch) {
    const [a, b] = [...pts.values()];
    const d = Math.hypot(a.x - b.x, a.y - b.y);
    const s = Math.min(4, Math.max(1, pinch.scale * (d / Math.max(1, pinch.d))));
    const rect = viewEl.getBoundingClientRect();
    const fx = pinch.cx - rect.left, fy = pinch.cy - rect.top;
    tx = fx - (fx - pinch.tx) * (s / pinch.scale);
    ty = fy - (fy - pinch.ty) * (s / pinch.scale);
    scale = s;
    if (scale === 1) { tx = 0; ty = 0; }
    applyTransform();
  } else if (pts.size === 1 && scale > 1 && tapStart && tapStart.moved > 8) {
    tx += dx; ty += dy;
    applyTransform();
  }
}
function onUp(e) {
  if (!pts.has(e.pointerId)) return;
  pts.delete(e.pointerId);
  if (pts.size < 2) pinch = null;
  if (pts.size === 0 && tapStart) {
    const t = tapStart; tapStart = null;
    if (t.moved < 10 && performance.now() - t.t < 400 && lastFrameW) {
      const rect = canvas.getBoundingClientRect();
      const x = (e.clientX - rect.left) / rect.width, y = (e.clientY - rect.top) / rect.height;
      if (x >= 0 && x <= 1 && y >= 0 && y <= 1) tryCall('mouse.click', { button: 'left', count: 1, display, x: Math.round(x * 1000) / 1000, y: Math.round(y * 1000) / 1000 });
    }
  }
}

function render() {
  if (!root) return;
  canvas = h('canvas', { id: 'screen-canvas', width: 16, height: 9 });
  ctx = canvas.getContext('2d');
  overlay = h('div.screen-overlay', { id: 'screen-overlay' }, 'Connecting…');
  viewEl = h('div.screen-view', { id: 'screen-view', role: 'application', 'aria-label': 'Screen preview; tap to click' }, canvas, overlay);
  viewEl.addEventListener('pointerdown', onDown);
  viewEl.addEventListener('pointermove', onMove);
  viewEl.addEventListener('pointerup', onUp);
  viewEl.addEventListener('pointercancel', (e) => { pts.delete(e.pointerId); pinch = null; tapStart = null; });
  viewEl.addEventListener('contextmenu', (e) => e.preventDefault());
  const qual = h('div.segmented', { id: 'screen-quality' },
    h('button', { dataset: { value: 'low' } }, 'Low'), h('button', { dataset: { value: 'med' }, 'aria-pressed': 'true' }, 'Medium'), h('button', { dataset: { value: 'high' } }, 'High'));
  segmented(qual, { onChange(v) { quality = v === 'low' ? { quality: 0.3, fps: 3 } : v === 'high' ? { quality: 0.7, fps: 8 } : { quality: 0.5, fps: 5 }; restart(); } });
  const displaySel = h('span.select.select-sm', { id: 'screen-display', hidden: true }, h('select', { 'aria-label': 'Display', onchange: (e) => { display = Number(e.target.value); restart(); } }));
  const reset = h('button.btn.btn-secondary.btn-sm', { type: 'button', id: 'reset-zoom', hidden: true, onclick: () => { scale = 1; tx = ty = 0; applyTransform(); } }, 'Reset zoom');
  if (!LOCAL && !conn.laptop) {
    mount(root, [h('div.page-head', {}, h('h1.page-title', {}, 'Screen')), h('div.card.empty-state', {}, h('div.empty-state-title', {}, 'No laptop selected'), h('a.btn.btn-primary', { href: '#laptops' }, 'Go to Laptops'))]);
    return;
  }
  mount(root, [
    h('div.page-head', {}, h('h1.page-title', {}, 'Screen'), displaySel),
    viewEl,
    h('div.screen-controls', {}, qual, reset, h('span.screen-hint.grow', {}, 'Tap the preview to click there · pinch to zoom')),
  ]);
  if (!LOCAL && !conn.laptop) return;
  loadDisplays(displaySel);
}

async function loadDisplays(sel) {
  const list = await tryCall('screen.displays', {}, { quiet: true });
  if (!Array.isArray(list) || !sel) return;
  displays = list;
  if (display >= displays.length) display = 0;
  const select = sel.querySelector('select');
  mount(select, displays.map((d) => h('option', { value: String(d.index), selected: d.index === display }, `${d.name || 'Display ' + (d.index + 1)} ${d.w}×${d.h}`)));
  sel.hidden = displays.length < 2;
}

function restart() { stop(); setTimeout(start, 50); }

function onVisibility() { if (document.visibilityState === 'hidden') stop(); else if (root) start(); }

export default {
  id: 'screen', title: 'Screen', icon: ICONS.screen,
  show(container) {
    root = container; frames = 0; scale = 1; tx = ty = 0;
    render();
    wake.acquire();
    document.addEventListener('visibilitychange', onVisibility);
    start();
  },
  hide() {
    document.removeEventListener('visibilitychange', onVisibility);
    stop();
    wake.release();
    root = null; viewEl = null; overlay = null;
  },
};

on('status', (s) => { if (!root) return; if (s === 'online') { streaming = false; loadDisplays(root.querySelector('#screen-display')); start(); } else { streaming = false; setOverlay(s === 'offline' ? 'Laptop is offline' : 'Reconnecting…'); } });
