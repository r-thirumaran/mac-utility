// demo.js — kitchen-sink page for the shared UI kit: every component and chart with
// fake live data. Doubles as documentation (view source of demo.html) and as the
// Playwright target (tests/ui/01-ui-kit.test.mjs reads window.__demo).
import { Sparkline, LineChart, RingGauge, BarMeter, formatBytes, formatPercent, formatDuration } from './charts.js';
import { h, $, $$, tabs, segmented, sliderFill, toast, confirmDialog, relativeTime, setTheme, applyStoredTheme, icon } from './ui.js';

// ------------------------------------------------------------- test hook

const demo = {
  ready: false,
  counters: { ticks: 0, sparklinePushes: 0, linePushes: 0, gaugeSets: 0, barMeterSets: 0 },
  lastConfirm: null,
  charts: {},
  stop() { clearInterval(timer); },
};
window.__demo = demo;

// ---------------------------------------------------------- fake signals

/** Deterministic PRNG so screenshots are reproducible. */
function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6D2B79F5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(20261005);
const walk = (v, min, max, step) => Math.min(max, Math.max(min, v + (rand() - 0.5) * 2 * step));

const GIB = 2 ** 30;
const state = {
  cores: [35, 20, 12, 8],
  mem: { app: 6.1 * GIB, wired: 2.2 * GIB, compressed: 1.3 * GIB, cached: 3.4 * GIB, swap: 0.4 * GIB, total: 16 * GIB },
  net: { rx: 2.1e6, tx: 0.4e6 },
  disk: { write: 1.2e6 },
  gpu: 0.31,
  pressure: 0.42,
  battery: 0.87,
};

// --------------------------------------------------------------- tokens

const uiTokens = ['--bg', '--surface', '--surface-2', '--surface-3', '--border', '--text', '--text-muted', '--text-faint', '--accent', '--success', '--warning', '--danger'];
$('#swatches').append(...uiTokens.map((name) => h('div.swatch', {},
  h('div.swatch-color', { style: { background: `var(${name})` } }),
  h('div.swatch-name', { title: name }, name))));
$('#chart-swatches').append(...Array.from({ length: 8 }, (_, i) => h('div.swatch', {},
  h('div.swatch-color', { style: { background: `var(--chart-${i + 1})` } }),
  h('div.swatch-name', {}, `--chart-${i + 1}`))));
$('#seq-ramp').append(...Array.from({ length: 7 }, (_, i) => h('span', { style: { background: `var(--chart-seq-${i + 1})` } })));

// -------------------------------------------------------------- buttons

$('#btn-icon-refresh').append(icon('refresh'));
$('#btn-icon-more').append(icon('more'));
$('#btn-with-icon').append(icon('plus'), 'Add machine');

// ---------------------------------------------------------------- forms

const sliderA = $('#slider-a');
const sliderAValue = $('#slider-a-value');
const showSliderA = () => { sliderAValue.textContent = `${sliderA.value} min`; };
sliderFill(sliderA);
sliderA.addEventListener('input', showSliderA);
showSliderA();
sliderFill($('#slider-b'));
const sliderDelay = $('#slider-delay');
const showDelay = () => { $('#slider-delay-value').textContent = `${sliderDelay.value} ms`; };
sliderFill(sliderDelay);
sliderDelay.addEventListener('input', showDelay);
showDelay();
demo.viewSwitch = segmented('#view-switch', { onChange: (v) => toast(`View: ${v}`, { timeout: 1200 }) });

// ----------------------------------------------------------------- tabs

demo.tabs = tabs('#demo-tabs', { onChange: (id) => { demo.lastTab = id; } });
tabs($('.tabs.is-pills'));

// ---------------------------------------------------------------- theme

const themeSwitch = segmented('#theme-switch', { onChange: (v) => setTheme(v) });
themeSwitch.select(applyStoredTheme(), { silent: true });

// ---------------------------------------------------------------- stats

const sparks = {
  cpu: new Sparkline($('#spark-cpu'), { max: 100, seconds: 120 }),
  mem: new Sparkline($('#spark-mem'), { min: 0, max: state.mem.total, color: '--chart-7' }),
  net: new Sparkline($('#spark-net'), { color: '--chart-3' }),
  disk: new Sparkline($('#spark-disk'), { color: '--chart-2', fill: false }),
};
demo.charts.sparks = sparks;

function setDelta(id, current, before, { format, upIsBad = true } = {}) {
  const el = $(id);
  if (!Number.isFinite(before)) { el.textContent = 'vs 1 min ago'; el.className = 'stat-delta'; return; }
  const diff = current - before;
  const dir = Math.abs(diff) < 1e-9 ? '' : diff > 0 ? 'is-up' : 'is-down';
  const mood = !dir ? '' : (diff > 0) === upIsBad ? 'is-bad' : 'is-good';
  el.className = `stat-delta ${dir} ${mood}`;
  el.textContent = `${format(Math.abs(diff))} vs 1 min ago`;
}

// --------------------------------------------------------------- charts

const coreChart = new LineChart($('#line-cores'), {
  series: state.cores.map((_, i) => ({ name: `Core ${i + 1}` })),
  max: 100,
  window: 120,
  format: (v) => `${Math.round(v)}%`,
});
const netChart = new LineChart($('#line-net'), {
  series: [{ name: 'Download' }, { name: 'Upload' }],
  window: 120,
  format: (v) => formatBytes(v, { perSecond: true }),
});
const pressureChart = new LineChart($('#line-pressure'), {
  series: [{ name: 'Pressure', color: '--chart-7' }],
  max: 100,
  window: 120,
  legend: false,
  fill: true,
  format: (v) => `${Math.round(v)}%`,
});
demo.charts.cores = coreChart;
demo.charts.net = netChart;
demo.charts.pressure = pressureChart;

// --------------------------------------------------------------- gauges

const gauges = {
  cpu: new RingGauge($('#gauge-cpu'), { label: 'CPU', thresholds: { warning: 0.7, danger: 0.9 } }),
  gpu: new RingGauge($('#gauge-gpu'), { label: 'GPU', color: '--chart-7', thresholds: { warning: 0.7, danger: 0.9 } }),
  battery: new RingGauge($('#gauge-battery'), { label: 'Battery', color: '--success', thickness: 6 }),
};
demo.charts.gauges = gauges;

const memMeter = new BarMeter($('#barmeter-mem'), {
  segments: [{ name: 'App' }, { name: 'Wired' }, { name: 'Compressed' }, { name: 'Cached' }],
  total: state.mem.total,
  label: 'Memory breakdown',
});
demo.charts.memMeter = memMeter;
$('#mem-total').textContent = `${formatBytes(state.mem.total)} total`;

const liveMeter = $('#meter-live');
function setMeter(el, fraction) {
  el.style.setProperty('--value', `${(fraction * 100).toFixed(1)}%`);
  el.classList.toggle('meter-warning', fraction >= 0.7 && fraction < 0.9);
  el.classList.toggle('meter-danger', fraction >= 0.9);
  $('.meter-text', el).textContent = formatPercent(fraction);
}

// ---------------------------------------------------------------- table

const PROCS = ['WindowServer', 'kernel_task', 'Safari', 'Xcode', 'Vitals', 'Edgehop', 'Reach', 'Music', 'Terminal', 'mds_stores', 'coreaudiod', 'Finder'];
const procs = PROCS.map((name, i) => ({ name, pid: 100 + Math.floor(rand() * 60000), cpu: rand() * 40, mem: (0.05 + rand() * 2.5) * GIB, threads: 2 + Math.floor(rand() * 60), color: `var(--chart-${(i % 8) + 1})` }));
const tbody = $('#proc-table tbody');
const rows = procs.map((p) => {
  const cells = {
    cpu: h('td.num'),
    mem: h('td.num'),
    energy: h('div.meter', { style: { '--value': '0%' } }, h('span.meter-bar', {}, h('span.meter-fill'))),
  };
  const tr = h('tr', {},
    h('td', {}, h('span.cell-icon', {}, h('span.proc-dot', { style: { '--c': p.color } }), p.name)),
    h('td.num.text-muted', {}, String(p.pid)),
    cells.cpu, cells.mem,
    h('td.num', {}, String(p.threads)),
    h('td', {}, cells.energy));
  return { p, tr, cells };
});
function renderTable() {
  rows.sort((a, b) => b.p.cpu - a.p.cpu);
  for (const r of rows) {
    r.cells.cpu.textContent = `${r.p.cpu.toFixed(1)}%`;
    r.cells.mem.textContent = formatBytes(r.p.mem);
    r.cells.energy.style.setProperty('--value', `${Math.min(100, r.p.cpu * 2.2).toFixed(0)}%`);
  }
  tbody.replaceChildren(...rows.map((r) => r.tr));
}

// -------------------------------------------------------------- feedback

$('#btn-toast').addEventListener('click', () => toast('Settings saved.'));
$('#btn-toast-success').addEventListener('click', () => toast('Paired with “Office PC”.', { kind: 'success' }));
$('#btn-toast-warning').addEventListener('click', () => toast('Accessibility permission is still missing.', { kind: 'warning', action: 'Open Settings', onAction: () => {} }));
$('#btn-toast-danger').addEventListener('click', () => toast('Could not apply power settings: not authorized.', { kind: 'danger', timeout: 6000 }));
$('#btn-confirm').addEventListener('click', async () => {
  demo.lastConfirm = null;
  const ok = await confirmDialog({ title: 'Apply power settings?', body: 'macOS will ask for an administrator password.', okLabel: 'Apply' });
  demo.lastConfirm = ok;
  $('#confirm-result').textContent = ok ? 'Applied' : 'Cancelled';
});
$('#btn-delete').addEventListener('click', async () => {
  demo.lastConfirm = null;
  const ok = await confirmDialog({ title: 'Force quit “Safari”?', body: 'Unsaved changes in Safari will be lost.', okLabel: 'Force Quit', danger: true });
  demo.lastConfirm = ok;
  $('#confirm-result').textContent = ok ? 'Quit' : 'Cancelled';
  if (ok) toast('Safari was force quit.', { kind: 'success' });
});
const sheet = $('#demo-sheet');
$('#btn-sheet').addEventListener('click', () => { $('#sheet-seen').textContent = relativeTime(Date.now() - 3 * 60 * 1000); sheet.showModal(); });
$('#btn-sheet-close').addEventListener('click', () => sheet.close());
sheet.addEventListener('click', (e) => { if (e.target === sheet) sheet.close(); });
$('#btn-empty-action').addEventListener('click', () => toast('Pairing is not available in the demo.', { kind: 'info' }));

// --------------------------------------------------------------- ticking

const started = Date.now();
const history = { cpu: [], mem: [], net: [], disk: [] };
function remember(key, v) { const a = history[key]; a.push(v); if (a.length > 61) a.shift(); return a.length > 60 ? a[0] : NaN; }

function tick() {
  demo.counters.ticks++;
  const c = state.cores;
  for (let i = 0; i < c.length; i++) c[i] = walk(c[i], 2, 98, 9);
  const cpuTotal = c.reduce((a, b) => a + b, 0) / c.length;
  state.mem.app = walk(state.mem.app, 3 * GIB, 9 * GIB, 0.15 * GIB);
  state.mem.cached = walk(state.mem.cached, 1 * GIB, 4 * GIB, 0.1 * GIB);
  state.mem.swap = walk(state.mem.swap, 0, 2 * GIB, 0.05 * GIB);
  state.net.rx = walk(state.net.rx, 0, 60e6, 4e6);
  state.net.tx = walk(state.net.tx, 0, 12e6, 1e6);
  state.disk.write = walk(state.disk.write, 0, 40e6, 3e6);
  state.gpu = walk(state.gpu, 0.02, 0.98, 0.06);
  state.pressure = walk(state.pressure, 0.1, 0.95, 0.03);
  const memUsed = state.mem.app + state.mem.wired + state.mem.compressed;

  sparks.cpu.push(cpuTotal);
  sparks.mem.push(memUsed);
  sparks.net.push(state.net.rx);
  sparks.disk.push(state.disk.write);
  demo.counters.sparklinePushes += 4;

  $('#stat-cpu-value').textContent = cpuTotal.toFixed(0);
  $('#stat-mem-value').textContent = formatBytes(memUsed);
  $('#stat-net-value').textContent = formatBytes(state.net.rx, { perSecond: true });
  $('#stat-disk-value').textContent = formatBytes(state.disk.write, { perSecond: true });
  setDelta('#stat-cpu-delta', cpuTotal, remember('cpu', cpuTotal), { format: (v) => `${v.toFixed(0)} pt` });
  setDelta('#stat-mem-delta', memUsed, remember('mem', memUsed), { format: (v) => formatBytes(v) });
  setDelta('#stat-net-delta', state.net.rx, remember('net', state.net.rx), { format: (v) => formatBytes(v, { perSecond: true }), upIsBad: false });
  setDelta('#stat-disk-delta', state.disk.write, remember('disk', state.disk.write), { format: (v) => formatBytes(v, { perSecond: true }) });

  coreChart.push(c);
  netChart.push([state.net.rx, state.net.tx]);
  pressureChart.push([state.pressure * 100]);
  demo.counters.linePushes += 3;

  gauges.cpu.set(cpuTotal / 100);
  gauges.gpu.set(state.gpu);
  gauges.battery.set(state.battery, `${Math.round(state.battery * 100)}%`);
  demo.counters.gaugeSets += 3;

  memMeter.set([state.mem.app, state.mem.wired, state.mem.compressed, state.mem.cached]);
  demo.counters.barMeterSets++;
  $('#mem-swap').textContent = formatBytes(state.mem.swap);
  setMeter(liveMeter, state.gpu);

  if (demo.counters.ticks % 2 === 0) {
    for (const r of rows) r.p.cpu = walk(r.p.cpu, 0, 60, 3);
    renderTable();
  }
  $('#uptime').textContent = `up ${formatDuration((Date.now() - started) / 1000 + 3600 * 5 + 1500)}`;
}

// Pre-fill most of the window so the page is meaningful at first paint.
for (let i = 0; i < 100; i++) tick();
$('#last-updated').textContent = relativeTime(new Date());
const timer = setInterval(tick, 1000);
demo.ready = true;
