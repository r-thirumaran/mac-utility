// processes.js — the Processes page: a dense sortable table of every
// process (name, pid, cpu %, memory), a search box, a 2 s refresh while the
// page is visible and "End task" behind a confirmation dialog.
import { formatBytes } from './shared/charts.js';
import { h, icon, toast, confirmDialog, debounce } from './shared/ui.js';
import { errorCard } from './common.js';

const REFRESH_MS = 2000;

const COLUMNS = [
  { key: 'name', label: 'Name', num: false },
  { key: 'pid', label: 'PID', num: true },
  { key: 'cpu', label: 'CPU %', num: true },
  { key: 'memBytes', label: 'Memory', num: true },
];

/**
 * Sort rows by key/direction (stable, ties by pid).
 * @param {object[]} rows
 * @param {string} key
 * @param {'asc'|'desc'} dir
 */
export function sortRows(rows, key, dir) {
  const sign = dir === 'asc' ? 1 : -1;
  return rows.slice().sort((a, b) => {
    let c = 0;
    if (key === 'name') c = String(a.name || '').localeCompare(String(b.name || ''), undefined, { sensitivity: 'base' });
    else c = (a[key] || 0) - (b[key] || 0);
    if (c === 0) c = a.pid - b.pid;
    return c * sign;
  });
}

/** Case-insensitive filter on name or pid. */
export function filterRows(rows, query) {
  const q = query.trim().toLowerCase();
  if (!q) return rows;
  return rows.filter((r) => String(r.name || '').toLowerCase().includes(q) || String(r.pid).includes(q));
}

export class Processes {
  /**
   * @param {HTMLElement} root
   * @param {object} opts
   * @param {(method: string, params?: object) => Promise<any>} opts.call
   */
  constructor(root, { call }) {
    this.root = root;
    this.call = call;
    this.rows = [];
    this.visible = false;
    this.timer = 0;
    this.sortKey = 'cpu';
    this.sortDir = 'desc';
    this.query = '';
    this.selected = null;
    this.refreshes = 0;
    this.error = null;
    this.build();
  }

  build() {
    this.el = {};
    this.el.search = h('input.input.input-sm', {
      type: 'search', placeholder: 'Search processes', 'aria-label': 'Search processes', autocomplete: 'off',
      oninput: debounce((e) => { this.query = e.target.value; this.render(); }, 80),
    });
    this.el.count = h('span.badge', {}, '–');
    this.el.endBtn = h('button.btn.btn-sm.btn-danger', { type: 'button', disabled: true, onclick: () => this.endTask() }, 'End task');
    this.el.refreshBtn = h('button.btn.btn-sm.btn-icon', { type: 'button', 'aria-label': 'Refresh', title: 'Refresh', onclick: () => this.refresh() }, icon('refresh'));
    this.el.tbody = h('tbody');
    this.el.head = h('tr', {}, COLUMNS.map((c) => h('th', {
      class: c.num ? 'num' : '', dataset: { key: c.key }, scope: 'col', tabindex: 0,
      onclick: () => this.sortBy(c.key),
      onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); this.sortBy(c.key); } },
    }, c.label)));
    this.el.table = h('table.table.vt-proc-table', { id: 'proc-table' }, h('thead', {}, this.el.head), this.el.tbody);
    this.el.wrap = h('div.table-wrap.vt-proc-wrap', {}, this.el.table);
    this.el.footer = h('div.vt-proc-footer', {}, h('span', { ref: (n) => { this.el.summary = n; } }, ''), h('span', {}, 'Click a column to sort · select a row and press Delete to end it'));
    this.el.errorBox = h('div', { hidden: true });
    this.el.card = h('div.card.is-flush.vt-proc-card', {},
      h('div.card-header', {},
        h('div.row.gap-2', {}, h('div.card-title', {}, 'Processes'), this.el.count),
        h('div.vt-proc-tools', {}, this.el.search, this.el.refreshBtn, this.el.endBtn)),
      this.el.errorBox,
      this.el.wrap,
      this.el.footer);
    this.root.replaceChildren(this.el.card);
    this.el.table.addEventListener('click', (e) => {
      const tr = e.target.closest('tbody tr');
      if (tr) this.select(Number(tr.dataset.pid));
    });
    this.el.table.addEventListener('dblclick', (e) => {
      const tr = e.target.closest('tbody tr');
      if (tr) { this.select(Number(tr.dataset.pid)); this.endTask(); }
    });
    this.el.wrap.addEventListener('keydown', (e) => {
      if (e.key === 'Delete' && this.selected != null) { e.preventDefault(); this.endTask(); }
    });
    this.updateSortHeader();
  }

  sortBy(key) {
    if (this.sortKey === key) this.sortDir = this.sortDir === 'asc' ? 'desc' : 'asc';
    else { this.sortKey = key; this.sortDir = key === 'name' || key === 'pid' ? 'asc' : 'desc'; }
    this.updateSortHeader();
    this.render();
  }

  updateSortHeader() {
    for (const th of this.el.head.children) {
      if (th.dataset.key === this.sortKey) th.setAttribute('aria-sort', this.sortDir === 'asc' ? 'ascending' : 'descending');
      else th.removeAttribute('aria-sort');
    }
  }

  select(pid) {
    this.selected = pid;
    for (const tr of this.el.tbody.children) tr.setAttribute('aria-selected', String(Number(tr.dataset.pid) === pid));
    const row = this.rows.find((r) => r.pid === pid);
    this.el.endBtn.disabled = !row;
    this.el.endBtn.textContent = row ? `End task` : 'End task';
  }

  async refresh() {
    if (this.inFlight) return;
    this.inFlight = true;
    try {
      const sortBy = this.sortKey === 'memBytes' ? 'mem' : this.sortKey;
      const rows = await this.call('vitals.processes', { sortBy, limit: 0 });
      this.rows = Array.isArray(rows) ? rows : [];
      this.error = null;
      this.refreshes++;
      this.render();
    } catch (e) {
      this.error = e;
      this.render();
    } finally {
      this.inFlight = false;
    }
  }

  render() {
    const err = this.error;
    this.el.errorBox.hidden = !err;
    if (err) this.el.errorBox.replaceChildren(errorCard(err, { title: 'Could not list processes', action: 'Retry', onAction: () => this.refresh() }));
    const visible = sortRows(filterRows(this.rows, this.query), this.sortKey, this.sortDir);
    const frag = document.createDocumentFragment();
    let totalCPU = 0;
    let totalMem = 0;
    for (const r of this.rows) { totalCPU += r.cpu || 0; totalMem += r.memBytes || 0; }
    for (const r of visible) {
      const tr = h('tr', { dataset: { pid: r.pid, mem: r.memBytes || 0, cpu: (r.cpu || 0).toFixed(1) }, 'aria-selected': String(r.pid === this.selected) },
        h('td.vt-name', { title: r.name }, r.name || `pid ${r.pid}`),
        h('td.num.vt-pid', {}, String(r.pid)),
        h('td.num', { class: r.cpu >= 25 ? 'is-hot' : '' }, (r.cpu || 0).toFixed(1)),
        h('td.num', {}, formatBytes(r.memBytes || 0, { digits: 1 })));
      frag.append(tr);
    }
    this.el.tbody.replaceChildren(frag);
    this.el.count.textContent = this.query ? `${visible.length} of ${this.rows.length}` : String(this.rows.length);
    this.el.summary.textContent = `${this.rows.length} processes · ${totalCPU.toFixed(0)}% CPU · ${formatBytes(totalMem, { digits: 1 })} resident`;
    if (this.selected != null && !this.rows.some((r) => r.pid === this.selected)) { this.selected = null; this.el.endBtn.disabled = true; }
  }

  async endTask() {
    const row = this.rows.find((r) => r.pid === this.selected);
    if (!row) return;
    const ok = await confirmDialog({
      title: `End "${row.name}"?`,
      body: `Vitals will terminate process ${row.pid}. Unsaved data in that application may be lost.`,
      okLabel: 'End task', danger: true,
    });
    if (!ok) return;
    try {
      await this.call('vitals.killProcess', { pid: row.pid, force: false });
      toast(`Ended ${row.name} (${row.pid})`, { kind: 'success' });
    } catch (e) {
      toast(`Could not end ${row.name}: ${e.message}`, { kind: 'danger', timeout: 6000 });
    }
    this.refresh();
  }

  show() {
    this.visible = true;
    this.refresh();
    clearInterval(this.timer);
    this.timer = setInterval(() => { if (this.visible && !document.hidden) this.refresh(); }, REFRESH_MS);
  }

  hide() {
    this.visible = false;
    clearInterval(this.timer);
    this.timer = 0;
  }

  /** Rows as currently rendered (tests). */
  visibleRows() {
    return Array.from(this.el.tbody.children).map((tr) => ({ pid: Number(tr.dataset.pid), mem: Number(tr.dataset.mem), cpu: Number(tr.dataset.cpu), name: tr.firstChild.textContent }));
  }
}
