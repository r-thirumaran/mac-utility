// layout.js — the screen-canvas layout editor. Every display of every machine
// is drawn to scale at (machine.x + d.x, machine.y + d.y); drag a machine (all
// its displays together) so its edges touch another machine's. The pointer
// crosses only along the highlighted touching edges (spec §2). Save hands
// {machines:[{id,x,y}]} to edgehop.layout.set.
import { h, toast } from './shared/ui.js';

/** Bounding box of a machine's displays (null when it has none). */
export function unionOf(displays) {
  let u = null;
  for (const d of displays || []) {
    if (!(d.w > 0 && d.h > 0)) continue;
    if (!u) { u = { x: d.x, y: d.y, r: d.x + d.w, b: d.y + d.h }; continue; }
    u.x = Math.min(u.x, d.x); u.y = Math.min(u.y, d.y); u.r = Math.max(u.r, d.x + d.w); u.b = Math.max(u.b, d.y + d.h);
  }
  return u ? { x: u.x, y: u.y, w: u.r - u.x, h: u.b - u.y } : null;
}

/**
 * A little monitor showing the machine's displays at their real aspect.
 * @param {{displays?: Array, active?: boolean, status?: string}} m
 * @param {object} [opts]
 */
export function screenGlyph(m, { large = false } = {}) {
  const u = unionOf(m.displays);
  const ratio = u ? Math.min(Math.max(u.w / u.h, 1), 3.2) : 16 / 9;
  const el = h('div.eh-screen', {
    class: [large && 'eh-screen-lg', m.active && 'is-active', m.status === 'offline' && 'is-offline'],
    style: { 'aspect-ratio': `${ratio.toFixed(3)} / 1` },
    aria: { hidden: 'true' },
  });
  if (u) {
    const ratioU = u.w / u.h;
    // When the union is clamped the displays are scaled to fit inside the glyph.
    const sx = ratio >= ratioU ? (ratioU / ratio) : 1;
    const sy = ratio >= ratioU ? 1 : (ratio / ratioU);
    const ox = (1 - sx) / 2;
    const oy = (1 - sy) / 2;
    for (const d of m.displays) {
      if (!(d.w > 0 && d.h > 0)) continue;
      el.append(h('div.eh-display', {
        class: d.primary ? 'is-primary' : null,
        style: {
          left: `${(ox + sx * (d.x - u.x) / u.w) * 100}%`,
          top: `${(oy + sy * (d.y - u.y) / u.h) * 100}%`,
          width: `${(sx * d.w / u.w) * 100}%`,
          height: `${(sy * d.h / u.h) * 100}%`,
        },
      }));
    }
  }
  return el;
}


function displaySummary(displays) {
  if (!displays?.length) return 'no displays';
  if (displays.length === 1) return `${displays[0].w}×${displays[0].h}`;
  return `${displays.length} displays`;
}

// ------------------------------------------------------------ geometry
// Pure helpers (spec §1, §2, §4). A machine is {id, displays}; a position is {x, y}.

export const SNAP_PX = 12;      // snap distance in screen pixels
export const TOUCH_UNITS = 2;   // |gap| ≤ 2 world units counts as touching
const PLACEHOLDER = { x: 0, y: 0, w: 1920, h: 1080 }; // drawn for machines that never reported displays
const SVGNS = 'http://www.w3.org/2000/svg';

/** World rects of a machine's displays when the machine sits at `p`. */
export function rectsOf(m, p) {
  return (m.displays || []).filter((d) => d.w > 0 && d.h > 0)
    .map((d, i) => ({ mid: m.id, id: d.id, idx: i, x: p.x + d.x, y: p.y + d.y, w: d.w, h: d.h, primary: !!d.primary }));
}

const intersects = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

/** First pair of different machines whose displays overlap, or null. `pos` is Map id → {x,y}. */
export function findOverlap(machines, pos) {
  const placed = machines.filter((m) => pos.has(m.id));
  for (let i = 0; i < placed.length; i++) {
    for (let j = i + 1; j < placed.length; j++) {
      const ra = rectsOf(placed[i], pos.get(placed[i].id));
      const rb = rectsOf(placed[j], pos.get(placed[j].id));
      if (ra.some((a) => rb.some((b) => intersects(a, b)))) return [placed[i], placed[j]];
    }
  }
  return null;
}

/**
 * Boundary segments (spec §2). For each pair of placed machines (earlier, later in `machines`),
 * every touching display pair yields {a, b, edge (of a), da, db, x0, y0, x1, y1}.
 */
export function findBoundaries(machines, pos) {
  const placed = machines.filter((m) => pos.has(m.id));
  const out = [];
  for (let i = 0; i < placed.length; i++) {
    for (let j = i + 1; j < placed.length; j++) {
      const M = placed[i], N = placed[j];
      for (const a of rectsOf(M, pos.get(M.id))) {
        for (const b of rectsOf(N, pos.get(N.id))) {
          const y0 = Math.max(a.y, b.y), y1 = Math.min(a.y + a.h, b.y + b.h);
          const x0 = Math.max(a.x, b.x), x1 = Math.min(a.x + a.w, b.x + b.w);
          const seg = (edge, extra) => out.push({ a: M.id, b: N.id, edge, da: a, db: b, ...extra });
          if (y1 - y0 >= 1) {
            if (Math.abs(a.x + a.w - b.x) <= TOUCH_UNITS) seg('right', { x0: (a.x + a.w + b.x) / 2, x1: (a.x + a.w + b.x) / 2, y0, y1 });
            if (Math.abs(a.x - (b.x + b.w)) <= TOUCH_UNITS) seg('left', { x0: (a.x + b.x + b.w) / 2, x1: (a.x + b.x + b.w) / 2, y0, y1 });
          }
          if (x1 - x0 >= 1) {
            if (Math.abs(a.y + a.h - b.y) <= TOUCH_UNITS) seg('bottom', { y0: (a.y + a.h + b.y) / 2, y1: (a.y + a.h + b.y) / 2, x0, x1 });
            if (Math.abs(a.y - (b.y + b.h)) <= TOUCH_UNITS) seg('top', { y0: (a.y + b.y + b.h) / 2, y1: (a.y + b.y + b.h) / 2, x0, x1 });
          }
        }
      }
    }
  }
  return out;
}

const unionOrDefault = (m) => unionOf(m.displays) || PLACEHOLDER;

/** Spec §4 conversion of legacy grid tiles to machine offsets: rows top to bottom, columns left to right, touching. */
export function legacyToPositions(machines, tiles) {
  const byId = new Map(machines.map((m) => [m.id, m]));
  const rows = new Map();
  for (const t of tiles || []) if (byId.has(t.id)) (rows.get(t.row) || rows.set(t.row, []).get(t.row)).push(t);
  const pos = new Map();
  let rowY = 0;
  for (const r of [...rows.keys()].sort((a, b) => a - b)) {
    let cx = 0, tallest = 0;
    for (const t of rows.get(r).sort((a, b) => a.col - b.col)) {
      const u = unionOrDefault(byId.get(t.id));
      pos.set(t.id, { x: cx - u.x, y: rowY - u.y });
      cx += u.w;
      tallest = Math.max(tallest, u.h);
    }
    rowY += tallest;
  }
  return pos;
}

const platLabel = (plat) => ({ macos: 'this Mac', windows: 'this PC' }[plat] || 'this computer');
const dispName = (r) => (r.primary ? 'Main display' : `Display ${r.idx + 1}`);
const hueOf = (i) => (210 + i * 67) % 360;

function sv(tag, attrs = {}, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs)) if (v != null && v !== false) el.setAttribute(k, v === true ? '' : String(v));
  for (const k of kids) if (k != null) el.append(k);
  return el;
}

/** The editor. `onSave(layout)` receives {machines:[{id,x,y}]}. */
export class LayoutEditor {
  constructor(container, { onSave } = {}) {
    this.container = container;
    this.onSave = onSave;
    this.machines = [];       // [{id, name, status, active, displays, self, plat}]
    this.pos = new Map();     // id → {x, y} (the working copy)
    this.saved = new Map();   // the last layout from the backend
    this.dirty = false;
    this.drag = null;
    this.pendingState = null;
    this.scale = 1;
    this.error = '';
    this.build();
  }

  build() {
    this.svg = sv('svg', { id: 'layout-canvas', class: 'eh-canvas', role: 'group', 'aria-label': 'Screen layout canvas. Tab to a computer, then use the arrow keys to move it.', preserveAspectRatio: 'xMidYMid meet' });
    this.unplaced = h('div.eh-unplaced', { id: 'layout-unplaced' });
    this.summary = h('div.eh-bsum', { id: 'layout-boundaries', 'aria-live': 'polite' });
    this.saveBtn = h('button.btn.btn-sm.btn-primary', { type: 'button', id: 'layout-save', disabled: true, onclick: () => this.save() }, 'Save layout');
    this.revertBtn = h('button.btn.btn-sm', { type: 'button', id: 'layout-revert', disabled: true, onclick: () => this.revert() }, 'Revert');
    this.autoBtn = h('button.btn.btn-sm', { type: 'button', id: 'layout-auto', onclick: () => this.autoArrange() }, 'Auto-arrange');
    this.hint = h('div.text-muted.text-sm', { id: 'layout-hint', role: 'status' });
    this.container.replaceChildren(
      h('div.card.stack.gap-3', {},
        h('div.card-header', {},
          h('div', {}, h('div.card-title', {}, 'Screen layout'),
            h('div.card-subtitle', { id: 'layout-help' }, 'Drag screens so their edges touch. The pointer moves to the other computer only across the highlighted edges. Monitors inside one computer keep the arrangement from that computer’s Display settings.')),
          h('div.row.gap-2.shrink-0', {}, this.autoBtn, this.revertBtn, this.saveBtn)),
        h('div.row.gap-2.wrap.items-center', {}, h('span.text-sm.weight-500', {}, 'Not placed:'), this.unplaced),
        h('div.eh-canvas-wrap', {}, this.svg),
        this.summary,
        h('div.row.gap-3.wrap.items-center', {},
          h('div.eh-legend', {}, h('span.is-boundary', {}, 'Pointer crosses here'), h('span.is-active', {}, 'Has the pointer now'), h('span.is-offline', {}, 'Offline')),
          this.hint)));
    this.svg.addEventListener('pointerdown', (e) => this.onPointerDown(e));
    this.svg.addEventListener('keydown', (e) => this.onKey(e));
    if (typeof ResizeObserver !== 'undefined') {
      let last = '';
      new ResizeObserver(() => {
        const key = `${this.svg.clientWidth}x${this.svg.clientHeight}`;
        if (key !== last && !this.drag) { last = key; this.renderCanvas(); }
      }).observe(this.svg);
    }
  }

  /** Update from an edgehop.state payload. Keeps unsaved edits. */
  setState(state) {
    if (this.drag) { this.pendingState = state; return; }
    this.pendingState = null;
    const self = { ...state.self, self: true };
    this.machines = [self, ...state.peers.filter((p) => p.paired || p.status === 'online')];
    let incoming;
    if (state.layout?.machines) incoming = new Map(state.layout.machines.map((p) => [p.id, { x: p.x | 0, y: p.y | 0 }]));
    else incoming = legacyToPositions(this.machines, state.layout?.tiles); // old config: spec §4
    this.saved = incoming;
    if (!this.dirty) this.pos = new Map([...incoming].map(([id, p]) => [id, { ...p }]));
    const known = new Set(this.machines.map((m) => m.id));
    for (const id of [...this.pos.keys()]) if (!known.has(id)) this.pos.delete(id);
    this.render();
  }

  machine(id) { return this.machines.find((m) => m.id === id); }
  isOnline(m) { return m.self || m.status === 'online'; }

  render() {
    this.renderUnplaced();
    this.renderCanvas();
    this.saveBtn.disabled = !this.dirty;
    this.revertBtn.disabled = !this.dirty;
    this.renderHint();
  }

  renderHint() {
    this.hint.textContent = this.error || (this.dirty ? 'Unsaved changes.' : '');
    this.hint.dataset.dirty = String(this.dirty);
    this.hint.dataset.error = String(!!this.error);
  }

  renderUnplaced() {
    const rest = this.machines.filter((m) => !this.pos.has(m.id));
    this.unplaced.replaceChildren(...rest.map((m) => h('span.eh-chip', { dataset: { id: m.id } },
      m.name || m.id.slice(0, 8),
      h('button.btn.btn-sm', { type: 'button', dataset: { action: 'place', id: m.id }, 'aria-label': `Place ${m.name}`, onclick: () => this.place(m.id) }, 'Place'))));
    if (!rest.length) this.unplaced.append(h('span.text-faint.text-sm', {}, 'everything is placed'));
  }

  worldBounds() {
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const m of this.machines) {
      const p = this.pos.get(m.id);
      if (!p) continue;
      const rs = rectsOf(m, p);
      for (const r of rs.length ? rs : [{ x: p.x, y: p.y, w: PLACEHOLDER.w, h: PLACEHOLDER.h }]) {
        x0 = Math.min(x0, r.x); y0 = Math.min(y0, r.y); x1 = Math.max(x1, r.x + r.w); y1 = Math.max(y1, r.y + r.h);
      }
    }
    if (x0 === Infinity) return { x: 0, y: 0, w: PLACEHOLDER.w, h: PLACEHOLDER.h };
    return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
  }

  /** Redraw the whole canvas: auto-fit, one group per placed machine, then the boundary layer. */
  renderCanvas() {
    const W = this.svg.clientWidth || 720, H = this.svg.clientHeight || 360;
    const b = this.worldBounds();
    const pad = Math.max(b.w, b.h) * 0.3 + 200;
    const scale = Math.min(W / (b.w + 2 * pad), H / (b.h + 2 * pad));
    const vw = W / scale, vh = H / scale;
    this.scale = scale;
    this.svg.setAttribute('viewBox', `${b.x + b.w / 2 - vw / 2} ${b.y + b.h / 2 - vh / 2} ${vw} ${vh}`);
    this.svg.dataset.scale = scale.toFixed(6);
    const fs = 12 / scale, pd = 8 / scale;
    const nodes = [];
    this.machines.forEach((m, mi) => {
      const p = this.pos.get(m.id);
      if (!p) return;
      const online = this.isOnline(m);
      const g = sv('g', {
        class: ['eh-node', m.active && 'is-active', !online && 'is-offline', m.self && 'is-self'].filter(Boolean).join(' '),
        'data-id': m.id, transform: `translate(${p.x} ${p.y})`, tabindex: 0, role: 'button',
        'aria-label': `${m.name}, ${online ? 'online' : 'offline'}${m.self ? `, ${platLabel(m.plat)}` : ''}. Arrow keys move it 10 units, Shift plus arrow 100.`,
      });
      g.style.setProperty('--mc', `hsl(${hueOf(mi)} 62% 52%)`);
      const rs = (m.displays || []).filter((d) => d.w > 0 && d.h > 0);
      const list = rs.length ? rs : [{ ...PLACEHOLDER, placeholder: true }];
      list.forEach((d, i) => {
        const r = { idx: i, primary: d.primary };
        g.append(sv('rect', { class: `eh-d${d.primary ? ' is-primary' : ''}${d.placeholder ? ' is-placeholder' : ''}`, x: d.x, y: d.y, width: d.w, height: d.h, rx: 4 / scale, 'data-primary': d.primary ? 'true' : null }));
        const cid = `eh-clip-${mi}-${i}`;
        g.append(sv('clipPath', { id: cid }, sv('rect', { x: d.x, y: d.y, width: d.w, height: d.h })));
        const cap = d.placeholder ? 'No display info yet' : `${dispName(r)} · ${d.w}×${d.h}`;
        if (i === 0) {
          const status = `${online ? 'online' : 'offline'}${m.self ? ` · ${platLabel(m.plat)}` : ''}`;
          g.append(sv('text', { class: 'eh-name', x: d.x + pd, y: d.y + pd + fs, 'font-size': fs * 1.15, 'clip-path': `url(#${cid})` }, document.createTextNode(m.name || m.id.slice(0, 8))));
          g.append(sv('text', { class: 'eh-sub', x: d.x + pd, y: d.y + pd + fs * 2.6, 'font-size': fs * 0.95, 'clip-path': `url(#${cid})` }, document.createTextNode(status)));
        }
        g.append(sv('text', { class: 'eh-cap', x: d.x + pd, y: d.y + d.h - pd, 'font-size': fs * 0.85, 'clip-path': `url(#${cid})` }, document.createTextNode(cap)));
      });
      nodes.push(g);
    });
    this.layer = sv('g', { class: 'eh-bounds', 'pointer-events': 'none' });
    this.svg.replaceChildren(...nodes, this.layer);
    this.drawBoundaries();
  }

  /** Thick lines on touching edges + the text summary. Cheap enough to run on every drag move. */
  drawBoundaries() {
    const segs = findBoundaries(this.machines, this.pos);
    this.layer.replaceChildren(...segs.map((s) => sv('line', {
      class: 'eh-boundary', x1: s.x0, y1: s.y0, x2: s.x1, y2: s.y1,
      'data-a': s.a, 'data-b': s.b, 'data-edge': s.edge, 'data-len': Math.round(Math.hypot(s.x1 - s.x0, s.y1 - s.y0)),
    })));
    this.segs = segs;
    const lines = [];
    const pairs = new Map();
    for (const s of segs) (pairs.get(`${s.a}|${s.b}`) || pairs.set(`${s.a}|${s.b}`, []).get(`${s.a}|${s.b}`)).push(s);
    for (const list of pairs.values()) {
      const na = this.machine(list[0].a)?.name, nb = this.machine(list[0].b)?.name;
      const what = list.map((s) => `${s.edge} edge of ${dispName(s.da)}`).join('; ');
      lines.push(`${na} ↔ ${nb}: ${list.length} ${list.length === 1 ? 'boundary' : 'boundaries'} (${what})`);
    }
    const placed = this.machines.filter((m) => this.pos.has(m.id)).length;
    if (!lines.length) lines.push(placed > 1 ? 'No touching edges yet: the pointer cannot move between computers. Drag screens so their edges touch.' : '');
    this.summary.replaceChildren(...lines.filter(Boolean).map((t) => h('div.eh-bsum-line', {}, t)));
    this.summary.dataset.count = String(segs.length);
  }

  // ---------------------------------------------------------- editing

  markDirty() {
    this.dirty = !this.sameAsSaved();
    this.render();
  }

  sameAsSaved() {
    if (this.pos.size !== this.saved.size) return false;
    for (const [id, p] of this.pos) {
      const s = this.saved.get(id);
      if (!s || s.x !== p.x || s.y !== p.y) return false;
    }
    return true;
  }

  revert() {
    this.pos = new Map([...this.saved].map(([id, p]) => [id, { ...p }]));
    this.dirty = false;
    this.error = '';
    this.render();
  }

  /** Put `id` touching the right of the rightmost placed machine, top aligned with it. */
  place(id) {
    const m = this.machine(id);
    if (!m) return;
    this.pos.delete(id);
    let rightmost = null, right = -Infinity;
    for (const o of this.machines) {
      const p = this.pos.get(o.id);
      if (!p) continue;
      const u = unionOrDefault(o);
      if (p.x + u.x + u.w > right) { right = p.x + u.x + u.w; rightmost = { top: p.y + u.y }; }
    }
    const u = unionOrDefault(m);
    this.pos.set(id, rightmost ? { x: right - u.x, y: rightmost.top - u.y } : { x: 0, y: 0 });
    this.error = '';
    this.markDirty();
    this.focusNode(id);
  }

  /** Left to right in machine order (this machine first), touching, tops aligned at y = 0. */
  autoArrange() {
    this.pos = new Map();
    let cx = 0;
    for (const m of this.machines) {
      const u = unionOrDefault(m);
      this.pos.set(m.id, { x: cx - u.x, y: -u.y });
      cx += u.w;
    }
    this.error = '';
    this.markDirty();
  }

  layout() {
    return { machines: this.machines.filter((m) => this.pos.has(m.id)).map((m) => ({ id: m.id, x: this.pos.get(m.id).x, y: this.pos.get(m.id).y })) };
  }

  async save() {
    if (!this.onSave) return;
    const bad = findOverlap(this.machines, this.pos);
    if (bad) { this.error = `${bad[0].name} and ${bad[1].name} overlap. Move them apart first.`; this.renderHint(); return; }
    this.saveBtn.disabled = true;
    try {
      await this.onSave(this.layout());
      this.saved = new Map([...this.pos].map(([id, p]) => [id, { ...p }]));
      this.dirty = false;
      this.error = '';
      this.render();
      toast('Layout saved and shared with your machines', { kind: 'success' });
    } catch (e) {
      this.saveBtn.disabled = false;
      toast(e.message || 'Could not save the layout', { kind: 'danger' });
    }
  }

  // ------------------------------------------------------------- snap

  /** Snap machine `m` at (x, y): opposite edges within SNAP_PX meet exactly, then aligned tops/bottoms or lefts/rights. */
  snap(m, x, y) {
    const tol = SNAP_PX / this.scale;
    const others = this.machines.filter((o) => o.id !== m.id && this.pos.has(o.id)).flatMap((o) => rectsOf(o, this.pos.get(o.id)));
    const mine = rectsOf(m, { x, y });
    const best = (cur, d) => (Math.abs(d) <= tol && (cur === null || Math.abs(d) < Math.abs(cur)) ? d : cur);
    let dx = null, dy = null;
    for (const b of others) {
      for (const a of mine) {
        const yNear = a.y < b.y + b.h + tol && b.y < a.y + a.h + tol;
        const xNear = a.x < b.x + b.w + tol && b.x < a.x + a.w + tol;
        if (yNear) { dx = best(dx, b.x - (a.x + a.w)); dx = best(dx, b.x + b.w - a.x); }
        if (xNear) { dy = best(dy, b.y - (a.y + a.h)); dy = best(dy, b.y + b.h - a.y); }
      }
    }
    const snappedX = dx !== null, snappedY = dy !== null;
    x += dx || 0; y += dy || 0;
    // Aligned edges for pairs that now touch.
    const moved = rectsOf(m, { x, y });
    let ax = null, ay = null;
    for (const b of others) {
      for (const a of moved) {
        const yOv = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
        const xOv = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
        const touchH = yOv >= 1 && (Math.abs(a.x + a.w - b.x) <= TOUCH_UNITS || Math.abs(a.x - (b.x + b.w)) <= TOUCH_UNITS);
        const touchV = xOv >= 1 && (Math.abs(a.y + a.h - b.y) <= TOUCH_UNITS || Math.abs(a.y - (b.y + b.h)) <= TOUCH_UNITS);
        if (touchH && !snappedY) { ay = best(ay, b.y - a.y); ay = best(ay, b.y + b.h - (a.y + a.h)); }
        if (touchV && !snappedX) { ax = best(ax, b.x - a.x); ax = best(ax, b.x + b.w - (a.x + a.w)); }
      }
    }
    return { x: Math.round(x + (ax || 0)), y: Math.round(y + (ay || 0)) };
  }

  invalidFor(id) {
    const bad = findOverlap(this.machines, this.pos);
    return bad && bad.some((m) => m.id === id) ? bad : null;
  }

  // ------------------------------------------------------------- drag

  onPointerDown(e) {
    const node = e.target.closest?.('.eh-node');
    if (!node || e.button !== 0) return;
    e.preventDefault();
    const id = node.dataset.id;
    const m = this.machine(id);
    const p = this.pos.get(id);
    if (!m || !p) return;
    node.focus();
    this.error = '';
    this.drag = { id, node, m, start: { ...p }, px: e.clientX, py: e.clientY, moved: false };
    node.setPointerCapture?.(e.pointerId);
    node.classList.add('is-dragging');
    const move = (ev) => this.onPointerMove(ev);
    const up = (ev) => { node.removeEventListener('pointermove', move); node.removeEventListener('pointerup', up); node.removeEventListener('pointercancel', up); this.onPointerUp(ev); };
    node.addEventListener('pointermove', move);
    node.addEventListener('pointerup', up);
    node.addEventListener('pointercancel', up);
  }

  onPointerMove(e) {
    const d = this.drag;
    if (!d) return;
    if (Math.abs(e.clientX - d.px) + Math.abs(e.clientY - d.py) > 3) d.moved = true;
    if (!d.moved) return;
    const rx = Math.round(d.start.x + (e.clientX - d.px) / this.scale);
    const ry = Math.round(d.start.y + (e.clientY - d.py) / this.scale);
    const s = this.snap(d.m, rx, ry);
    this.pos.set(d.id, s);
    d.node.setAttribute('transform', `translate(${s.x} ${s.y})`);
    d.node.classList.toggle('is-invalid', !!this.invalidFor(d.id));
    this.drawBoundaries();
  }

  onPointerUp() {
    const d = this.drag;
    if (!d) return;
    this.drag = null;
    if (d.moved) {
      const bad = this.invalidFor(d.id);
      if (bad) {
        this.pos.set(d.id, d.start); // revert on drop
        this.error = `${bad[0].name} and ${bad[1].name} would overlap, so the screen went back.`;
      }
      this.dirty = !this.sameAsSaved();
    }
    const pending = this.pendingState;
    if (pending) this.setState(pending); else this.render();
    this.focusNode(d.id);
  }

  focusNode(id) {
    this.svg.querySelector(`.eh-node[data-id="${CSS.escape(id)}"]`)?.focus();
  }

  // --------------------------------------------------------- keyboard

  onKey(e) {
    const node = e.target.closest?.('.eh-node');
    if (!node) return;
    const id = node.dataset.id;
    const p = this.pos.get(id);
    const dir = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
    if (!dir || !p) return;
    e.preventDefault();
    const step = e.shiftKey ? 100 : 10;
    const next = { x: p.x + dir[0] * step, y: p.y + dir[1] * step };
    this.pos.set(id, next);
    const bad = this.invalidFor(id);
    if (bad) {
      this.pos.set(id, p);
      this.error = `${bad[0].name} and ${bad[1].name} would overlap, so it did not move.`;
      this.renderHint();
      return;
    }
    this.error = '';
    this.markDirty();
    this.focusNode(id);
  }
}
