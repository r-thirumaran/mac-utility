// layout.js — the 5×5 screen-layout editor. Machines are tiles drawn as
// little monitors (their real display map, scaled); drag a tile onto a cell
// (pointer events, so mouse, touch and Playwright all work), or focus a tile
// and move it with the arrow keys. Save hands the tiles to edgehop.layout.set.
import { h, toast } from './shared/ui.js';

export const GRID = 5;

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
  const parts = displays.map((d) => `${d.w}×${d.h}`);
  return parts.length <= 2 ? parts.join(' + ') : `${parts.length} displays`;
}

/** The editor. `onSave(layout)` receives {updatedAt, tiles:[{id,col,row}]}. */
export class LayoutEditor {
  constructor(container, { onSave } = {}) {
    this.container = container;
    this.onSave = onSave;
    this.machines = [];       // [{id, name, status, active, displays, self}]
    this.tiles = new Map();   // id → {col, row}
    this.saved = new Map();   // the last layout from the backend
    this.dirty = false;
    this.drag = null;
    this.cells = [];
    this.build();
  }

  build() {
    this.grid = h('div.eh-grid', { role: 'grid', 'aria-label': 'Screen layout grid', id: 'layout-grid' });
    for (let row = 0; row < GRID; row++) {
      for (let col = 0; col < GRID; col++) {
        const cell = h('div.eh-cell', { role: 'gridcell', dataset: { col, row }, 'aria-label': `Column ${col + 1}, row ${row + 1}` });
        this.cells.push(cell);
        this.grid.append(cell);
      }
    }
    this.unplaced = h('div.eh-unplaced', { id: 'layout-unplaced' });
    this.saveBtn = h('button.btn.btn-sm.btn-primary', { type: 'button', id: 'layout-save', disabled: true, onclick: () => this.save() }, 'Save layout');
    this.revertBtn = h('button.btn.btn-sm', { type: 'button', id: 'layout-revert', disabled: true, onclick: () => this.revert() }, 'Revert');
    this.autoBtn = h('button.btn.btn-sm', { type: 'button', id: 'layout-auto', onclick: () => this.autoArrange() }, 'Auto-arrange');
    this.hint = h('div.text-muted.text-sm', { id: 'layout-hint' });
    this.container.replaceChildren(
      h('div.card.stack.gap-3', {},
        h('div.card-header', {},
          h('div', {}, h('div.card-title', {}, 'Screen layout'), h('div.card-subtitle', {}, 'Drag machines next to each other. The pointer crosses an edge where two tiles touch; the layout is shared with every machine.')),
          h('div.row.gap-2.shrink-0', {}, this.autoBtn, this.revertBtn, this.saveBtn)),
        this.grid,
        h('div.row.gap-2.wrap.items-center', {}, h('span.text-sm.weight-500', {}, 'Not placed:'), this.unplaced),
        h('div.eh-legend', {}, h('span.is-self', {}, 'This machine'), h('span.is-active', {}, 'Has the pointer now'), h('span.is-offline', {}, 'Offline'), h('span', {}, 'Online')),
        this.hint));
    this.grid.addEventListener('pointerdown', (e) => this.onPointerDown(e));
    this.unplaced.addEventListener('pointerdown', (e) => this.onPointerDown(e));
    this.container.addEventListener('keydown', (e) => this.onKey(e));
  }

  /** Update from an edgehop.state payload. Keeps unsaved edits. */
  setState(state) {
    const self = { ...state.self, self: true };
    this.machines = [self, ...state.peers.filter((p) => p.paired || p.status === 'online')];
    const incoming = new Map();
    for (const t of state.layout?.tiles || []) incoming.set(t.id, { col: t.col, row: t.row });
    this.saved = incoming;
    if (!this.dirty) this.tiles = new Map(incoming);
    const known = new Set(this.machines.map((m) => m.id));
    for (const id of [...this.tiles.keys()]) if (!known.has(id)) this.tiles.delete(id);
    this.render();
  }

  machine(id) { return this.machines.find((m) => m.id === id); }

  tileAt(col, row) {
    for (const [id, t] of this.tiles) if (t.col === col && t.row === row) return id;
    return null;
  }

  tileEl(m, { inGrid }) {
    const sub = m.self ? 'This machine' : (m.active ? 'Active' : m.status === 'online' ? displaySummary(m.displays) : 'Offline');
    return h('div.eh-tile', {
      dataset: { id: m.id }, tabIndex: 0, role: 'button',
      class: [m.active && 'is-active', m.status === 'offline' && 'is-offline', m.self && 'is-self'],
      'aria-label': `${m.name}${inGrid ? '' : ', not placed'}. Drag onto the grid or use the arrow keys.`,
      title: `${m.name} · ${displaySummary(m.displays)}`,
    }, screenGlyph(m), h('div.eh-tile-name', {}, m.name), h('div.eh-tile-sub', {}, sub));
  }

  render() {
    for (const cell of this.cells) cell.replaceChildren();
    const placed = new Set();
    for (const [id, t] of this.tiles) {
      const m = this.machine(id);
      const cell = this.cells[t.row * GRID + t.col];
      if (!m || !cell) continue;
      cell.append(this.tileEl(m, { inGrid: true }));
      placed.add(id);
    }
    this.unplaced.replaceChildren(...this.machines.filter((m) => !placed.has(m.id)).map((m) => this.tileEl(m, { inGrid: false })));
    if (!this.unplaced.children.length) this.unplaced.append(h('span.text-faint.text-sm', {}, 'everything is placed'));
    this.saveBtn.disabled = !this.dirty;
    this.revertBtn.disabled = !this.dirty;
    const selfPlaced = this.tiles.has(this.machines[0]?.id);
    this.hint.textContent = this.dirty ? 'Unsaved changes.' : (!selfPlaced ? 'Place this machine first, then drop the others beside it.' : '');
    this.hint.dataset.dirty = String(this.dirty);
  }

  // ---------------------------------------------------------- editing

  place(id, col, row) {
    const occupant = this.tileAt(col, row);
    const prev = this.tiles.get(id);
    if (occupant && occupant !== id) {
      if (prev) this.tiles.set(occupant, prev); else this.tiles.delete(occupant); // swap or bump out
    }
    this.tiles.set(id, { col, row });
    this.markDirty();
  }

  unplace(id) {
    if (!this.tiles.has(id)) return;
    this.tiles.delete(id);
    this.markDirty();
  }

  markDirty() {
    this.dirty = !this.sameAsSaved();
    this.render();
  }

  sameAsSaved() {
    if (this.tiles.size !== this.saved.size) return false;
    for (const [id, t] of this.tiles) {
      const s = this.saved.get(id);
      if (!s || s.col !== t.col || s.row !== t.row) return false;
    }
    return true;
  }

  revert() {
    this.tiles = new Map(this.saved);
    this.dirty = false;
    this.render();
  }

  /** Self in the middle, the others in a row to its right (then left, below, above). */
  autoArrange() {
    this.tiles = new Map();
    const [self, ...peers] = this.machines;
    if (!self) return;
    this.tiles.set(self.id, { col: 2, row: 2 });
    const spots = [[3, 2], [1, 2], [4, 2], [0, 2], [2, 3], [2, 1], [3, 3], [1, 3], [3, 1], [1, 1], [2, 4], [2, 0]];
    peers.forEach((p, i) => { if (spots[i]) this.tiles.set(p.id, { col: spots[i][0], row: spots[i][1] }); });
    this.markDirty();
  }

  layout() {
    return { updatedAt: 0, tiles: [...this.tiles].map(([id, t]) => ({ id, col: t.col, row: t.row })) };
  }

  async save() {
    if (!this.onSave) return;
    this.saveBtn.disabled = true;
    try {
      await this.onSave(this.layout());
      this.saved = new Map(this.tiles);
      this.dirty = false;
      this.render();
      toast('Layout saved and shared with your machines', { kind: 'success' });
    } catch (e) {
      this.saveBtn.disabled = false;
      toast(e.message || 'Could not save the layout', { kind: 'danger' });
    }
  }

  // ------------------------------------------------------------- drag

  onPointerDown(e) {
    const tile = e.target.closest('.eh-tile');
    if (!tile || e.button !== 0) return;
    e.preventDefault();
    const id = tile.dataset.id;
    const m = this.machine(id);
    if (!m) return;
    tile.focus();
    const ghost = h('div.eh-tile-ghost', {}, this.tileEl(m, { inGrid: true }));
    document.body.append(ghost);
    this.drag = { id, tile, ghost, over: null, moved: false, startX: e.clientX, startY: e.clientY };
    tile.classList.add('is-dragging');
    tile.setPointerCapture?.(e.pointerId);
    this.positionGhost(e);
    const move = (ev) => this.onPointerMove(ev);
    const up = (ev) => { this.onPointerUp(ev); tile.removeEventListener('pointermove', move); tile.removeEventListener('pointerup', up); tile.removeEventListener('pointercancel', up); };
    tile.addEventListener('pointermove', move);
    tile.addEventListener('pointerup', up);
    tile.addEventListener('pointercancel', up);
  }

  positionGhost(e) {
    this.drag.ghost.style.left = `${e.clientX}px`;
    this.drag.ghost.style.top = `${e.clientY}px`;
  }

  cellUnder(e) {
    this.drag.ghost.style.display = 'none';
    const el = document.elementFromPoint(e.clientX, e.clientY);
    this.drag.ghost.style.display = '';
    return el?.closest?.('.eh-cell') || null;
  }

  onPointerMove(e) {
    if (!this.drag) return;
    if (Math.abs(e.clientX - this.drag.startX) + Math.abs(e.clientY - this.drag.startY) > 3) this.drag.moved = true;
    this.positionGhost(e);
    const cell = this.cellUnder(e);
    if (cell !== this.drag.over) {
      this.drag.over?.classList.remove('is-over');
      cell?.classList.add('is-over');
      this.drag.over = cell;
    }
  }

  onPointerUp(e) {
    const d = this.drag;
    if (!d) return;
    this.drag = null;
    d.ghost.remove();
    d.tile.classList.remove('is-dragging');
    d.over?.classList.remove('is-over');
    if (!d.moved) return;
    const cell = this.cellUnder(e) || d.over;
    if (cell) this.place(d.id, +cell.dataset.col, +cell.dataset.row);
    else if (this.grid.contains(d.tile) && !this.grid.getBoundingClientRect || !this.inside(this.grid, e)) this.unplace(d.id);
    this.focusTile(d.id);
  }

  inside(el, e) {
    const r = el.getBoundingClientRect();
    return e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
  }

  focusTile(id) {
    this.container.querySelector(`.eh-tile[data-id="${CSS.escape(id)}"]`)?.focus();
  }

  // --------------------------------------------------------- keyboard

  onKey(e) {
    const tile = e.target.closest?.('.eh-tile');
    if (!tile) return;
    const id = tile.dataset.id;
    const cur = this.tiles.get(id);
    const delta = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
    if (delta) {
      e.preventDefault();
      if (!cur) { this.placeNearSelf(id); } else {
        const col = Math.min(GRID - 1, Math.max(0, cur.col + delta[0]));
        const row = Math.min(GRID - 1, Math.max(0, cur.row + delta[1]));
        if (col !== cur.col || row !== cur.row) this.place(id, col, row);
      }
      this.focusTile(id);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!cur) { this.placeNearSelf(id); this.focusTile(id); }
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault();
      this.unplace(id);
      this.focusTile(id);
    }
  }

  /** Put an unplaced machine on the first free cell next to this machine (or the centre). */
  placeNearSelf(id) {
    const self = this.machines[0];
    const s = self && this.tiles.get(self.id);
    if (!s) { this.place(id, 2, 2); return; }
    for (const [dc, dr] of [[1, 0], [-1, 0], [0, 1], [0, -1], [2, 0], [-2, 0]]) {
      const col = s.col + dc, row = s.row + dr;
      if (col < 0 || row < 0 || col >= GRID || row >= GRID) continue;
      if (!this.tileAt(col, row)) { this.place(id, col, row); return; }
    }
    for (let i = 0; i < GRID * GRID; i++) {
      if (!this.tileAt(i % GRID, Math.floor(i / GRID))) { this.place(id, i % GRID, Math.floor(i / GRID)); return; }
    }
  }
}
