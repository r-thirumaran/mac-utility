// power.js — the Power page: sleep timers and lid behaviour per power
// source, wake options, hibernation and the power button (where supported),
// Apply/Revert with only the changed keys, keep-awake, immediate actions and
// recurring schedules. Everything is driven by the power.get model and its
// `supported` block (docs/ARCHITECTURE.md §5), so the same page works with
// the Windows (Go) and macOS (Swift) backends.
import { h, icon, toast, confirmDialog, $ } from './shared/ui.js';
import { ACTION_LABELS, KEY_LABELS, SLEEP_MINUTES, minutesLabel, daysLabel, WEEK, DAY_NAMES, errorCard, select, toggle, settingRow } from './common.js';

/** Read a dotted key from the model. */
export function getPath(obj, key) {
  return key.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

/** Nest dotted keys back into the model shape power.apply expects. */
export function nestChanges(flat) {
  const out = {};
  for (const [key, value] of Object.entries(flat)) {
    const parts = key.split('.');
    let o = out;
    for (let i = 0; i < parts.length - 1; i++) o = o[parts[i]] ||= {};
    o[parts[parts.length - 1]] = value;
  }
  return out;
}

/** Sleep timer options including an off-list current value. */
export function sleepOptions(current) {
  const list = SLEEP_MINUTES.slice();
  if (current != null && !list.includes(current)) {
    list.push(current);
    list.sort((a, b) => (a === 0 ? 1 : b === 0 ? -1 : a - b));
  }
  return list.map((m) => ({ value: m, label: minutesLabel(m) }));
}

export class Power {
  /**
   * @param {HTMLElement} root
   * @param {object} opts
   * @param {(method: string, params?: object) => Promise<any>} opts.call
   * @param {(event: string, fn: Function) => Function} opts.on
   */
  constructor(root, { call, on }) {
    this.root = root;
    this.call = call;
    this.info = null;
    this.model = null;
    this.schedule = [];
    this.pending = {};
    this.loaded = false;
    this.loading = null;
    this.lastSample = null;
    this.el = {};
    on('power.keepAwake', (st) => { if (this.model) { this.model.keepAwake = st; this.renderAwake(); } });
    this.root.replaceChildren(h('div.vt-note', {}, 'Loading power settings…'));
  }

  setInfo(info) { this.info = info; }

  /** Latest vitals.sample (for the AC/battery badge). */
  sample(s) {
    this.lastSample = s;
    if (this.el.source) this.renderSourceBadge();
  }

  get platform() { return this.info?.platform || ''; }

  // ----------------------------------------------------------------- load

  async load() {
    if (this.loading) return this.loading;
    this.loading = (async () => {
      try {
        this.model = await this.call('power.get');
        this.error = null;
      } catch (e) {
        this.error = e;
        this.model = null;
      }
      try {
        const res = await this.call('power.schedule.get');
        this.schedule = Array.isArray(res?.items) ? res.items : [];
        this.scheduleError = null;
      } catch (e) {
        this.schedule = [];
        this.scheduleError = e;
      }
      this.pending = {};
      this.loaded = true;
      this.render();
    })();
    try { await this.loading; } finally { this.loading = null; }
  }

  show() { if (!this.loaded) this.load(); }
  hide() {}

  // --------------------------------------------------------------- render

  render() {
    const m = this.model;
    if (!m) {
      this.root.replaceChildren(errorCard(this.error || new Error('No power model'), {
        title: this.error?.code === 'not_supported' ? 'Power settings are not available here' : 'Could not read power settings',
        action: this.error?.code === 'needs_permission' ? 'Open Settings' : 'Retry',
        onAction: () => (this.error?.code === 'needs_permission' ? this.openPane() : this.load()),
      }), this.buildAwakeCard(), this.buildActionsCard());
      return;
    }
    const sup = m.supported || {};
    this.dirtyRows = new Map();

    const sources = [];
    if (sup.battery !== false) sources.push(this.buildSourceCard('onBattery', 'On battery', 'When running on battery power.'));
    sources.push(this.buildSourceCard('onPower', 'Plugged in', 'When connected to power.'));

    this.root.replaceChildren(
      this.buildStatusCard(),
      h('div.grid.vt-sources', {}, ...sources),
      this.buildOptionsCard(),
      this.buildApplyBar(),
      h('div.grid', { style: { '--grid-min': '320px' } }, this.buildAwakeCard(), this.buildActionsCard()),
      this.buildScheduleCard(),
    );
    this.renderSourceBadge();
    this.renderDirty();
  }

  buildStatusCard() {
    const m = this.model;
    const left = h('div.vt-power-scheme', {},
      h('div.card-title', {}, m.scheme ? `Power plan: ${m.scheme}` : 'Power settings'),
      h('div.text-muted.text-sm', {}, {
        windows: 'Timers and button actions apply to the active power plan. Hibernation changes need administrator rights.',
        macos: 'Changes are applied with pmset and may ask for an administrator password.',
      }[this.platform] || 'Changes take effect when you press Apply.'));
    const badges = [];
    this.el.source = h('span.badge.badge-outline', {}, '');
    badges.push(this.el.source);
    if (m.fake) badges.push(h('span.badge.badge-warning.badge-dot', { title: 'This backend simulates a laptop; nothing is changed on this machine.' }, 'Simulated'));
    if (this.platform === 'windows' || this.platform === 'linux') {
      badges.push(h('span.badge', { class: m.elevated ? 'badge-success badge-dot' : 'badge-outline' }, m.elevated ? 'Administrator' : 'Standard user'));
      if (this.platform === 'windows' && !m.elevated) {
        badges.push(h('button.btn.btn-sm', { type: 'button', onclick: () => this.relaunchElevated() }, 'Restart as administrator'));
      }
    }
    return h('div.card.vt-power-status', { id: 'power-status' }, left, h('div.vt-hero-badges', {}, ...badges));
  }

  renderSourceBadge() {
    const b = this.lastSample?.battery;
    const el = this.el.source;
    if (!el) return;
    if (!b || b.present === false) { el.textContent = 'On AC power'; return; }
    el.textContent = b.charging ? `Charging · ${b.percent}%` : b.onAC ? `Plugged in · ${b.percent}%` : `On battery · ${b.percent}%`;
  }

  /** The "On battery" / "Plugged in" card. */
  buildSourceCard(src, title, subtitle) {
    const m = this.model;
    const sup = m.supported || {};
    const cur = m[src] || {};
    const rows = [];
    if (sup.displaySleep !== false) {
      rows.push(this.row(`${src}.displaySleepMin`, 'Turn off the display after', null,
        select(sleepOptions(cur.displaySleepMin), cur.displaySleepMin ?? 0, (v) => this.change(`${src}.displaySleepMin`, Number(v)), { 'aria-label': `${title}: display sleep` })));
    }
    if (sup.systemSleep !== false) {
      rows.push(this.row(`${src}.systemSleepMin`, 'Put the computer to sleep after', null,
        select(sleepOptions(cur.systemSleepMin), cur.systemSleepMin ?? 0, (v) => this.change(`${src}.systemSleepMin`, Number(v)), { 'aria-label': `${title}: system sleep` })));
    }
    if (sup.lid && Array.isArray(sup.lidClose) && sup.lidClose.length) {
      const choices = sup.lidClose.includes(cur.lidClose) ? sup.lidClose : [...sup.lidClose, cur.lidClose].filter(Boolean);
      rows.push(this.row(`${src}.lidClose`, 'When I close the lid', null,
        select(choices.map((a) => ({ value: a, label: ACTION_LABELS[a] || a })), cur.lidClose, (v) => this.change(`${src}.lidClose`, v), { 'aria-label': `${title}: lid close`, dataset: { key: `${src}.lidClose` } })));
    }
    if (!rows.length) rows.push(h('div.setting', {}, h('div.setting-main.text-muted', {}, 'No adjustable settings.')));
    return h('div.card.is-flush', { dataset: { source: src } },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, title), h('div.card-subtitle', {}, subtitle))),
      ...rows);
  }

  /** Wake, hibernate and power-button options. */
  buildOptionsCard() {
    const m = this.model;
    const sup = m.supported || {};
    const rows = [];
    if (sup.lidOpenWakes) rows.push(this.row('lidOpenWakes', 'Wake when the lid opens', null, toggle(!!m.lidOpenWakes, (v) => this.change('lidOpenWakes', v), { 'aria-label': 'Wake when the lid opens' })));
    if (sup.wakeOnNetwork) rows.push(this.row('wakeOnNetwork', 'Wake for network access', 'Lets other devices wake this computer.', toggle(!!m.wakeOnNetwork, (v) => this.change('wakeOnNetwork', v), { 'aria-label': 'Wake for network access' })));
    if (sup.powerNap) rows.push(this.row('powerNap', 'Power Nap', 'Check for mail, calendar and iCloud updates while sleeping.', toggle(!!m.powerNap, (v) => this.change('powerNap', v), { 'aria-label': 'Power Nap' })));
    if (sup.lowPowerMode) rows.push(this.row('lowPowerMode', 'Low power mode', 'Reduce energy use and performance.', toggle(!!m.lowPowerMode, (v) => this.change('lowPowerMode', v), { 'aria-label': 'Low power mode' })));
    if (sup.hibernate) {
      const hib = m.hibernate || { enabled: false, mode: 'full' };
      rows.push(this.row('hibernate.enabled', 'Hibernation', this.platform === 'windows' ? 'Keeps a hibernation file so Hibernate and Fast Startup are available. Needs administrator rights.' : null,
        toggle(!!hib.enabled, (v) => this.change('hibernate.enabled', v), { 'aria-label': 'Hibernation' })));
      const modes = Array.isArray(sup.hibernateModes) && sup.hibernateModes.length ? sup.hibernateModes : ['full', 'reduced'];
      const modeLabels = { full: 'Full (hibernate + fast startup)', reduced: 'Reduced (fast startup only)' };
      const modeRow = this.row('hibernate.mode', 'Hibernation file', null,
        select(modes.map((v) => ({ value: v, label: modeLabels[v] || v })), hib.mode || modes[0], (v) => this.change('hibernate.mode', v), { 'aria-label': 'Hibernation file type' }));
      modeRow.classList.toggle('is-off', !hib.enabled);
      this.el.hibernateModeRow = modeRow;
      rows.push(modeRow);
    }
    if (sup.powerButton) {
      const actions = Array.isArray(sup.powerButtonActions) && sup.powerButtonActions.length ? sup.powerButtonActions : ['nothing', 'sleep', 'shutdown'];
      const choices = actions.includes(m.powerButton) || !m.powerButton ? actions : [...actions, m.powerButton];
      rows.push(this.row('powerButton', 'When I press the power button', null,
        select(choices.map((a) => ({ value: a, label: ACTION_LABELS[a] || a })), m.powerButton || actions[0], (v) => this.change('powerButton', v), { 'aria-label': 'Power button action' })));
    }
    if (!rows.length) rows.push(h('div.setting', {}, h('div.setting-main.text-muted', {}, 'No additional options are available on this platform.')));
    return h('div.card.is-flush', { id: 'power-options' },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, 'Wake, hibernation and buttons'))),
      ...rows);
  }

  row(key, label, desc, control) {
    const node = settingRow(label, desc, control, { dataset: { key } });
    this.dirtyRows.set(key, node);
    return node;
  }

  buildApplyBar() {
    this.el.dirty = h('span.vt-dirty', {}, '');
    this.el.revert = h('button.btn.btn-secondary', { type: 'button', disabled: true, onclick: () => this.revert() }, 'Revert');
    this.el.apply = h('button.btn.btn-primary', { type: 'button', id: 'power-apply', disabled: true, onclick: () => this.apply() }, 'Apply');
    return h('div.card.vt-applybar', {}, this.el.dirty, this.el.revert, this.el.apply);
  }

  // -------------------------------------------------------------- changes

  change(key, value) {
    const original = getPath(this.model, key);
    const same = key.endsWith('Min') ? Number(original ?? 0) === Number(value) : (original ?? (typeof value === 'boolean' ? false : '')) === value;
    if (same) delete this.pending[key]; else this.pending[key] = value;
    if (key === 'hibernate.enabled' && this.el.hibernateModeRow) this.el.hibernateModeRow.classList.toggle('is-off', !value);
    this.renderDirty();
  }

  renderDirty() {
    const n = Object.keys(this.pending).length;
    if (this.el.apply) {
      this.el.apply.disabled = n === 0;
      this.el.revert.disabled = n === 0;
      this.el.dirty.textContent = n === 0 ? 'No unsaved changes' : `${n} unsaved ${n === 1 ? 'change' : 'changes'}`;
    }
    for (const [key, row] of this.dirtyRows || []) row.classList.toggle('is-dirty', key in this.pending);
  }

  revert() {
    this.pending = {};
    this.render();
  }

  async apply() {
    const keys = Object.keys(this.pending);
    if (!keys.length) return;
    const changes = nestChanges(this.pending);
    this.el.apply.disabled = true;
    this.el.apply.textContent = 'Applying…';
    try {
      const res = await this.call('power.apply', { changes });
      const applied = res?.applied || [];
      const failed = res?.failed || [];
      const needs = !!res?.needsElevation || failed.some((f) => f.needsElevation);
      if (failed.length) {
        const list = failed.map((f) => `${KEY_LABELS[f.key] || f.key}: ${f.reason}`).join('; ');
        toast(`${applied.length ? `Applied ${applied.length}, ` : ''}${failed.length} failed — ${list}`, {
          kind: needs ? 'warning' : 'danger', timeout: 9000,
          action: needs && this.platform === 'windows' ? 'Restart as administrator' : undefined,
          onAction: () => this.relaunchElevated(),
        });
      } else {
        toast(`Applied ${applied.map((k) => KEY_LABELS[k] || k).join(', ')}`, { kind: 'success' });
      }
    } catch (e) {
      const needs = e.code === 'not_authorized' && (e.details?.needsElevation || this.platform === 'windows');
      toast(e.message || 'Apply failed', {
        kind: needs ? 'warning' : 'danger', timeout: needs ? 0 : 7000,
        action: needs && this.platform === 'windows' ? 'Restart as administrator' : undefined,
        onAction: () => this.relaunchElevated(),
      });
    }
    this.el.apply.textContent = 'Apply';
    await this.load();
  }

  async relaunchElevated() {
    try {
      const r = await this.call('power.relaunchElevated');
      if (r?.alreadyElevated) toast('Vitals is already running as administrator.', { kind: 'info' });
      else toast('Restarting Vitals as administrator…', { kind: 'info', timeout: 6000 });
    } catch (e) {
      toast(e.message || 'Could not restart as administrator', { kind: e.code === 'not_supported' ? 'info' : 'danger' });
    }
  }

  async openPane() {
    try { await this.call('app.openSettingsPane', { pane: 'power' }); } catch (e) { toast(e.message, { kind: 'danger' }); }
  }

  // ----------------------------------------------------------- keep awake

  buildAwakeCard() {
    const st = this.model?.keepAwake || { enabled: false, mode: 'system' };
    const supported = this.model ? this.model.supported?.keepAwake !== false : true;
    this.el.awakeToggle = h('input', { type: 'checkbox', role: 'switch', id: 'keep-awake', checked: !!st.enabled, disabled: !supported, onchange: (e) => this.setAwake(e.target.checked, this.awakeMode()) });
    const seg = h('div.segmented', { id: 'keep-awake-mode', 'aria-label': 'Keep awake mode' },
      h('button', { type: 'button', dataset: { value: 'system' }, 'aria-pressed': String(st.mode !== 'display'), onclick: () => this.pickMode('system') }, 'System'),
      h('button', { type: 'button', dataset: { value: 'display' }, 'aria-pressed': String(st.mode === 'display'), onclick: () => this.pickMode('display') }, 'System + display'));
    this.el.awakeSeg = seg;
    this.el.awakeStatus = h('span.badge', {}, '');
    const card = h('div.card', { id: 'card-awake' },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, 'Keep awake'), h('div.card-subtitle', {}, 'Stop this computer from sleeping while Vitals runs.')), this.el.awakeStatus),
      h('div.vt-awake-row', {},
        h('label.toggle', {}, this.el.awakeToggle, h('span.toggle-track'), h('span', {}, 'Prevent sleep')),
        seg),
      supported ? null : h('div.vt-note', {}, 'Keep-awake is not supported on this platform.'));
    this.renderAwake();
    return card;
  }

  awakeMode() { return $('button[aria-pressed="true"]', this.el.awakeSeg)?.dataset.value || 'system'; }

  pickMode(mode) {
    for (const b of this.el.awakeSeg.children) b.setAttribute('aria-pressed', String(b.dataset.value === mode));
    if (this.el.awakeToggle.checked) this.setAwake(true, mode);
  }

  renderAwake() {
    const st = this.model?.keepAwake || { enabled: false, mode: 'system' };
    if (!this.el.awakeToggle) return;
    this.el.awakeToggle.checked = !!st.enabled;
    for (const b of this.el.awakeSeg.children) b.setAttribute('aria-pressed', String(b.dataset.value === (st.mode || 'system')));
    this.el.awakeStatus.className = `badge ${st.enabled ? 'badge-success badge-dot' : 'badge-outline'}`;
    this.el.awakeStatus.textContent = st.enabled ? (st.mode === 'display' ? 'Awake · display on' : 'Awake') : 'Off';
  }

  async setAwake(enabled, mode) {
    try {
      const st = await this.call('power.keepAwake', { enabled, mode });
      if (this.model) this.model.keepAwake = st;
      this.renderAwake();
    } catch (e) {
      toast(e.message, { kind: 'danger' });
      this.renderAwake();
    }
  }

  // -------------------------------------------------------------- actions

  buildActionsCard() {
    const all = ['sleep', 'displayOff', 'lock', 'restart', 'shutdown'];
    const sup = this.model?.supported?.actions;
    const actions = Array.isArray(sup) ? all.filter((a) => sup.includes(a)) : all;
    const buttons = actions.map((a) => h('button.btn', {
      type: 'button', dataset: { action: a }, class: a === 'shutdown' || a === 'restart' ? 'btn-danger' : 'btn-secondary',
      onclick: () => this.action(a),
    }, ACTION_LABELS[a] || a));
    return h('div.card', { id: 'card-actions' },
      h('div.card-header', {}, h('div', {}, h('div.card-title', {}, 'Actions'), h('div.card-subtitle', {}, 'Restart and shut down ask for confirmation.'))),
      buttons.length ? h('div.vt-actions', {}, ...buttons) : h('div.vt-note', {}, 'Power actions are not available on this platform.'));
  }

  async action(a) {
    if (a === 'restart' || a === 'shutdown') {
      const ok = await confirmDialog({
        title: a === 'restart' ? 'Restart now?' : 'Shut down now?',
        body: 'Open applications will be closed. Save your work first.',
        okLabel: ACTION_LABELS[a], danger: true,
      });
      if (!ok) return;
    }
    try {
      await this.call('power.action', { action: a });
      toast(`${ACTION_LABELS[a]}…`, { kind: 'info', timeout: 2500 });
    } catch (e) {
      toast(e.code === 'not_supported' ? `${ACTION_LABELS[a]} is not supported here` : e.message, { kind: e.code === 'not_supported' ? 'warning' : 'danger' });
    }
  }

  // ------------------------------------------------------------- schedule

  buildScheduleCard() {
    const types = this.model?.supported?.scheduleTypes || [];
    const canAdd = types.length > 0;
    this.el.scheduleList = h('div', { id: 'schedule-list' });
    const card = h('div.card.is-flush', { id: 'card-schedule' },
      h('div.card-header', {},
        h('div', {}, h('div.card-title', {}, 'Schedule'), h('div.card-subtitle', {}, 'Recurring sleep, shut down or restart.')),
        h('button.btn.btn-sm.btn-primary', { type: 'button', id: 'schedule-add', disabled: !canAdd, onclick: () => this.addScheduleDialog() }, icon('plus'), 'Add…')),
      this.el.scheduleList);
    this.renderSchedule();
    return card;
  }

  renderSchedule() {
    const list = this.el.scheduleList;
    if (!list) return;
    if (this.scheduleError && this.scheduleError.code !== 'not_supported') {
      list.replaceChildren(h('div.card-section', {}, errorCard(this.scheduleError, { title: 'Could not read the schedule' })));
      return;
    }
    const types = this.model?.supported?.scheduleTypes || [];
    if (!types.length) {
      list.replaceChildren(h('div.vt-note', { style: { padding: '0 16px 12px' } }, 'Scheduled power events are not supported on this platform.'));
      return;
    }
    if (!this.schedule.length) {
      list.replaceChildren(h('div.empty-state', {},
        h('div.empty-state-title', {}, 'No scheduled events'),
        h('div.empty-state-text', {}, 'Add a daily or weekly sleep, shutdown or restart.')));
      return;
    }
    list.replaceChildren(...this.schedule.map((it) => h('div.vt-schedule-row', { class: it.enabled ? '' : 'is-off', dataset: { id: it.id } },
      h('span.vt-sched-time', {}, it.time),
      h('div.vt-sched-main', {}, h('span.vt-sched-type', {}, ACTION_LABELS[it.type] || it.type), h('span.vt-sched-days', {}, daysLabel(it.days))),
      toggle(!!it.enabled, (v) => this.saveSchedule(this.schedule.map((x) => (x.id === it.id ? { ...x, enabled: v } : x))), { 'aria-label': `Enable ${it.type} at ${it.time}` }),
      h('button.btn.btn-sm.btn-ghost.btn-icon', { type: 'button', 'aria-label': 'Remove', title: 'Remove', onclick: () => this.saveSchedule(this.schedule.filter((x) => x.id !== it.id)) }, icon('x')))));
  }

  async saveSchedule(items) {
    try {
      const res = await this.call('power.schedule.set', { items });
      this.schedule = Array.isArray(res?.items) ? res.items : items;
      toast('Schedule saved', { kind: 'success', timeout: 2000 });
    } catch (e) {
      toast(e.message, { kind: 'danger', timeout: 6000 });
    }
    this.renderSchedule();
  }

  addScheduleDialog() {
    const types = this.model?.supported?.scheduleTypes || [];
    const typeSel = h('select', { id: 'sched-type', 'aria-label': 'Event' }, types.map((t) => h('option', { value: t }, ACTION_LABELS[t] || t)));
    const time = h('input.input', { type: 'time', id: 'sched-time', value: '23:00', required: true, 'aria-label': 'Time' });
    const days = h('div.vt-days', { role: 'group', 'aria-label': 'Days' }, WEEK.map((d) => h('label', {}, h('input', { type: 'checkbox', value: d, checked: d !== 'sat' && d !== 'sun' }), DAY_NAMES[d])));
    const ok = h('button.btn.btn-primary', { type: 'button', onclick: () => dialog.close('ok') }, 'Add');
    const cancel = h('button.btn.btn-secondary', { type: 'button', onclick: () => dialog.close('cancel') }, 'Cancel');
    const dialog = h('dialog.dialog', { id: 'schedule-dialog', 'aria-labelledby': 'sched-title' },
      h('div.dialog-body', {},
        h('h2.dialog-title', { id: 'sched-title' }, 'Add a scheduled event'),
        h('div.field', {}, h('label.label', { for: 'sched-type' }, 'Event'), h('span.select.is-block', {}, typeSel)),
        h('div.field', {}, h('label.label', { for: 'sched-time' }, 'Time'), time),
        h('div.field', {}, h('span.label', {}, 'Days'), days)),
      h('div.dialog-actions', {}, cancel, ok));
    dialog.addEventListener('cancel', (e) => { e.preventDefault(); dialog.close('cancel'); });
    dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close('cancel'); });
    dialog.addEventListener('close', () => {
      if (dialog.returnValue === 'ok' && time.value) {
        const picked = Array.from(days.querySelectorAll('input:checked')).map((i) => i.value);
        const item = { id: '', type: typeSel.value, days: picked.length === 7 ? [] : picked, time: time.value, enabled: true };
        this.saveSchedule([...this.schedule, item]);
      }
      dialog.remove();
    });
    document.body.append(dialog);
    dialog.showModal();
    typeSel.focus();
  }
}
