// power.js — lock / sleep / display off / restart / shut down, each confirmed.
import { conn, tryCall, svg, ICONS, LOCAL } from './core.js';
import { h, mount, toast, confirmDialog } from './shared/ui.js';

const ACTIONS = [
  { action: 'lock', label: 'Lock', icon: ICONS.lock, body: 'The laptop locks and asks for the password to continue.' },
  { action: 'sleep', label: 'Sleep', icon: ICONS.moon, body: 'The laptop goes to sleep; wake it from its own keyboard.' },
  { action: 'displayOff', label: 'Display off', icon: ICONS.display, body: 'Turns the screen off; any key or mouse movement turns it back on.' },
  { action: 'restart', label: 'Restart', icon: ICONS.restart, danger: true, body: 'Unsaved work may be lost. Reach comes back once the laptop is up again.' },
  { action: 'shutdown', label: 'Shut down', icon: ICONS.power, danger: true, body: 'Unsaved work may be lost. You will need to be at the laptop to turn it on.' },
];

let root = null;

function render() {
  if (!root) return;
  const name = conn.laptop?.name || 'the laptop';
  mount(root, [
    h('div.page-head', {}, h('h1.page-title', {}, 'Power')),
    (!LOCAL && !conn.laptop) ? h('div.card.empty-state', {}, h('div.empty-state-title', {}, 'No laptop selected'), h('a.btn.btn-primary', { href: '#laptops' }, 'Go to Laptops')) :
      h('div.power-grid', {}, ACTIONS.map((a) => h('button.btn.btn-secondary.power-btn', { type: 'button', class: a.danger ? 'is-danger' : '', dataset: { power: a.action }, onclick: async () => {
        const ok = await confirmDialog({ title: `${a.label} ${name}?`, body: a.body, okLabel: a.label, danger: !!a.danger });
        if (!ok) return;
        if (await tryCall('power', { action: a.action }) !== undefined) toast(`${a.label}: sent to ${name}`, { kind: 'success' });
      } }, svg(a.icon), a.label))),
    h('p.text-sm.text-muted', {}, 'Every action asks for confirmation. Restart and shut down close apps without saving.'),
  ]);
}

export default {
  id: 'power', title: 'Power', icon: ICONS.power,
  show(container) { root = container; render(); },
  hide() { root = null; },
};
