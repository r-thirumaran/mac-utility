// overview.js — the Overview page: machine header, stat tiles with sparklines,
// per-core CPU chart, memory breakdown, GPU, network and disk charts. Every
// number comes from vitals.sample events (docs/ARCHITECTURE.md §5); the page
// builds its memory breakdown from whichever fields the platform fills in.
import { Sparkline, LineChart, RingGauge, BarMeter, formatBytes, formatDuration } from './shared/charts.js';
import { h, icon } from './shared/ui.js';
import { clamp } from './common.js';

const WINDOW_SEC = 120;

/** Segment definitions for the memory bar, in stacking order. */
const MEM_SEGMENTS = {
  app: { name: 'App memory', color: '--chart-1' },
  wired: { name: 'Wired', color: '--chart-2' },
  compressed: { name: 'Compressed', color: '--chart-4' },
  used: { name: 'In use', color: '--chart-1' },
  cached: { name: 'Cached', color: '--chart-seq-3' },
};

/**
 * Pick the memory segments a sample can fill: macOS reports app/wired/
 * compressed(+cached), Windows and Linux report used(+cached).
 * @param {object} mem
 * @returns {string[]} keys of MEM_SEGMENTS
 */
export function memSegmentKeys(mem) {
  if ((mem.app || 0) > 0 || (mem.wired || 0) > 0 || (mem.compressed || 0) > 0) {
    return ['app', 'wired', 'compressed', 'cached'].filter((k) => (mem[k] || 0) > 0);
  }
  const keys = ['used'];
  if ((mem.cached || 0) > 0) keys.push('cached');
  return keys;
}

/** Values for the chosen keys; cached is clamped so the bar never overflows. */
export function memSegmentValues(mem, keys) {
  let acc = 0;
  return keys.map((k) => {
    let v = Math.max(0, mem[k] || 0);
    if (k === 'cached' && mem.total) v = clamp(v, 0, Math.max(0, mem.total - acc));
    acc += v;
    return v;
  });
}

/** Compact byte rate for a stat tile: "91.9 KB/s", "1.2 MB/s", "0 B/s". */
function rateText(v) {
  return formatBytes(v || 0, { perSecond: true, digits: v >= 100 * 1024 || v < 1024 ? 0 : 1 });
}

function pressureBadge(level) {
  const map = { normal: ['badge-success', 'Normal'], warning: ['badge-warning', 'Elevated'], critical: ['badge-danger', 'Critical'] };
  const [cls, text] = map[level] || map.normal;
  return { cls, text };
}

export class Overview {
  /**
   * @param {HTMLElement} root the panel element
   */
  constructor(root) {
    this.root = root;
    this.info = null;
    this.infoAt = 0;
    this.charts = {};
    this.memKeys = '';
    this.gpuRows = [];
    this.gpuRendered = false;
    this.samples = 0;
    this.build();
  }

  // ---------------------------------------------------------------- DOM

  build() {
    const r = (name) => (node) => { this.el[name] = node; };
    this.el = {};

    const hero = h('div.card.vt-hero', {},
      h('div.stack.gap-1', {},
        h('div.title', { ref: r('host') }, '—'),
        h('div.vt-hero-meta', { ref: r('meta') })),
      h('div.vt-hero-badges', {},
        h('span.badge.badge-outline', { ref: r('platform') }, ''),
        h('span.badge', { ref: r('uptime'), title: 'Uptime' }, '')));

    const tile = (key, label, color, { unit = '', trend = 'spark', sparkOpts = {} } = {}) => {
      const value = h('span', {}, '–');
      const unitEl = h('span.stat-unit', {}, unit);
      const sub = h('div.stat-sub', {}, '');
      let trendEl = null;
      let canvas = null;
      if (trend === 'spark') {
        canvas = h('canvas', { 'aria-label': `${label} history` });
        trendEl = h('div.stat-trend', {}, canvas);
      } else {
        trendEl = h('div.meter', { style: { '--value': '0%' } }, h('span.meter-bar', {}, h('span.meter-fill')));
      }
      const card = h('div.card.stat.vt-tile', { id: `tile-${key}`, dataset: { tile: key } },
        h('div.stat-label', {}, h('i', { style: { '--c': `var(${color})` } }), label),
        h('div.stat-value', {}, value, unit ? unitEl : null),
        sub, trendEl);
      const t = { card, value, sub, unitEl, trendEl, canvas };
      if (canvas) t.spark = new Sparkline(canvas, { color, seconds: WINDOW_SEC, ...sparkOpts });
      this.tiles[key] = t;
      return card;
    };
    this.tiles = {};
    const tiles = h('div.grid.vt-tiles', {},
      tile('cpu', 'CPU', '--chart-1', { unit: '%', sparkOpts: { max: 100 } }),
      tile('mem', 'Memory', '--chart-7'),
      tile('gpu', 'GPU', '--chart-5', { unit: '%', sparkOpts: { max: 100 } }),
      tile('net', 'Network', '--chart-3', { unit: '↓' }),
      tile('disk', 'Disk', '--chart-2', { unit: 'read' }),
      tile('battery', 'Battery', '--chart-6', { trend: 'meter' }));
    this.charts.sparks = Object.fromEntries(Object.entries(this.tiles).filter(([, t]) => t.spark).map(([k, t]) => [k, t.spark]));

    // per-core chart (series are created once the core count is known)
    this.el.coreCanvas = h('canvas', { id: 'chart-cores' });
    this.el.coreLegend = h('div.chart-legend');
    this.el.legendToggle = h('button.btn.btn-sm.btn-ghost', { type: 'button', hidden: true, onclick: () => this.toggleLegend() }, 'Show legend');
    this.el.coreLegendBox = h('div.vt-core-legend', {}, this.el.coreLegend);
    this.el.coreBadge = h('span.badge.badge-outline', {}, '');
    const coreCard = h('div.card.vt-core-card', { id: 'card-cores' },
      h('div.card-header', {},
        h('div', {}, h('div.card-title', {}, 'CPU'), h('div.card-subtitle', { ref: r('coreSub') }, 'Per core · last 2 minutes')),
        h('div.row.gap-2', {}, this.el.coreBadge, this.el.legendToggle)),
      h('div.chart', {}, this.el.coreCanvas),
      this.el.coreLegendBox);

    // memory card
    this.el.memBar = h('div.barmeter-lg', { id: 'mem-bar' });
    this.el.memBadge = h('span.badge.badge-dot.badge-success', { id: 'mem-pressure' }, 'Normal');
    this.el.memRows = h('dl.vt-mem-rows');
    const memCard = h('div.card', { id: 'card-memory' },
      h('div.card-header', {},
        h('div', {}, h('div.card-title', {}, 'Memory'), h('div.card-subtitle', { ref: r('memSub') }, '')),
        h('div.row.gap-2', {}, h('span.text-muted.text-sm', {}, 'Pressure'), this.el.memBadge)),
      this.el.memBar,
      this.el.memRows);

    // gpu card
    this.el.gpuBody = h('div', { id: 'gpu-body' }, h('div.vt-note', {}, 'Waiting for GPU data…'));
    const gpuCard = h('div.card', { id: 'card-gpu' },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, 'GPU'), h('div.card-subtitle', { ref: r('gpuSub') }, 'Utilization and memory'))),
      this.el.gpuBody);

    // network + disk charts
    this.el.netCanvas = h('canvas', { id: 'chart-net' });
    this.el.diskCanvas = h('canvas', { id: 'chart-disk' });
    const netCard = h('div.card.vt-mini-chart', { id: 'card-net' },
      h('div.card-header', {}, h('div.card-title', {}, 'Network'), h('span.text-muted.text-sm', { ref: r('netSub') }, '')),
      h('div.chart', {}, this.el.netCanvas));
    const diskCard = h('div.card.vt-mini-chart', { id: 'card-disk' },
      h('div.card-header', {}, h('div.card-title', {}, 'Disk'), h('span.text-muted.text-sm', { ref: r('diskSub') }, '')),
      h('div.chart', {}, this.el.diskCanvas));

    this.root.replaceChildren(
      hero, tiles, coreCard,
      h('div.grid.items-start', { style: { '--grid-min': '320px' } }, memCard, gpuCard),
      h('div.grid', { style: { '--grid-min': '320px' } }, netCard, diskCard));

    const bps = (v) => formatBytes(v, { perSecond: true });
    this.charts.net = new LineChart(this.el.netCanvas, { series: [{ name: 'Download', color: '--chart-3' }, { name: 'Upload', color: '--chart-7' }], window: WINDOW_SEC, fill: true, format: bps });
    this.charts.disk = new LineChart(this.el.diskCanvas, { series: [{ name: 'Read', color: '--chart-2' }, { name: 'Write', color: '--chart-4' }], window: WINDOW_SEC, fill: true, format: bps });
  }

  // ----------------------------------------------------------------- info

  /** app.info arrived: machine header and chart sizing. */
  setInfo(info) {
    this.info = info;
    this.infoAt = Date.now();
    const m = info.machine || {};
    this.el.host.textContent = m.hostname || 'This machine';
    const meta = [];
    if (m.cpuModel) meta.push(h('span', {}, h('b', {}, m.cpuModel)));
    if (m.cores) meta.push(h('span', {}, h('b', {}, String(m.cores)), ' logical cores'));
    if (m.memTotal) meta.push(h('span', {}, h('b', {}, formatBytes(m.memTotal, { digits: 0 })), ' memory'));
    if (m.os) meta.push(h('span', {}, m.os));
    this.el.meta.replaceChildren(...meta);
    const plat = { windows: 'Windows', macos: 'macOS', linux: 'Linux' }[info.platform] || info.platform || '';
    this.el.platform.textContent = `${plat} · Vitals ${info.version || ''}`.trim();
    this.updateUptime();
    if (m.cores) this.ensureCores(m.cores);
    if (m.memTotal && this.tiles.mem.spark) this.tiles.mem.spark.max = m.memTotal;
  }

  updateUptime() {
    const m = this.info?.machine;
    if (!m || !m.uptimeSec) { this.el.uptime.hidden = true; return; }
    const up = m.uptimeSec + (Date.now() - this.infoAt) / 1000;
    this.el.uptime.hidden = false;
    this.el.uptime.textContent = `Up ${formatDuration(up)}`;
  }

  /** Create the per-core chart once (or recreate when the core count changes). */
  ensureCores(n) {
    if (!n || (this.charts.cores && this.charts.cores.series.length === n)) return;
    if (this.charts.cores) { this.charts.cores.destroy(); this.el.coreLegend.replaceChildren(); }
    this.charts.cores = new LineChart(this.el.coreCanvas, {
      series: Array.from({ length: n }, (_, i) => ({ name: `Core ${i + 1}` })),
      max: 100, window: WINDOW_SEC, legendEl: this.el.coreLegend, format: (v) => `${Math.round(v)}%`,
    });
    this.el.coreCanvas.setAttribute('aria-label', `CPU usage per core, ${n} cores`);
    this.el.coreBadge.textContent = `${n} cores`;
    const collapsible = n > 8;
    this.el.legendToggle.hidden = !collapsible;
    this.setLegendCollapsed(collapsible);
  }

  setLegendCollapsed(collapsed) {
    this.legendCollapsed = collapsed;
    this.el.coreLegendBox.dataset.collapsed = String(collapsed);
    const n = this.charts.cores ? this.charts.cores.series.length : 0;
    this.el.legendToggle.textContent = collapsed ? `Show legend (${n} cores)` : 'Hide legend';
    this.el.legendToggle.setAttribute('aria-expanded', String(!collapsed));
  }

  toggleLegend() { this.setLegendCollapsed(!this.legendCollapsed); }

  // --------------------------------------------------------------- sample

  /** One vitals.sample event. */
  update(s) {
    this.samples++;
    const t = this.tiles;

    // CPU
    const cpu = s.cpu || { total: 0, perCore: [] };
    t.cpu.value.textContent = String(Math.round(cpu.total));
    t.cpu.sub.textContent = cpu.loadAvg?.length ? `Load ${cpu.loadAvg.map((v) => v.toFixed(2)).join(' · ')}` : `${cpu.perCore.length} cores busy ${Math.round(cpu.total)}%`;
    t.cpu.spark.push(cpu.total);
    this.ensureCores(cpu.perCore.length || this.info?.machine?.cores || 0);
    if (this.charts.cores) this.charts.cores.push(cpu.perCore);
    this.el.coreSub.textContent = `Per core · last 2 minutes · total ${Math.round(cpu.total)}%`;

    // Memory
    const m = s.mem || {};
    const total = m.total || this.info?.machine?.memTotal || 0;
    const used = m.used || (m.app || 0) + (m.wired || 0) + (m.compressed || 0);
    t.mem.value.textContent = formatBytes(used, { digits: 1 });
    t.mem.sub.textContent = total ? `of ${formatBytes(total, { digits: 0 })} · ${Math.round((used / total) * 100)}%` : '';
    if (total && t.mem.spark.max !== total) t.mem.spark.max = total;
    t.mem.spark.push(used);
    this.updateMemory(m, total, used);

    // GPU
    const gpus = Array.isArray(s.gpu) ? s.gpu : [];
    if (gpus.length) {
      const g = gpus.reduce((a, b) => (b.utilization > a.utilization ? b : a), gpus[0]);
      t.gpu.value.textContent = String(Math.round(g.utilization || 0));
      t.gpu.sub.textContent = g.memTotal ? `${formatBytes(g.memUsed || 0, { digits: 1 })} of ${formatBytes(g.memTotal, { digits: 0 })}` : g.name || '';
      t.gpu.spark.push(g.utilization || 0);
    } else {
      t.gpu.value.textContent = '–';
      t.gpu.sub.textContent = 'No GPU data';
      t.gpu.spark.push(0);
    }
    this.updateGPU(gpus);

    // Network
    const net = s.net || { rxBps: 0, txBps: 0 };
    t.net.value.textContent = rateText(net.rxBps);
    t.net.sub.textContent = `↑ ${formatBytes(net.txBps, { perSecond: true })}`;
    t.net.spark.push(net.rxBps + net.txBps);
    this.charts.net.push([net.rxBps, net.txBps]);
    this.el.netSub.textContent = `↓ ${formatBytes(net.rxBps, { perSecond: true })}  ↑ ${formatBytes(net.txBps, { perSecond: true })}`;

    // Disk
    const d = s.disk || { readBps: 0, writeBps: 0 };
    t.disk.value.textContent = rateText(d.readBps);
    t.disk.sub.textContent = `${formatBytes(d.writeBps, { perSecond: true })} write`;
    t.disk.spark.push(d.readBps + d.writeBps);
    this.charts.disk.push([d.readBps, d.writeBps]);
    this.el.diskSub.textContent = `R ${formatBytes(d.readBps, { perSecond: true })}  W ${formatBytes(d.writeBps, { perSecond: true })}`;

    // Battery
    this.updateBattery(s.battery);
    this.updateUptime();
  }

  updateMemory(m, total, used) {
    const keys = memSegmentKeys({ ...m, used });
    const sig = keys.join(',');
    if (sig !== this.memKeys) {
      this.memKeys = sig;
      if (this.charts.mem) this.charts.mem.destroy();
      this.el.memBar.replaceChildren();
      this.charts.mem = new BarMeter(this.el.memBar, {
        segments: keys.map((k) => MEM_SEGMENTS[k]),
        total: total || undefined,
        label: 'Memory breakdown',
      });
    }
    this.charts.mem.set(memSegmentValues({ ...m, used, total }, keys), total || undefined);
    const { cls, text } = pressureBadge(m.pressure);
    this.el.memBadge.className = `badge badge-dot ${cls}`;
    this.el.memBadge.textContent = text;
    this.el.memSub.textContent = total ? `${formatBytes(used, { digits: 1 })} of ${formatBytes(total, { digits: 0 })} in use` : '';
    const rows = [];
    const free = m.free || (total > used ? total - used : 0);
    rows.push(['Available', formatBytes(Math.max(free, total - used), { digits: 1 })]);
    if (m.swapTotal) rows.push([m.committed ? 'Page file' : 'Swap', `${formatBytes(m.swapUsed || 0, { digits: 1 })} of ${formatBytes(m.swapTotal, { digits: 1 })}`]);
    if (m.committed) rows.push(['Committed', `${formatBytes(m.committed, { digits: 1 })} of ${formatBytes(m.commitLimit || 0, { digits: 1 })}`]);
    this.el.memRows.replaceChildren(...rows.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]));
  }

  updateGPU(gpus) {
    if (!this.gpuRendered || gpus.length !== this.gpuRows.length) {
      this.gpuRendered = true;
      for (const row of this.gpuRows) row.gauge.destroy();
      this.gpuRows = [];
      if (!gpus.length) {
        this.el.gpuBody.replaceChildren(h('div.empty-state', {}, icon('cpu', { size: 28, className: 'text-faint' }),
          h('div.empty-state-title', {}, 'No GPU data'),
          h('div.empty-state-text', {}, 'This platform reports no adapter utilization counters.')));
        this.el.gpuSub.textContent = 'Utilization and memory';
        return;
      }
      const rows = gpus.map((g, i) => {
        const canvas = h('canvas');
        const row = {
          el: h('div.vt-gpu-row', { dataset: { gpu: i } },
            h('div.gauge', {}, canvas),
            h('div.vt-gpu-main', {},
              h('div.vt-gpu-name', { ref: (n) => { row.name = n; } }, g.name || `GPU ${i + 1}`),
              h('div.meter', { ref: (n) => { row.meter = n; }, style: { '--value': '0%' } },
                h('span.meter-label.text-muted.text-sm', {}, 'Memory'),
                h('span.meter-bar', {}, h('span.meter-fill')),
                h('span.meter-text', { ref: (n) => { row.memText = n; } }, '–')),
              h('div.vt-gpu-engines', { ref: (n) => { row.engines = n; } }))),
        };
        row.gauge = new RingGauge(canvas, { label: 'GPU', color: '--chart-5', thickness: 7, thresholds: { warning: 0.8, danger: 0.95 } });
        return row;
      });
      this.gpuRows = rows;
      this.el.gpuBody.replaceChildren(...rows.map((r) => r.el));
      this.el.gpuSub.textContent = gpus.length === 1 ? (gpus[0].name || 'Utilization and memory') : `${gpus.length} adapters`;
    }
    gpus.forEach((g, i) => {
      const row = this.gpuRows[i];
      row.gauge.set((g.utilization || 0) / 100);
      row.name.textContent = g.name || `GPU ${i + 1}`;
      if (g.memTotal) {
        row.meter.style.setProperty('--value', `${clamp(((g.memUsed || 0) / g.memTotal) * 100, 0, 100).toFixed(1)}%`);
        row.memText.textContent = `${formatBytes(g.memUsed || 0, { digits: 1 })} / ${formatBytes(g.memTotal, { digits: 0 })}`;
        row.meter.hidden = false;
      } else if (g.memUsed) {
        row.memText.textContent = formatBytes(g.memUsed, { digits: 1 });
        row.meter.style.setProperty('--value', '0%');
      } else {
        row.meter.hidden = true;
      }
      const parts = [];
      if (g.engines) for (const [name, v] of Object.entries(g.engines)) parts.push(h('span', {}, `${name} `, h('b', {}, `${Math.round(v)}%`)));
      if (g.renderer != null) parts.push(h('span', {}, 'Renderer ', h('b', {}, `${Math.round(g.renderer)}%`)));
      if (g.tiler != null) parts.push(h('span', {}, 'Tiler ', h('b', {}, `${Math.round(g.tiler)}%`)));
      if (g.sharedUsed) parts.push(h('span', {}, 'Shared ', h('b', {}, formatBytes(g.sharedUsed, { digits: 1 }))));
      row.engines.replaceChildren(...parts);
    });
  }

  updateBattery(b) {
    const t = this.tiles.battery;
    const meter = t.trendEl;
    if (!b || b.present === false) {
      t.value.textContent = b?.onAC === false ? '–' : 'AC';
      t.sub.textContent = 'No battery';
      meter.style.setProperty('--value', '100%');
      meter.className = 'meter meter-success';
      return;
    }
    const pct = clamp(b.percent ?? 0, 0, 100);
    t.value.textContent = `${pct}%`;
    let state = b.charging ? 'Charging' : b.onAC ? 'Plugged in' : 'On battery';
    if (!b.onAC && b.timeToEmptyMin) state += ` · ${formatDuration(b.timeToEmptyMin * 60).replace(/ \d\ds$/, '')} left`;
    t.sub.textContent = state;
    meter.style.setProperty('--value', `${pct}%`);
    meter.className = `meter ${pct <= 10 ? 'meter-danger' : pct <= 20 ? 'meter-warning' : 'meter-success'}`;
  }

  show() { for (const c of Object.values(this.charts)) { if (c && typeof c.resize === 'function') c.resize(); } }
  hide() {}
}
