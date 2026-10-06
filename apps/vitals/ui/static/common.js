// common.js — small helpers shared by the Vitals pages (labels, option lists,
// the inline error card). Nothing here touches the bridge.
import { h, icon } from './shared/ui.js';

/** Friendly names for the model's action values. */
export const ACTION_LABELS = {
  nothing: 'Do nothing',
  sleep: 'Sleep',
  hibernate: 'Hibernate',
  shutdown: 'Shut down',
  displayOff: 'Turn off the display',
  restart: 'Restart',
  lock: 'Lock',
  wake: 'Wake',
};

/** Friendly names for the dotted settings keys (toasts, failures). */
export const KEY_LABELS = {
  'onBattery.displaySleepMin': 'display sleep (battery)',
  'onBattery.systemSleepMin': 'system sleep (battery)',
  'onBattery.lidClose': 'lid close (battery)',
  'onPower.displaySleepMin': 'display sleep (plugged in)',
  'onPower.systemSleepMin': 'system sleep (plugged in)',
  'onPower.lidClose': 'lid close (plugged in)',
  lidOpenWakes: 'wake on lid open',
  wakeOnNetwork: 'wake for network access',
  powerNap: 'Power Nap',
  lowPowerMode: 'low power mode',
  'hibernate.enabled': 'hibernation',
  'hibernate.mode': 'hibernation file',
  powerButton: 'power button',
};

/** Sleep timer choices in minutes; 0 = never. */
export const SLEEP_MINUTES = [1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 0];

/**
 * Label for a sleep timer value in minutes.
 * @param {number} min
 * @returns {string}
 */
export function minutesLabel(min) {
  if (!min) return 'Never';
  if (min % 60 === 0) return min === 60 ? '1 hour' : `${min / 60} hours`;
  if (min > 60) return `${(min / 60).toFixed(1).replace(/\.0$/, '')} hours`;
  return min === 1 ? '1 minute' : `${min} minutes`;
}

export const DAY_NAMES = { mon: 'Mon', tue: 'Tue', wed: 'Wed', thu: 'Thu', fri: 'Fri', sat: 'Sat', sun: 'Sun' };
export const WEEK = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];

/**
 * "Every day", "Weekdays", "Weekends" or "Mon, Wed, Fri".
 * @param {string[]} days
 */
export function daysLabel(days) {
  if (!days || days.length === 0 || days.length === 7) return 'Every day';
  const set = new Set(days);
  if (set.size === 5 && ['mon', 'tue', 'wed', 'thu', 'fri'].every((d) => set.has(d))) return 'Weekdays';
  if (set.size === 2 && set.has('sat') && set.has('sun')) return 'Weekends';
  return WEEK.filter((d) => set.has(d)).map((d) => DAY_NAMES[d]).join(', ');
}

/**
 * An inline error card for a failed backend call. `needs_permission` and
 * `not_supported` get their own tone and copy; an optional action button
 * (Open Settings, Retry) is rendered when given.
 * @param {Error & {code?: string}} err
 * @param {object} [opts]
 * @param {string} [opts.title]
 * @param {string} [opts.action] button label
 * @param {() => void} [opts.onAction]
 * @returns {HTMLElement}
 */
export function errorCard(err, { title, action, onAction } = {}) {
  const code = err?.code || 'internal';
  let tone = '';
  let heading = title;
  let iconName = 'danger';
  if (code === 'not_supported') { tone = 'is-info'; heading ||= 'Not available on this platform'; iconName = 'info'; }
  else if (code === 'needs_permission' || code === 'not_authorized') { tone = 'is-warning'; heading ||= 'Permission needed'; iconName = 'warning'; }
  else if (code === 'disconnected') { tone = 'is-warning'; heading ||= 'Backend disconnected'; iconName = 'warning'; }
  else heading ||= 'Something went wrong';
  return h('div.card.vt-error', { class: tone, role: 'alert', dataset: { code } },
    icon(iconName, { size: 18 }),
    h('div.stack.gap-1.grow', {},
      h('div.vt-error-title', {}, heading),
      h('div.vt-error-text', {}, err?.message || String(err)),
      action ? h('div', {}, h('button.btn.btn-sm.btn-primary', { type: 'button', onclick: onAction }, action)) : null));
}

/**
 * `<span class="select"><select>` with the given options.
 * @param {{value: string|number, label: string}[]} options
 * @param {string|number} value
 * @param {(value: string) => void} onChange
 * @param {object} [attrs] extra attributes for the <select>
 */
export function select(options, value, onChange, attrs = {}) {
  const sel = h('select', { ...attrs, onchange: (e) => onChange(e.target.value) },
    options.map((o) => h('option', { value: String(o.value), selected: String(o.value) === String(value) }, o.label)));
  return h('span.select', {}, sel);
}

/**
 * A `.toggle` switch.
 * @param {boolean} checked
 * @param {(checked: boolean) => void} onChange
 * @param {object} [attrs] extra attributes for the input
 */
export function toggle(checked, onChange, attrs = {}) {
  return h('label.toggle', {},
    h('input', { type: 'checkbox', role: 'switch', checked, ...attrs, onchange: (e) => onChange(e.target.checked) }),
    h('span.toggle-track'));
}

/**
 * A `.setting` row with label, optional description and a control.
 * @param {string} label
 * @param {string|null} desc
 * @param {Node} control
 * @param {object} [attrs]
 */
export function settingRow(label, desc, control, attrs = {}) {
  return h('div.setting', attrs,
    h('div.setting-main', {}, h('div.setting-label', {}, label), desc ? h('div.setting-desc', {}, desc) : null),
    h('div.setting-control', {}, control));
}

/** Clamp helper. */
export const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
