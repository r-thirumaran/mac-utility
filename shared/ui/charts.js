// charts.js — small canvas charts for live system data (Vitals and friends).
//
//   import { Sparkline, LineChart, RingGauge, BarMeter, formatBytes } from './shared/charts.js';
//   const cpu = new Sparkline(canvas, { max: 100 });
//   setInterval(() => cpu.push(sample.cpu.total), 1000);
//
// Every chart is devicePixelRatio-aware, resizes itself with a ResizeObserver,
// coalesces redraws onto one requestAnimationFrame, and reads its colors from CSS
// custom properties (--chart-1 … --chart-8, --chart-grid, --accent, …) at draw time
// so an app can re-theme by overriding variables. Series data lives in typed-array
// ring buffers; the draw loops allocate nothing.

// ----------------------------------------------------------- draw scheduler

let pendingSet = new Set();
let drawingSet = new Set();
let rafId = 0;
const live = new Set();

function flush() {
  rafId = 0;
  const batch = pendingSet;
  pendingSet = drawingSet;
  drawingSet = batch;
  for (const chart of batch) chart._draw();
  batch.clear();
}

function schedule(chart) {
  pendingSet.add(chart);
  if (!rafId) rafId = requestAnimationFrame(flush);
}

// ----------------------------------------------------------- theme tracking

function bumpTheme() {
  for (const chart of live) {
    chart._themeStale = true;
    schedule(chart);
  }
}

if (typeof window !== 'undefined' && typeof matchMedia === 'function') {
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', bumpTheme);
  const watchDpr = () => {
    const mql = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
    mql.addEventListener('change', () => { for (const chart of live) chart.resize(); watchDpr(); }, { once: true });
  };
  watchDpr();
  if (typeof MutationObserver === 'function' && document.documentElement) {
    new MutationObserver(bumpTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class', 'style'] });
  }
}

/**
 * Resolve a color option to something a canvas can use. Accepts a CSS color,
 * a custom-property name ('--chart-2') or a var() expression.
 * @param {CSSStyleDeclaration} cs computed style of the chart element
 * @param {string|undefined} color the option as given
 * @param {string} fallbackVar custom property used when `color` is empty
 * @returns {string}
 */
function resolveColor(cs, color, fallbackVar) {
  if (!color) return readVar(cs, fallbackVar, '#2a78d6');
  if (color.startsWith('--')) return readVar(cs, color, '#2a78d6');
  const m = /^var\((--[\w-]+)\s*(?:,\s*(.+))?\)$/.exec(color);
  if (m) return readVar(cs, m[1], m[2] || '#2a78d6');
  return color;
}

function readVar(cs, name, fallback) {
  const v = cs.getPropertyValue(name).trim();
  return v || fallback;
}

function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }

/** Round `v` up to a "nice" axis maximum (1, 2, 2.5, 5 × 10^k). */
function niceCeil(v) {
  if (!(v > 0)) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = Math.pow(10, exp);
  const m = v / base;
  const nice = m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10;
  return nice * base;
}

function compactNumber(v) {
  const a = Math.abs(v);
  if (a >= 1e9) return `${(v / 1e9).toFixed(a < 1e10 ? 1 : 0)}B`;
  if (a >= 1e6) return `${(v / 1e6).toFixed(a < 1e7 ? 1 : 0)}M`;
  if (a >= 1e3) return `${(v / 1e3).toFixed(a < 1e4 ? 1 : 0)}K`;
  if (a >= 100 || Number.isInteger(v)) return String(Math.round(v));
  return v.toFixed(a < 10 ? 1 : 0);
}

function el(tag, className) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  return node;
}

// -------------------------------------------------------------- ring buffer

/** Fixed-capacity ring buffer of numbers (oldest → newest). */
class Ring {
  constructor(capacity) {
    this.cap = Math.max(2, capacity | 0);
    this.buf = new Float64Array(this.cap);
    this.head = 0; // next write position
    this.len = 0;
  }
  push(v) {
    this.buf[this.head] = v;
    this.head = (this.head + 1) % this.cap;
    if (this.len < this.cap) this.len++;
  }
  /** i = 0 is the oldest retained sample. */
  at(i) { return this.buf[(this.head - this.len + i + this.cap) % this.cap]; }
  get last() { return this.len ? this.buf[(this.head - 1 + this.cap) % this.cap] : NaN; }
  max() { let m = -Infinity; for (let i = 0; i < this.len; i++) { const v = this.at(i); if (v > m) m = v; } return m; }
  min() { let m = Infinity; for (let i = 0; i < this.len; i++) { const v = this.at(i); if (v < m) m = v; } return m; }
  clear() { this.head = 0; this.len = 0; }
  toArray() { const out = new Array(this.len); for (let i = 0; i < this.len; i++) out[i] = this.at(i); return out; }
}

// ------------------------------------------------------------- base class

/** Shared plumbing: DPR-aware backing store, ResizeObserver, theme cache, rAF draws. */
class CanvasChart {
  constructor(canvas, opts = {}) {
    if (!(canvas instanceof HTMLCanvasElement)) throw new TypeError('charts: expected a <canvas>');
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.opts = opts;
    this.w = 0;
    this.h = 0;
    this.dpr = 1;
    this.theme = {};
    this._themeStale = true;
    this._destroyed = false;
    this._ro = typeof ResizeObserver === 'function' ? new ResizeObserver(() => this.resize()) : null;
    if (this._ro) this._ro.observe(canvas);
    live.add(this);
    this.resize();
  }

  /** Re-measure the canvas and resize the backing store for the current devicePixelRatio. */
  resize() {
    if (this._destroyed) return;
    const dpr = window.devicePixelRatio || 1;
    const w = this.canvas.clientWidth;
    const h = this.canvas.clientHeight;
    if (w !== this.w || h !== this.h || dpr !== this.dpr) {
      this.w = w;
      this.h = h;
      this.dpr = dpr;
      this.canvas.width = Math.max(1, Math.round(w * dpr));
      this.canvas.height = Math.max(1, Math.round(h * dpr));
    }
    schedule(this);
  }

  /** Ask for a redraw on the next animation frame (calls coalesce). */
  requestDraw() { if (!this._destroyed) schedule(this); }

  /** Release observers and DOM. The canvas itself is left in place. */
  destroy() {
    this._destroyed = true;
    if (this._ro) this._ro.disconnect();
    live.delete(this);
    pendingSet.delete(this);
    drawingSet.delete(this);
  }

  _refreshTheme() {
    if (!this._themeStale) return;
    this._themeStale = false;
    const cs = getComputedStyle(this.canvas);
    const t = this.theme;
    t.font = readVar(cs, '--font-sans', 'system-ui, sans-serif');
    t.surface = readVar(cs, '--chart-surface', readVar(cs, '--surface', '#fff'));
    t.grid = readVar(cs, '--chart-grid', '#e5e5ea');
    t.axis = readVar(cs, '--chart-axis', '#c7c7cc');
    t.text = readVar(cs, '--chart-text', readVar(cs, '--text-muted', '#6e6e73'));
    t.ink = readVar(cs, '--text', '#1d1d1f');
    t.muted = readVar(cs, '--chart-muted', '#898781');
    t.accent = readVar(cs, '--accent', '#0b6bd9');
    t.warning = readVar(cs, '--warning', '#fab219');
    t.danger = readVar(cs, '--danger', '#d03b3b');
    this._readTheme(cs);
  }

  _readTheme() {}

  _draw() {
    if (this._destroyed || this.w < 2 || this.h < 2) return;
    this._refreshTheme();
    const ctx = this.ctx;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.clearRect(0, 0, this.w, this.h);
    this.render(ctx, this.w, this.h);
  }

  render() {}
}

// ---------------------------------------------------------------- Sparkline

/**
 * A tiny area/line trend with no axes, for stat tiles and table cells.
 *
 * @example
 *   const sp = new Sparkline(canvas, { max: 100 });
 *   sp.push(42);
 */
export class Sparkline extends CanvasChart {
  /**
   * @param {HTMLCanvasElement} canvas
   * @param {object} [opts]
   * @param {number} [opts.max] fixed top of the scale; omit to autoscale
   * @param {number} [opts.min=0] bottom of the scale
   * @param {string} [opts.color] CSS color or custom property ('--chart-2'); default --chart-1
   * @param {boolean} [opts.fill=true] draw a 12 % area wash under the line
   * @param {number} [opts.seconds] window length in seconds (capacity = seconds × 1000 / interval)
   * @param {number} [opts.interval=1000] expected ms between pushes
   * @param {number} [opts.capacity=120] ring-buffer size when `seconds` is not given
   * @param {number} [opts.lineWidth=1.5]
   * @param {boolean} [opts.marker=true] dot on the newest sample
   */
  constructor(canvas, opts = {}) {
    super(canvas, opts);
    const interval = opts.interval || 1000;
    const cap = opts.capacity || (opts.seconds ? Math.round((opts.seconds * 1000) / interval) : 120);
    this.ring = new Ring(cap);
    this.max = opts.max;
    this.min = opts.min ?? 0;
    this.fill = opts.fill ?? true;
    this.lineWidth = opts.lineWidth ?? 1.5;
    this.marker = opts.marker ?? true;
    this.pushes = 0;
  }

  /** Append a sample (the oldest is dropped once the buffer is full) and redraw. */
  push(value) {
    this.ring.push(Number.isFinite(+value) ? +value : 0);
    this.pushes++;
    this.requestDraw();
  }

  /** The newest sample, or NaN when empty. */
  get last() { return this.ring.last; }

  /** Copy of the retained samples, oldest first. */
  values() { return this.ring.toArray(); }

  /** Drop every sample. */
  clear() { this.ring.clear(); this.requestDraw(); }

  _readTheme(cs) {
    this.theme.color = resolveColor(cs, this.opts.color, '--chart-1');
    if (typeof this.fill === 'string') this.theme.fill = resolveColor(cs, this.fill, '--chart-1');
  }

  render(ctx, w, h) {
    const r = this.ring;
    const t = this.theme;
    const rad = this.marker ? (h < 40 ? 3 : 4) : 0;
    const pad = rad + 2;
    const plotW = w - pad * 2;
    const plotH = h - pad * 2;
    if (r.len < 2 || plotW <= 0 || plotH <= 0) {
      ctx.strokeStyle = t.grid;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(pad, h - pad + 0.5);
      ctx.lineTo(w - pad, h - pad + 0.5);
      ctx.stroke();
      return;
    }
    const min = this.min;
    let max = this.max;
    if (max == null) {
      max = r.max();
      max = max <= min ? min + 1 : min + (max - min) * 1.08;
    }
    const span = max - min || 1;
    const step = plotW / (r.cap - 1);
    const offset = r.cap - r.len; // newest sample sits on the right edge
    const baseY = pad + plotH;
    const x0 = pad + offset * step;
    const color = t.color;

    if (this.fill) {
      ctx.beginPath();
      ctx.moveTo(x0, baseY);
      for (let i = 0; i < r.len; i++) {
        const y = pad + plotH * (1 - clamp((r.at(i) - min) / span, 0, 1));
        ctx.lineTo(pad + (offset + i) * step, y);
      }
      ctx.lineTo(pad + (offset + r.len - 1) * step, baseY);
      ctx.closePath();
      ctx.fillStyle = typeof this.fill === 'string' ? t.fill : color;
      ctx.globalAlpha = 0.12;
      ctx.fill();
      ctx.globalAlpha = 1;
    }

    ctx.beginPath();
    for (let i = 0; i < r.len; i++) {
      const x = pad + (offset + i) * step;
      const y = pad + plotH * (1 - clamp((r.at(i) - min) / span, 0, 1));
      if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = color;
    ctx.lineWidth = this.lineWidth;
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
    ctx.stroke();

    if (rad) {
      const x = pad + (offset + r.len - 1) * step;
      const y = pad + plotH * (1 - clamp((r.last - min) / span, 0, 1));
      ctx.beginPath();
      ctx.arc(x, y, rad + 2, 0, Math.PI * 2);
      ctx.fillStyle = t.surface;
      ctx.fill();
      ctx.beginPath();
      ctx.arc(x, y, rad, 0, Math.PI * 2);
      ctx.fillStyle = color;
      ctx.fill();
    }
  }
}

// ---------------------------------------------------------------- LineChart

/**
 * Multi-series rolling line chart with hairline grid, y-axis ticks, a DOM legend
 * (click a key to hide/show that series) and a crosshair tooltip.
 *
 * @example
 *   const chart = new LineChart(canvas, { series: [{ name: 'User' }, { name: 'System' }], max: 100 });
 *   chart.push([12, 4]);
 */
export class LineChart extends CanvasChart {
  /**
   * @param {HTMLCanvasElement} canvas should sit inside a `.chart` box (position: relative)
   * @param {object} [opts]
   * @param {{name: string, color?: string}[]} [opts.series] one entry per value pushed; colors default to --chart-N
   * @param {number} [opts.max] fixed top of the scale; omit to autoscale to a nice ceiling
   * @param {number} [opts.min=0]
   * @param {number} [opts.window=120] number of samples kept (the visible width)
   * @param {number} [opts.interval=1000] ms between pushes, used for the time labels
   * @param {boolean} [opts.legend=true] render the legend after the canvas
   * @param {HTMLElement} [opts.legendEl] put the legend here instead
   * @param {boolean} [opts.tooltip=true] crosshair + tooltip on hover / keyboard focus
   * @param {boolean} [opts.grid=true]
   * @param {boolean} [opts.xAxis=true] "−2:00 … now" labels under the plot
   * @param {boolean|number} [opts.fill=false] area wash under series (true = all, or a series index)
   * @param {(v: number) => string} [opts.format] value formatter for ticks, legend and tooltip
   */
  constructor(canvas, opts = {}) {
    super(canvas, opts);
    const win = opts.window || 120;
    this.interval = opts.interval || 1000;
    this.max = opts.max;
    this.min = opts.min ?? 0;
    this.grid = opts.grid ?? true;
    this.xAxis = opts.xAxis ?? true;
    this.fill = opts.fill ?? false;
    this.format = opts.format || ((v) => compactNumber(v));
    this.series = (opts.series && opts.series.length ? opts.series : [{ name: 'Series 1' }]).map((s, i) => ({
      name: String(s.name ?? `Series ${i + 1}`),
      colorOpt: s.color || `--chart-${(i % 8) + 1}`,
      color: '#2a78d6',
      visible: true,
      ring: new Ring(win),
    }));
    this.pushes = 0;
    this.hover = -1;
    this._scaleMax = NaN;
    this._tickLabels = ['', '', ''];
    this._padL = 0;
    this._plotW = 0;
    this._buildDom();
    this._bindEvents();
  }

  /** Append one value per series (missing entries count as 0). */
  push(values) {
    const s = this.series;
    for (let i = 0; i < s.length; i++) {
      const v = values ? +values[i] : 0;
      s[i].ring.push(Number.isFinite(v) ? v : 0);
    }
    this.pushes++;
    this._legendDirty = true;
    this.requestDraw();
  }

  /** Show or hide a series by index (also toggled by clicking its legend key). */
  setVisible(index, visible) {
    const s = this.series[index];
    if (!s) return;
    s.visible = !!visible;
    if (s.key) s.key.setAttribute('aria-pressed', String(s.visible));
    this.requestDraw();
  }

  /** Drop all samples. */
  clear() { for (const s of this.series) s.ring.clear(); this.requestDraw(); }

  destroy() {
    super.destroy();
    const c = this.canvas;
    c.removeEventListener('pointermove', this._onMove);
    c.removeEventListener('pointerleave', this._onLeave);
    c.removeEventListener('keydown', this._onKey);
    c.removeEventListener('focus', this._onFocus);
    c.removeEventListener('blur', this._onLeave);
    if (this.legendEl && !this.opts.legendEl) this.legendEl.remove();
    if (this.tooltipEl) this.tooltipEl.remove();
  }

  _buildDom() {
    const canvas = this.canvas;
    if (this.opts.legend !== false) {
      const legend = this.opts.legendEl || el('div', 'chart-legend');
      legend.setAttribute('role', 'group');
      legend.setAttribute('aria-label', 'Series');
      for (let i = 0; i < this.series.length; i++) {
        const s = this.series[i];
        const key = el('button', 'chart-key');
        key.type = 'button';
        key.setAttribute('aria-pressed', 'true');
        const swatch = el('i');
        const name = el('span');
        name.textContent = s.name;
        const value = el('b');
        value.textContent = '–';
        key.append(swatch, name, value);
        key.addEventListener('click', () => this.setVisible(i, !s.visible));
        s.key = key;
        s.swatch = swatch;
        s.valueEl = value;
        legend.append(key);
      }
      if (!this.opts.legendEl) canvas.insertAdjacentElement('afterend', legend);
      this.legendEl = legend;
    }
    if (this.opts.tooltip !== false && canvas.parentElement) {
      const tip = el('div', 'chart-tooltip');
      tip.setAttribute('role', 'status');
      const time = el('div', 'chart-tooltip-time');
      tip.append(time);
      this.tooltipTime = time;
      for (const s of this.series) {
        const row = el('div', 'chart-tooltip-row');
        const sw = el('i');
        const name = el('span');
        name.textContent = s.name;
        const val = el('b');
        row.append(sw, name, val);
        s.tipRow = row;
        s.tipSwatch = sw;
        s.tipValue = val;
        tip.append(row);
      }
      canvas.parentElement.append(tip);
      this.tooltipEl = tip;
      if (!canvas.hasAttribute('tabindex')) canvas.tabIndex = 0;
      canvas.setAttribute('role', 'img');
      if (!canvas.hasAttribute('aria-label')) canvas.setAttribute('aria-label', this.series.map((s) => s.name).join(', '));
    }
  }

  _bindEvents() {
    const canvas = this.canvas;
    this._onMove = (e) => { this._setHover(this._indexAt(e.offsetX)); };
    this._onLeave = () => { this._setHover(-1); };
    this._onFocus = () => { const n = this.series[0].ring.len; if (n) this._setHover(n - 1); };
    this._onKey = (e) => {
      const n = this.series[0].ring.len;
      if (!n) return;
      let i = this.hover < 0 ? n - 1 : this.hover;
      if (e.key === 'ArrowLeft') i--; else if (e.key === 'ArrowRight') i++;
      else if (e.key === 'Home') i = 0; else if (e.key === 'End') i = n - 1;
      else if (e.key === 'Escape') { this._setHover(-1); return; }
      else return;
      e.preventDefault();
      this._setHover(clamp(i, 0, n - 1));
    };
    if (this.tooltipEl) {
      canvas.addEventListener('pointermove', this._onMove);
      canvas.addEventListener('pointerleave', this._onLeave);
      canvas.addEventListener('keydown', this._onKey);
      canvas.addEventListener('focus', this._onFocus);
      canvas.addEventListener('blur', this._onLeave);
    }
  }

  _indexAt(px) {
    const r = this.series[0].ring;
    if (!r.len || !(this._plotW > 0)) return -1;
    const step = this._plotW / (r.cap - 1);
    const offset = r.cap - r.len;
    const i = Math.round((px - this._padL) / step) - offset;
    return clamp(i, 0, r.len - 1);
  }

  _setHover(i) {
    if (i === this.hover) return;
    this.hover = i;
    this._tooltipDirty = true;
    this.requestDraw();
  }

  _readTheme(cs) {
    for (const s of this.series) {
      s.color = resolveColor(cs, s.colorOpt, '--chart-1');
      if (s.swatch) s.swatch.style.setProperty('--c', s.color);
      if (s.tipSwatch) s.tipSwatch.style.setProperty('--c', s.color);
    }
  }

  _updateScale(max) {
    if (max === this._scaleMax) return;
    this._scaleMax = max;
    const min = this.min;
    this._tickLabels[0] = this.format(max);
    this._tickLabels[1] = this.format(min + (max - min) / 2);
    this._tickLabels[2] = this.format(min);
    const ctx = this.ctx;
    ctx.font = `500 10px ${this.theme.font}`;
    let wMax = 0;
    for (let i = 0; i < 3; i++) wMax = Math.max(wMax, ctx.measureText(this._tickLabels[i]).width);
    this._padL = Math.ceil(wMax) + 8;
  }

  _updateLegend() {
    if (!this._legendDirty) return;
    this._legendDirty = false;
    for (const s of this.series) {
      if (s.valueEl) s.valueEl.textContent = s.ring.len ? this.format(s.ring.last) : '–';
    }
  }

  _updateTooltip(xPos, padT) {
    const tip = this.tooltipEl;
    if (!tip) return;
    if (this.hover < 0) {
      if (tip.classList.contains('is-visible')) tip.classList.remove('is-visible');
      return;
    }
    if (this._tooltipDirty || this._legendDirty) {
      this._tooltipDirty = false;
      const n = this.series[0].ring.len;
      const ago = (n - 1 - this.hover) * this.interval / 1000;
      this.tooltipTime.textContent = ago < 1 ? 'now' : `${formatDuration(ago)} ago`;
      for (const s of this.series) {
        s.tipRow.hidden = !s.visible;
        if (s.visible) s.tipValue.textContent = this.format(s.ring.at(this.hover));
      }
    }
    tip.classList.add('is-visible');
    const canvas = this.canvas;
    const left = canvas.offsetLeft + xPos;
    const tipW = tip.offsetWidth;
    const flip = left + 12 + tipW > canvas.offsetLeft + this.w;
    tip.style.left = `${flip ? left - 12 - tipW : left + 12}px`;
    tip.style.top = `${canvas.offsetTop + padT}px`;
  }

  render(ctx, w, h) {
    const t = this.theme;
    const series = this.series;
    const r0 = series[0].ring;
    const min = this.min;

    // scale
    let max = this.max;
    if (max == null) {
      let m = -Infinity;
      for (const s of series) if (s.visible) { const sm = s.ring.max(); if (sm > m) m = sm; }
      max = m > min ? min + niceCeil((m - min) * 1.05) : min + 1;
    }
    this._updateScale(max);
    this._updateLegend();
    const span = max - min || 1;

    const padT = 6;
    const padR = 8;
    const padB = this.xAxis ? 16 : 6;
    const padL = this._padL;
    const plotW = w - padL - padR;
    const plotH = h - padT - padB;
    this._plotW = plotW;
    if (plotW <= 10 || plotH <= 10) return;
    const bottom = padT + plotH;

    // grid + ticks
    ctx.font = `500 10px ${t.font}`;
    ctx.textBaseline = 'middle';
    ctx.textAlign = 'right';
    ctx.fillStyle = t.text;
    ctx.lineWidth = 1;
    for (let k = 0; k < 3; k++) {
      const y = Math.round(padT + plotH * (k / 2)) + 0.5;
      if (this.grid || k === 2) {
        ctx.strokeStyle = k === 2 ? t.axis : t.grid;
        ctx.beginPath();
        ctx.moveTo(padL, y);
        ctx.lineTo(w - padR, y);
        ctx.stroke();
      }
      ctx.fillText(this._tickLabels[k], padL - 6, y);
    }
    if (this.xAxis) {
      ctx.textBaseline = 'alphabetic';
      ctx.textAlign = 'left';
      ctx.fillText(this._windowLabel(r0.cap), padL, h - 3);
      ctx.textAlign = 'right';
      ctx.fillText('now', w - padR, h - 3);
    }

    if (!r0.len) return;
    const step = plotW / (r0.cap - 1);
    const offset = r0.cap - r0.len;
    const x0 = padL + offset * step;

    // area washes
    if (this.fill !== false) {
      ctx.globalAlpha = 0.10;
      for (let si = 0; si < series.length; si++) {
        const s = series[si];
        if (!s.visible || (this.fill !== true && this.fill !== si)) continue;
        ctx.beginPath();
        ctx.moveTo(x0, bottom);
        for (let i = 0; i < s.ring.len; i++) {
          ctx.lineTo(padL + (offset + i) * step, padT + plotH * (1 - clamp((s.ring.at(i) - min) / span, 0, 1)));
        }
        ctx.lineTo(padL + (offset + s.ring.len - 1) * step, bottom);
        ctx.closePath();
        ctx.fillStyle = s.color;
        ctx.fill();
      }
      ctx.globalAlpha = 1;
    }

    // lines
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
    for (const s of series) {
      if (!s.visible) continue;
      const r = s.ring;
      ctx.beginPath();
      for (let i = 0; i < r.len; i++) {
        const x = padL + (offset + i) * step;
        const y = padT + plotH * (1 - clamp((r.at(i) - min) / span, 0, 1));
        if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
      }
      ctx.strokeStyle = s.color;
      ctx.stroke();
    }

    // crosshair
    let hoverX = 0;
    if (this.hover >= 0 && this.hover < r0.len) {
      hoverX = padL + (offset + this.hover) * step;
      ctx.strokeStyle = t.axis;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(Math.round(hoverX) + 0.5, padT);
      ctx.lineTo(Math.round(hoverX) + 0.5, bottom);
      ctx.stroke();
    }

    // markers: newest sample always, plus the hovered sample
    for (const s of series) {
      if (!s.visible || !s.ring.len) continue;
      this._marker(ctx, padL + (offset + s.ring.len - 1) * step, padT + plotH * (1 - clamp((s.ring.last - min) / span, 0, 1)), s.color, 3.5);
      if (this.hover >= 0 && this.hover < s.ring.len - 1) {
        this._marker(ctx, hoverX, padT + plotH * (1 - clamp((s.ring.at(this.hover) - min) / span, 0, 1)), s.color, 4);
      }
    }
    this._updateTooltip(hoverX, padT);
  }

  _marker(ctx, x, y, color, rad) {
    ctx.beginPath();
    ctx.arc(x, y, rad + 2, 0, Math.PI * 2);
    ctx.fillStyle = this.theme.surface;
    ctx.fill();
    ctx.beginPath();
    ctx.arc(x, y, rad, 0, Math.PI * 2);
    ctx.fillStyle = color;
    ctx.fill();
  }

  _windowLabel(cap) {
    if (this._winLabelCap === cap) return this._winLabel;
    this._winLabelCap = cap;
    const secs = Math.round(cap * this.interval / 1000);
    this._winLabel = `−${secs >= 60 && secs % 60 === 0 ? `${secs / 60}m` : formatDuration(secs)}`;
    return this._winLabel;
  }
}

// ---------------------------------------------------------------- RingGauge

/**
 * A circular progress ring with a big value in the middle and a label beneath.
 *
 * @example
 *   const g = new RingGauge(canvas, { label: 'CPU', thresholds: { warning: 0.7, danger: 0.9 } });
 *   g.set(0.42);           // text defaults to "42%"
 *   g.set(0.42, '42 °C');
 */
export class RingGauge extends CanvasChart {
  /**
   * @param {HTMLCanvasElement} canvas square-ish; the ring fits the smaller side
   * @param {object} [opts]
   * @param {string} [opts.label] small caption under the value
   * @param {string} [opts.color] ring color (CSS color or custom property); default --accent
   * @param {number} [opts.thickness=8] ring stroke width in CSS px
   * @param {{warning?: number, danger?: number}} [opts.thresholds] switch the ring to the status
   *   colors when the fraction crosses these (severity lives in the fill, per the data-viz rules)
   */
  constructor(canvas, opts = {}) {
    super(canvas, opts);
    this.label = opts.label || '';
    this.thickness = opts.thickness || 8;
    this.thresholds = opts.thresholds || null;
    this.fraction = 0;
    this.text = '';
    this.sets = 0;
    canvas.setAttribute('role', 'img');
    this._syncLabel();
  }

  /**
   * Update the gauge.
   * @param {number} fraction 0 … 1
   * @param {string} [text] center text; defaults to the percentage
   */
  set(fraction, text) {
    this.fraction = clamp(Number.isFinite(+fraction) ? +fraction : 0, 0, 1);
    this.text = text ?? formatPercent(this.fraction);
    this.sets++;
    this._syncLabel();
    this.requestDraw();
  }

  _syncLabel() {
    this.canvas.setAttribute('aria-label', this.label ? `${this.label}: ${this.text}` : this.text);
  }

  _readTheme(cs) {
    this.theme.color = resolveColor(cs, this.opts.color, '--accent');
  }

  render(ctx, w, h) {
    const t = this.theme;
    const th = this.thickness;
    const cx = w / 2;
    const cy = h / 2;
    const radius = Math.min(w, h) / 2 - th / 2 - 1;
    if (radius <= 4) return;
    let color = t.color;
    const f = this.fraction;
    if (this.thresholds) {
      if (this.thresholds.danger != null && f >= this.thresholds.danger) color = t.danger;
      else if (this.thresholds.warning != null && f >= this.thresholds.warning) color = t.warning;
    }
    ctx.lineWidth = th;
    ctx.lineCap = 'round';
    ctx.strokeStyle = color;
    ctx.globalAlpha = 0.16;
    ctx.beginPath();
    ctx.arc(cx, cy, radius, 0, Math.PI * 2);
    ctx.stroke();
    ctx.globalAlpha = 1;
    if (f > 0) {
      const start = -Math.PI / 2;
      ctx.beginPath();
      ctx.arc(cx, cy, radius, start, start + Math.PI * 2 * f);
      ctx.stroke();
    }
    const inner = radius - th / 2;
    const valueSize = clamp(Math.round(inner * 0.5), 12, 40);
    ctx.fillStyle = t.ink;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.font = `600 ${valueSize}px ${t.font}`;
    const hasLabel = !!this.label && inner > 28;
    ctx.fillText(this.text, cx, hasLabel ? cy - valueSize * 0.22 : cy, inner * 1.7);
    if (hasLabel) {
      const labelSize = clamp(Math.round(inner * 0.22), 9, 13);
      ctx.font = `500 ${labelSize}px ${t.font}`;
      ctx.fillStyle = t.text;
      ctx.fillText(this.label, cx, cy + valueSize * 0.5 + labelSize * 0.3, inner * 1.7);
    }
  }
}

// ----------------------------------------------------------------- BarMeter

/**
 * A stacked horizontal bar (DOM, not canvas) for part-to-whole breakdowns such as
 * memory = app + wired + compressed + cached, with a legend that shows each value.
 *
 * @example
 *   const mem = new BarMeter(div, { segments: [{ name: 'App' }, { name: 'Wired' }], total: 16 * 2 ** 30 });
 *   mem.set([5.2 * 2 ** 30, 1.1 * 2 ** 30]);
 */
export class BarMeter {
  /**
   * @param {HTMLElement} container an empty block element; becomes `.barmeter`
   * @param {object} [opts]
   * @param {{name: string, color?: string}[]} [opts.segments] in stacking order; colors default to --chart-N
   * @param {number} [opts.total] fixed total (the unfilled remainder shows as track); default = sum of values
   * @param {boolean} [opts.legend=true]
   * @param {(v: number) => string} [opts.format=formatBytes] legend value formatter
   * @param {string} [opts.label] accessible name
   */
  constructor(container, opts = {}) {
    if (!(container instanceof HTMLElement)) throw new TypeError('BarMeter: expected a container element');
    this.el = container;
    this.opts = opts;
    this.total = opts.total;
    this.format = opts.format || ((v) => formatBytes(v));
    container.classList.add('barmeter');
    container.setAttribute('role', 'img');
    if (opts.label) container.setAttribute('aria-label', opts.label);
    this.track = el('div', 'barmeter-track');
    this.segments = (opts.segments || []).map((s, i) => {
      const seg = el('div', 'barmeter-seg');
      seg.style.setProperty('--c', s.color ? (s.color.startsWith('--') ? `var(${s.color})` : s.color) : `var(--chart-${(i % 8) + 1})`);
      seg.style.flexBasis = '0%';
      seg.hidden = true;
      this.track.append(seg);
      let key = null;
      let valueEl = null;
      if (opts.legend !== false) {
        key = el('span', 'chart-key is-rect');
        const sw = el('i');
        sw.style.setProperty('--c', seg.style.getPropertyValue('--c'));
        const name = el('span');
        name.textContent = String(s.name ?? `Segment ${i + 1}`);
        valueEl = el('b');
        valueEl.textContent = '–';
        key.append(sw, name, valueEl);
      }
      return { name: s.name, seg, key, valueEl, value: 0 };
    });
    container.append(this.track);
    if (opts.legend !== false) {
      this.legend = el('div', 'chart-legend');
      for (const s of this.segments) this.legend.append(s.key);
      container.append(this.legend);
    }
    this.sets = 0;
  }

  /**
   * Update the segment values (same order as `segments`).
   * @param {number[]} values
   * @param {number} [total] overrides the constructor total for this update
   */
  set(values, total) {
    const segs = this.segments;
    let sum = 0;
    for (let i = 0; i < segs.length; i++) {
      const v = Math.max(0, Number.isFinite(+values[i]) ? +values[i] : 0);
      segs[i].value = v;
      sum += v;
    }
    const denom = total ?? this.total ?? sum;
    let lastVisible = null;
    for (const s of segs) {
      const pct = denom > 0 ? (s.value / denom) * 100 : 0;
      s.seg.hidden = pct <= 0;
      s.seg.style.flexBasis = `${pct.toFixed(2)}%`;
      s.seg.classList.remove('is-last');
      if (!s.seg.hidden) lastVisible = s.seg;
      if (s.valueEl) s.valueEl.textContent = this.format(s.value);
    }
    if (lastVisible && sum >= denom) lastVisible.classList.add('is-last');
    this.sets++;
    if (!this.opts.label) {
      this.el.setAttribute('aria-label', segs.map((s) => `${s.name}: ${this.format(s.value)}`).join(', '));
    }
  }

  /** Present for API symmetry with the canvas charts; the DOM reflows itself. */
  resize() {}

  /** Remove the generated DOM. */
  destroy() {
    this.track.remove();
    if (this.legend) this.legend.remove();
    this.el.classList.remove('barmeter');
  }
}

// --------------------------------------------------------------- formatters

/**
 * Format a byte count: `formatBytes(1536)` → "1.50 KB", `formatBytes(2e9, {perSecond: true})` → "1.86 GB/s".
 * @param {number} n bytes
 * @param {object} [opts]
 * @param {boolean} [opts.perSecond=false] append "/s"
 * @param {boolean} [opts.decimal=false] use 1000-based units instead of 1024
 * @param {number} [opts.digits] fixed decimals; default adapts (0 for B, 2 below 10, 1 below 100)
 * @returns {string}
 */
export function formatBytes(n, { perSecond = false, decimal = false, digits } = {}) {
  if (!Number.isFinite(n)) return '–';
  const base = decimal ? 1000 : 1024;
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let v = Math.abs(n);
  let i = 0;
  while (v >= base && i < units.length - 1) { v /= base; i++; }
  const d = digits ?? (i === 0 ? 0 : v < 10 ? 2 : v < 100 ? 1 : 0);
  return `${n < 0 ? '-' : ''}${v.toFixed(d)} ${units[i]}${perSecond ? '/s' : ''}`;
}

/**
 * Format a fraction as a percentage: `formatPercent(0.4231)` → "42%", `formatPercent(0.042)` → "4.2%".
 * @param {number} x fraction in 0 … 1 (values above 1 are allowed)
 * @param {object} [opts]
 * @param {number} [opts.digits] fixed decimals; default 1 below 10 %, else 0
 * @returns {string}
 */
export function formatPercent(x, { digits } = {}) {
  if (!Number.isFinite(x)) return '–';
  const p = x * 100;
  const d = digits ?? (p > 0 && p < 10 ? 1 : 0);
  return `${p.toFixed(d)}%`;
}

/**
 * Format a duration in seconds: 45 → "45s", 125 → "2m 05s", 3900 → "1h 05m", 90000 → "1d 1h".
 * @param {number} sec seconds
 * @returns {string}
 */
export function formatDuration(sec) {
  if (!Number.isFinite(sec)) return '–';
  let s = Math.max(0, Math.round(sec));
  if (s < 60) return `${s}s`;
  let m = Math.floor(s / 60);
  s %= 60;
  if (m < 60) return `${m}m ${String(s).padStart(2, '0')}s`;
  let h = Math.floor(m / 60);
  m %= 60;
  if (h < 24) return `${h}h ${String(m).padStart(2, '0')}m`;
  const d = Math.floor(h / 24);
  h %= 24;
  return `${d}d ${h}h`;
}
