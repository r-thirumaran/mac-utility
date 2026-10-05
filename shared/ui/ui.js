// ui.js — tiny DOM helpers for the shared UI kit. No framework, no globals.
//
//   import { h, $, tabs, toast, confirmDialog } from './shared/ui.js';
//   mount('#app', h('div.card', {}, h('h2', {}, 'Hello'), h('button.btn.btn-primary', { onclick: save }, 'Save')));

let uidCounter = 0;
const uid = (prefix = 'ui') => `${prefix}-${++uidCounter}`;

const PROP_KEYS = new Set(['value', 'checked', 'selected', 'disabled', 'hidden', 'readOnly', 'tabIndex', 'indeterminate', 'open', 'htmlFor', 'textContent']);
const PROP_ALIASES = { for: 'htmlFor', readonly: 'readOnly', tabindex: 'tabIndex' };

function setAttr(node, key, value) {
  if (value == null || value === false) return;
  if (key === 'class' || key === 'className') {
    const cls = Array.isArray(value) ? value.filter(Boolean).join(' ') : String(value);
    node.className = node.className ? `${node.className} ${cls}` : cls;
    return;
  }
  if (key === 'style') {
    if (typeof value === 'string') { node.style.cssText = value; return; }
    for (const prop of Object.keys(value)) {
      if (prop.startsWith('--')) node.style.setProperty(prop, value[prop]);
      else node.style[prop] = value[prop];
    }
    return;
  }
  if (key === 'dataset') { for (const d of Object.keys(value)) node.dataset[d] = value[d]; return; }
  if (key === 'aria' && typeof value === 'object') { for (const a of Object.keys(value)) node.setAttribute(`aria-${a}`, value[a]); return; }
  if (key === 'text') { node.textContent = value; return; }
  if (key === 'ref' && typeof value === 'function') { value(node); return; }
  if (key.length > 2 && key.startsWith('on') && typeof value === 'function') {
    node.addEventListener(key.slice(2).toLowerCase(), value);
    return;
  }
  const prop = PROP_ALIASES[key] || key;
  if (PROP_KEYS.has(prop)) { node[prop] = value; return; }
  node.setAttribute(key, value === true ? '' : String(value));
}

function appendChildren(node, children) {
  for (const child of children) {
    if (child == null || child === false || child === true) continue;
    if (Array.isArray(child)) { appendChildren(node, child); continue; }
    if (child instanceof Node) { node.append(child); continue; }
    node.append(document.createTextNode(String(child)));
  }
}

/**
 * Create an element. `tag` may carry classes ('div.card.is-flush'). `attrs` keys:
 * `class`/`className` (string or array), `style` (string or object, custom properties ok),
 * `dataset` (object), `aria` (object → aria-* attributes), `text`, `ref(node)`,
 * `on<event>` handlers (`onclick`, `oninput`, …), form properties (`value`, `checked`,
 * `disabled`, …) and any other attribute. Children: nodes, strings, numbers, arrays,
 * or null/false (skipped). Text children are escaped by construction.
 * @param {string} tag
 * @param {object|Node|string} [attrs] attributes, or the first child
 * @param {...(Node|string|number|Array|null|false)} children
 * @returns {HTMLElement}
 */
export function h(tag, attrs, ...children) {
  const parts = String(tag).split('.');
  const node = document.createElement(parts[0] || 'div');
  if (parts.length > 1) node.className = parts.slice(1).filter(Boolean).join(' ');
  if (attrs != null && (typeof attrs !== 'object' || attrs instanceof Node || Array.isArray(attrs))) {
    children.unshift(attrs);
    attrs = null;
  }
  if (attrs) for (const key of Object.keys(attrs)) setAttr(node, key, attrs[key]);
  appendChildren(node, children);
  return node;
}

/**
 * First element matching `selector` under `root`.
 * @param {string} selector
 * @param {ParentNode} [root=document]
 * @returns {Element|null}
 */
export function $(selector, root = document) { return root.querySelector(selector); }

/**
 * All elements matching `selector` under `root`, as a real array.
 * @param {string} selector
 * @param {ParentNode} [root=document]
 * @returns {Element[]}
 */
export function $$(selector, root = document) { return Array.from(root.querySelectorAll(selector)); }

/**
 * Replace the children of `parent` (element or selector) with `node` (or an array of nodes).
 * @param {Element|string} parent
 * @param {Node|Node[]} node
 * @returns {Node|Node[]} the mounted node(s)
 */
export function mount(parent, node) {
  const target = typeof parent === 'string' ? $(parent) : parent;
  if (!target) throw new Error(`mount(): parent not found: ${parent}`);
  target.replaceChildren(...(Array.isArray(node) ? node : [node]));
  return node;
}

// ---------------------------------------------------------------- icons

const ICON_PATHS = {
  check: 'M5 12.5l4.5 4.5L19 7.5',
  x: 'M6 6l12 12M18 6L6 18',
  info: 'M12 3a9 9 0 110 18 9 9 0 010-18zm0 7.5V17m0-6.5h.01M12 7.5h.01',
  warning: 'M12 4l9.5 16.5h-19L12 4zm0 6v4.5m0 3h.01',
  danger: 'M12 3a9 9 0 110 18 9 9 0 010-18zm0 4.5V13m0 3.5h.01',
  search: 'M10.5 4a6.5 6.5 0 110 13 6.5 6.5 0 010-13zm5 11.5L20 20',
  'chevron-down': 'M6 9.5l6 6 6-6',
  'chevron-right': 'M9.5 6l6 6-6 6',
  plus: 'M12 5v14M5 12h14',
  minus: 'M5 12h14',
  refresh: 'M20 12a8 8 0 11-2.34-5.66M20 4v5h-5',
  more: 'M6 12h.01M12 12h.01M18 12h.01',
  inbox: 'M4 13V6.5A1.5 1.5 0 015.5 5h13A1.5 1.5 0 0120 6.5V13m-16 0v4.5A1.5 1.5 0 005.5 19h13a1.5 1.5 0 001.5-1.5V13m-16 0h4l1.5 2.5h5L15.5 13H20',
  power: 'M12 3v9m6.36-5.36a9 9 0 11-12.72 0',
  cpu: 'M8 8h8v8H8V8zm-3-3h14v14H5V5zm4-3v3m6-3v3M9 19v3m6-3v3M2 9h3m-3 6h3m14-6h3m-3 6h3',
};

/**
 * Inline SVG icon (stroke = currentColor). Names: check, x, info, warning, danger, search,
 * chevron-down, chevron-right, plus, minus, refresh, more, inbox, power, cpu.
 * @param {string} name
 * @param {object} [opts]
 * @param {number} [opts.size=16] width/height in px
 * @param {string} [opts.className]
 * @param {string} [opts.label] accessible label; omitted → aria-hidden
 * @returns {SVGSVGElement}
 */
export function icon(name, { size = 16, className, label } = {}) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', String(size));
  svg.setAttribute('height', String(size));
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.8');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  if (className) svg.setAttribute('class', className);
  if (label) { svg.setAttribute('role', 'img'); svg.setAttribute('aria-label', label); } else svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', ICON_PATHS[name] || ICON_PATHS.info);
  svg.append(path);
  return svg;
}

// ----------------------------------------------------------------- tabs

/**
 * Wire a `.tabs` strip: sets tablist/tab/tabpanel roles, aria-selected, roving tabindex,
 * Left/Right/Home/End keys, and shows the matching panel (`data-panel="id"` or
 * `aria-controls` on each `.tab`). The initially selected tab is the one with
 * `aria-selected="true"`, else the first.
 * @param {Element|string} container the `.tabs` element, or an ancestor containing it
 * @param {object} [opts]
 * @param {(id: string|number, tab: Element, panel: Element|null) => void} [opts.onChange]
 * @returns {{select: (target: number|string|Element) => void, index: number, tab: Element, destroy: () => void}}
 */
export function tabs(container, { onChange } = {}) {
  const root = typeof container === 'string' ? $(container) : container;
  const list = root && (root.classList.contains('tabs') ? root : root.querySelector('.tabs'));
  if (!list) throw new Error('tabs(): no .tabs element found');
  list.setAttribute('role', 'tablist');
  const scope = list === root ? (root.parentElement || document) : root;
  const items = $$('.tab', list);
  const panels = items.map((tab) => {
    tab.setAttribute('role', 'tab');
    if (tab.tagName === 'BUTTON' && !tab.hasAttribute('type')) tab.type = 'button';
    if (!tab.id) tab.id = uid('tab');
    const target = tab.dataset.panel || tab.getAttribute('aria-controls');
    let panel = null;
    if (target) panel = scope.querySelector(`#${CSS.escape(target)}`) || document.getElementById(target);
    if (panel) {
      panel.setAttribute('role', 'tabpanel');
      if (!panel.id) panel.id = uid('panel');
      panel.setAttribute('aria-labelledby', tab.id);
      tab.setAttribute('aria-controls', panel.id);
    }
    return panel;
  });
  let current = -1;

  function indexOf(target) {
    if (typeof target === 'number') return target;
    if (typeof target === 'string') return items.findIndex((t) => t.dataset.tab === target || t.id === target || t.dataset.panel === target);
    return items.indexOf(target);
  }

  function select(target, { silent = false } = {}) {
    const index = indexOf(target);
    if (index < 0 || index >= items.length) return;
    current = index;
    items.forEach((tab, i) => {
      const on = i === index;
      tab.setAttribute('aria-selected', String(on));
      tab.tabIndex = on ? 0 : -1;
      if (panels[i]) panels[i].hidden = !on;
    });
    if (!silent && onChange) onChange(items[index].dataset.tab ?? index, items[index], panels[index]);
  }

  const onClick = (e) => {
    const tab = e.target.closest('.tab');
    if (tab && items.includes(tab)) select(items.indexOf(tab));
  };
  const onKey = (e) => {
    const i = items.indexOf(document.activeElement);
    if (i < 0) return;
    let n = i;
    switch (e.key) {
      case 'ArrowRight': case 'ArrowDown': n = (i + 1) % items.length; break;
      case 'ArrowLeft': case 'ArrowUp': n = (i - 1 + items.length) % items.length; break;
      case 'Home': n = 0; break;
      case 'End': n = items.length - 1; break;
      default: return;
    }
    e.preventDefault();
    items[n].focus();
    select(n);
  };
  list.addEventListener('click', onClick);
  list.addEventListener('keydown', onKey);
  const initial = items.findIndex((t) => t.getAttribute('aria-selected') === 'true');
  select(initial < 0 ? 0 : initial, { silent: true });

  return {
    select,
    get index() { return current; },
    get tab() { return items[current]; },
    destroy() { list.removeEventListener('click', onClick); list.removeEventListener('keydown', onKey); },
  };
}

// ------------------------------------------------------------- segmented

/**
 * Wire a `.segmented` control made of `<button data-value="…">` children: aria-pressed,
 * roving tabindex and arrow keys. The initial value is the button with
 * `aria-pressed="true"`, else the first.
 * @param {Element|string} container
 * @param {object} [opts]
 * @param {(value: string, button: Element) => void} [opts.onChange] called when the value changes
 * @returns {{select: (target: number|string|Element, opts?: {silent?: boolean}) => void, value: string, destroy: () => void}}
 */
export function segmented(container, { onChange } = {}) {
  const root = typeof container === 'string' ? $(container) : container;
  if (!root) throw new Error('segmented(): container not found');
  const buttons = $$(':scope > button', root);
  root.setAttribute('role', 'group');
  let value = null;
  const valueOf = (b) => b.dataset.value ?? b.textContent.trim();

  function select(target, { silent = false } = {}) {
    const btn = typeof target === 'number' ? buttons[target]
      : typeof target === 'string' ? buttons.find((b) => valueOf(b) === target)
      : target;
    if (!btn || !buttons.includes(btn)) return;
    for (const b of buttons) {
      const on = b === btn;
      b.setAttribute('aria-pressed', String(on));
      b.tabIndex = on ? 0 : -1;
      if (!b.hasAttribute('type')) b.type = 'button';
    }
    const next = valueOf(btn);
    const changed = next !== value;
    value = next;
    if (changed && !silent && onChange) onChange(value, btn);
  }

  const onClick = (e) => { const b = e.target.closest('button'); if (b && buttons.includes(b)) select(b); };
  const onKey = (e) => {
    const i = buttons.indexOf(document.activeElement);
    if (i < 0) return;
    let n = i;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') n = (i + 1) % buttons.length;
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') n = (i - 1 + buttons.length) % buttons.length;
    else if (e.key === 'Home') n = 0;
    else if (e.key === 'End') n = buttons.length - 1;
    else return;
    e.preventDefault();
    buttons[n].focus();
    select(n);
  };
  root.addEventListener('click', onClick);
  root.addEventListener('keydown', onKey);
  const initial = buttons.findIndex((b) => b.getAttribute('aria-pressed') === 'true');
  select(initial < 0 ? 0 : initial, { silent: true });

  return {
    select,
    get value() { return value; },
    destroy() { root.removeEventListener('click', onClick); root.removeEventListener('keydown', onKey); },
  };
}

// ---------------------------------------------------------------- slider

/**
 * Keep a `.slider` range input's filled track (`--slider-fill`) in sync with its value.
 * Call `update()` after setting `input.value` from code.
 * @param {HTMLInputElement} input
 * @returns {{update: () => void, destroy: () => void}}
 */
export function sliderFill(input) {
  const update = () => {
    const min = input.min === '' ? 0 : +input.min;
    const max = input.max === '' ? 100 : +input.max;
    const pct = max > min ? ((+input.value - min) / (max - min)) * 100 : 0;
    input.style.setProperty('--slider-fill', `${Math.max(0, Math.min(100, pct)).toFixed(2)}%`);
  };
  update();
  input.addEventListener('input', update);
  input.addEventListener('change', update);
  return { update, destroy() { input.removeEventListener('input', update); input.removeEventListener('change', update); } };
}

// ----------------------------------------------------------------- toast

let toastRegion = null;
const TOAST_ICON = { info: 'info', success: 'check', warning: 'warning', danger: 'danger' };

/**
 * Show a transient message at the bottom of the window.
 * @param {string|Node} message
 * @param {object} [opts]
 * @param {'info'|'success'|'warning'|'danger'} [opts.kind='info']
 * @param {number} [opts.timeout=3500] ms before auto-dismiss; 0 keeps it until closed
 * @param {string} [opts.action] label of an optional action button
 * @param {() => void} [opts.onAction]
 * @returns {{el: HTMLElement, dismiss: () => void}}
 */
export function toast(message, { kind = 'info', timeout = 3500, action, onAction } = {}) {
  if (!toastRegion || !toastRegion.isConnected) {
    toastRegion = h('div.toast-region', { role: 'status', 'aria-live': 'polite' });
    document.body.append(toastRegion);
  }
  let timer = 0;
  const dismiss = () => {
    if (!item.isConnected || item.classList.contains('is-leaving')) return;
    clearTimeout(timer);
    item.classList.add('is-leaving');
    const done = () => item.remove();
    item.addEventListener('animationend', done, { once: true });
    setTimeout(done, 250);
  };
  const item = h(`div.toast.toast-${kind}`, {},
    icon(TOAST_ICON[kind] || 'info', { size: 18, className: 'toast-icon' }),
    h('div.toast-text', {}, message),
    action ? h('button.btn.btn-ghost.btn-sm', { type: 'button', onclick: () => { onAction?.(); dismiss(); } }, action) : null,
    h('button.btn.btn-ghost.btn-sm.btn-icon.toast-close', { type: 'button', 'aria-label': 'Dismiss', onclick: dismiss }, icon('x', { size: 14 })),
  );
  while (toastRegion.children.length >= 3) toastRegion.firstElementChild.remove();
  toastRegion.append(item);
  if (timeout > 0) timer = setTimeout(dismiss, timeout);
  return { el: item, dismiss };
}

// --------------------------------------------------------------- dialogs

/**
 * Modal confirmation. Resolves `true` for OK, `false` for Cancel / Escape / backdrop click.
 * @param {object} [opts]
 * @param {string} [opts.title='Are you sure?']
 * @param {string|Node} [opts.body]
 * @param {string} [opts.okLabel='OK']
 * @param {string} [opts.cancelLabel='Cancel']
 * @param {boolean} [opts.danger=false] destructive action: red OK button, focus starts on Cancel
 * @returns {Promise<boolean>}
 */
export function confirmDialog({ title = 'Are you sure?', body = '', okLabel = 'OK', cancelLabel = 'Cancel', danger = false } = {}) {
  return new Promise((resolve) => {
    const titleId = uid('dialog-title');
    const ok = h('button.btn', { type: 'button', class: danger ? 'btn-danger' : 'btn-primary', onclick: () => dialog.close('ok') }, okLabel);
    const cancel = h('button.btn.btn-secondary', { type: 'button', onclick: () => dialog.close('cancel') }, cancelLabel);
    const dialog = h('dialog.dialog', { 'aria-labelledby': titleId },
      h('div.dialog-body', {},
        h('h2.dialog-title', { id: titleId }, title),
        body ? h('div.dialog-text', {}, body) : null),
      h('div.dialog-actions', {}, cancel, ok));
    dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close('cancel'); });
    dialog.addEventListener('cancel', (e) => { e.preventDefault(); dialog.close('cancel'); });
    dialog.addEventListener('close', () => { resolve(dialog.returnValue === 'ok'); dialog.remove(); });
    document.body.append(dialog);
    dialog.showModal();
    (danger ? cancel : ok).focus();
  });
}

// ---------------------------------------------------------------- timing

/**
 * Trailing-edge debounce: `fn` runs `wait` ms after the last call. `.cancel()` drops a pending call.
 * @template {Function} F
 * @param {F} fn
 * @param {number} [wait=150]
 * @returns {F & {cancel: () => void}}
 */
export function debounce(fn, wait = 150) {
  let timer = 0;
  const debounced = (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => { timer = 0; fn(...args); }, wait);
  };
  debounced.cancel = () => { clearTimeout(timer); timer = 0; };
  return debounced;
}

/**
 * Leading + trailing throttle: `fn` runs at most once per `wait` ms, with the last
 * arguments delivered at the end of the window. `.cancel()` drops a pending trailing call.
 * @template {Function} F
 * @param {F} fn
 * @param {number} [wait=100]
 * @returns {F & {cancel: () => void}}
 */
export function throttle(fn, wait = 100) {
  let last = 0;
  let timer = 0;
  let pendingArgs = null;
  const throttled = (...args) => {
    const now = Date.now();
    const remaining = wait - (now - last);
    if (remaining <= 0) {
      clearTimeout(timer);
      timer = 0;
      last = now;
      fn(...args);
    } else {
      pendingArgs = args;
      if (!timer) {
        timer = setTimeout(() => {
          timer = 0;
          last = Date.now();
          const a = pendingArgs;
          pendingArgs = null;
          fn(...a);
        }, remaining);
      }
    }
  };
  throttled.cancel = () => { clearTimeout(timer); timer = 0; pendingArgs = null; };
  return throttled;
}

/**
 * Human-friendly relative time: "just now", "5 min ago", "2 h ago", "yesterday", "3 d ago", "in 2 h".
 * @param {Date|number|string} date
 * @param {number} [now=Date.now()] reference time in ms
 * @returns {string}
 */
export function relativeTime(date, now = Date.now()) {
  const t = date instanceof Date ? date.getTime() : typeof date === 'number' ? date : new Date(date).getTime();
  if (!Number.isFinite(t)) return '';
  const diff = (now - t) / 1000;
  const past = diff >= 0;
  const s = Math.abs(diff);
  const fmt = (n, unit) => (past ? `${n} ${unit} ago` : `in ${n} ${unit}`);
  if (s < 45) return past ? 'just now' : 'in a moment';
  if (s < 90) return fmt(1, 'min');
  const m = Math.round(s / 60);
  if (m < 45) return fmt(m, 'min');
  const hrs = Math.round(s / 3600);
  if (hrs < 2) return fmt(1, 'h');
  if (hrs < 22) return fmt(hrs, 'h');
  const d = Math.round(s / 86400);
  if (d < 2) return past ? 'yesterday' : 'tomorrow';
  if (d < 26) return fmt(d, 'd');
  const mo = Math.round(s / (86400 * 30.4));
  if (mo < 2) return fmt(1, 'mo');
  if (mo < 11) return fmt(mo, 'mo');
  return fmt(Math.max(1, Math.round(s / (86400 * 365.25))), 'y');
}

// ----------------------------------------------------------------- theme

const THEME_KEY = 'ui.theme';

/**
 * Pin the theme ('light' | 'dark') or follow the OS ('auto'). Sets `data-theme` on <html>
 * and remembers the choice in localStorage (ignored when storage is unavailable).
 * @param {'light'|'dark'|'auto'} theme
 * @returns {'light'|'dark'|'auto'} the applied value
 */
export function setTheme(theme) {
  const t = theme === 'dark' || theme === 'light' ? theme : 'auto';
  if (t === 'auto') delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  try {
    if (t === 'auto') localStorage.removeItem(THEME_KEY); else localStorage.setItem(THEME_KEY, t);
  } catch { /* private mode / blocked storage */ }
  return t;
}

/**
 * The pinned theme, or 'auto' when following the OS.
 * @returns {'light'|'dark'|'auto'}
 */
export function getTheme() {
  const t = document.documentElement.dataset.theme;
  return t === 'dark' || t === 'light' ? t : 'auto';
}

/**
 * Re-apply the theme remembered by `setTheme`. Call once at startup, before first paint if possible.
 * @returns {'light'|'dark'|'auto'}
 */
export function applyStoredTheme() {
  let t = 'auto';
  try { t = localStorage.getItem(THEME_KEY) || 'auto'; } catch { /* ignore */ }
  if (t === 'dark' || t === 'light') document.documentElement.dataset.theme = t;
  return getTheme();
}

/**
 * Whether the page currently renders dark (pinned, or OS preference under 'auto').
 * @returns {boolean}
 */
export function isDark() {
  const t = getTheme();
  if (t !== 'auto') return t === 'dark';
  return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches;
}
