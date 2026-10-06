// pairing.js — the two pairing dialogs.
//
//   PairDialog  (this machine starts the pairing): address → connecting →
//               6-digit code → paired / failed. Driven by edgehop.pairing events.
//   CodeDialog  (another machine asked to pair with us): shows the code that
//               edgehop.pairCode delivered, closes on edgehop.pairResult.
import { native } from './shared/native.js';
import { h, toast } from './shared/ui.js';

const PLAT = { macos: 'macOS', windows: 'Windows', linux: 'Linux' };
export const platName = (p) => PLAT[p] || p || '';

/** Dialog that drives `edgehop.pair.start` / `edgehop.pair.confirm`. */
export class PairDialog {
  constructor() {
    this.dialog = null;
    this.stage = null;
    this.peerId = '';
    this.peerName = '';
  }

  get open() { return !!this.dialog?.open; }

  /** Open for a machine (discovered peer) or for a typed address. */
  show({ peerId = '', name = '', address = '' } = {}) {
    this.close();
    this.peerId = peerId;
    this.peerName = name;
    this.address = address;
    const titleId = 'pair-title';
    this.body = h('div.stack.gap-3.eh-pair-step');
    this.actions = h('div.dialog-actions');
    this.dialog = h('dialog.dialog', { 'aria-labelledby': titleId, id: 'pair-dialog' },
      h('div.dialog-body', {},
        h('h2.dialog-title', { id: titleId }, name ? `Pair with ${name}` : 'Pair with a machine'),
        this.body),
      this.actions);
    const dlg = this.dialog;
    dlg.addEventListener('cancel', (e) => { e.preventDefault(); this.cancel(); });
    dlg.addEventListener('close', () => { dlg.remove(); if (this.dialog === dlg) this.dialog = null; });
    document.body.append(this.dialog);
    this.dialog.showModal();
    if (peerId) this.start({ peerId });
    else this.renderAddress();
  }

  setStage(stage) {
    this.stage = stage;
    if (this.dialog) this.dialog.dataset.stage = stage;
  }

  renderAddress() {
    this.setStage('address');
    const input = h('input.input.is-wide', { id: 'pair-address', placeholder: '192.168.1.20:24800', value: this.address || '', autocomplete: 'off', spellcheck: false, 'aria-label': 'Address' });
    const go = () => {
      const address = input.value.trim();
      if (!address) { input.focus(); return; }
      this.start({ address });
    };
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); go(); } });
    this.body.replaceChildren(
      h('p.dialog-text', {}, 'Type the address shown on the other machine’s Machines page. Both machines must be on the same network and Edgehop must be running there.'),
      h('div.field', {}, h('label.label', { for: 'pair-address' }, 'Address (host:port)'), input, h('span.hint', {}, 'The port is 24800 unless it was changed in Settings.')));
    this.actions.replaceChildren(
      h('button.btn.btn-secondary', { type: 'button', onclick: () => this.cancel() }, 'Cancel'),
      h('button.btn.btn-primary', { type: 'button', id: 'pair-go', onclick: go }, 'Pair'));
    setTimeout(() => input.focus(), 0);
  }

  async start(params) {
    if (params.address) this.address = params.address;
    this.renderConnecting(params.address || this.peerName);
    try {
      await native.call('edgehop.pair.start', params);
    } catch (e) {
      this.renderFailed(e.message || 'failed');
    }
  }

  renderConnecting(where) {
    this.setStage('connecting');
    this.body.replaceChildren(h('p.dialog-text.row.gap-2', {}, h('span.eh-spinner'), h('span', {}, `Connecting to ${where || 'the machine'}…`)));
    this.actions.replaceChildren(h('button.btn.btn-secondary', { type: 'button', onclick: () => this.cancel() }, 'Cancel'));
  }

  renderCode() {
    this.setStage('waiting_code');
    const input = h('input.input.eh-code-input', { id: 'pair-code', inputmode: 'numeric', pattern: '[0-9]*', maxlength: 7, autocomplete: 'one-time-code', 'aria-label': 'Pairing code', placeholder: '······' });
    const confirm = async () => {
      const code = input.value.replace(/\D/g, '');
      if (code.length !== 6) { input.focus(); input.select(); return; }
      btn.disabled = true;
      try {
        await native.call('edgehop.pair.confirm', { peerId: this.peerId, code });
        this.renderConnecting('');
        this.body.replaceChildren(h('p.dialog-text.row.gap-2', {}, h('span.eh-spinner'), h('span', {}, 'Checking the code…')));
      } catch (e) {
        btn.disabled = false;
        toast(e.message || 'Could not send the code', { kind: 'danger' });
      }
    };
    const btn = h('button.btn.btn-primary', { type: 'button', id: 'pair-confirm', onclick: confirm }, 'Confirm');
    input.addEventListener('input', () => { input.value = input.value.replace(/\D/g, '').slice(0, 6); });
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); confirm(); } });
    this.body.replaceChildren(
      h('p.dialog-text', {}, `A 6-digit code is now showing on ${this.peerName || 'the other machine'}. Type it here to prove both machines are yours.`),
      h('div.field', {}, input, h('span.hint', {}, 'The code expires after two minutes.')));
    this.actions.replaceChildren(
      h('button.btn.btn-secondary', { type: 'button', onclick: () => this.cancel() }, 'Cancel'), btn);
    setTimeout(() => input.focus(), 0);
  }

  renderPaired(name) {
    this.setStage('paired');
    this.body.replaceChildren(h('p.dialog-text', {}, h('span.badge.badge-success.badge-dot', {}, 'Paired'), ' ', `${name || 'The machine'} is now part of your group. Place it in the Layout page.`));
    this.actions.replaceChildren(h('button.btn.btn-primary', { type: 'button', id: 'pair-done', onclick: () => this.close() }, 'Done'));
    toast(`Paired with ${name || 'the machine'}`, { kind: 'success' });
  }

  renderFailed(reason) {
    this.setStage('failed');
    const text = {
      wrong_code: 'That code was not accepted. Check the digits on the other machine and try again.',
      code_expired: 'The code expired. Start the pairing again.',
      unreachable: 'Nothing answered at that address. Is Edgehop running there, and is the port right?',
      refused: 'The other machine refused the connection.',
      cancelled: 'Pairing was cancelled.',
    }[reason] || `Pairing failed (${reason}).`;
    this.body.replaceChildren(h('p.dialog-text', {}, h('span.badge.badge-danger.badge-dot', {}, 'Not paired'), ' ', text));
    this.actions.replaceChildren(
      h('button.btn.btn-secondary', { type: 'button', onclick: () => this.close() }, 'Close'),
      h('button.btn.btn-primary', { type: 'button', id: 'pair-retry', onclick: () => (this.peerId ? this.start({ peerId: this.peerId }) : this.renderAddress()) }, 'Try again'));
  }

  /** Feed an edgehop.pairing event (or the pairing field of edgehop.state). */
  update(p) {
    if (!p) return;
    if (!this.dialog) {
      // Only an in-flight pairing (e.g. started with --peer before the window opened) reopens the dialog.
      if (p.stage !== 'connecting' && p.stage !== 'waiting_code') return;
      this.show({ peerId: '', name: p.name, address: p.address });
    }
    if (p.peerId) this.peerId = p.peerId;
    if (p.name) this.peerName = p.name;
    if (p.stage === this.stage) return; // already showing this step
    switch (p.stage) {
      case 'connecting': if (this.stage !== 'connecting') this.renderConnecting(p.address); break;
      case 'waiting_code': if (this.stage !== 'waiting_code') this.renderCode(); break;
      case 'paired': this.renderPaired(p.name); break;
      case 'failed': this.renderFailed(p.error || 'failed'); break;
      case 'cancelled': this.close(); break;
      default: break;
    }
  }

  async cancel() {
    if (this.stage === 'connecting' || this.stage === 'waiting_code') {
      try { await native.call('edgehop.pair.cancel'); } catch { /* nothing to cancel */ }
    }
    this.close();
  }

  close() {
    if (this.dialog?.open) this.dialog.close();
    this.dialog = null;
    this.stage = null;
  }
}

/** Dialog showing the code generated for an incoming pairing request. */
export class CodeDialog {
  constructor() { this.dialog = null; this.timer = 0; }

  get open() { return !!this.dialog?.open; }

  show({ code, peer, ttlSec = 120 }) {
    this.close();
    const digits = String(code || '').padStart(6, '0');
    const name = peer?.name || 'Another machine';
    const left = h('span.text-muted.text-sm', { id: 'code-ttl' });
    const status = h('p.dialog-text', { id: 'code-status' }, `${name}${peer?.plat ? ` (${platName(peer.plat)})` : ''} wants to pair with this machine. Type this code there:`);
    this.dialog = h('dialog.dialog', { 'aria-labelledby': 'code-title', id: 'code-dialog' },
      h('div.dialog-body', {},
        h('h2.dialog-title', { id: 'code-title' }, 'Pairing request'),
        status,
        h('div.eh-code', { id: 'pair-code-display', 'aria-label': `Pairing code ${digits.split('').join(' ')}` }, h('span', {}, digits.slice(0, 3)), h('span', {}, digits.slice(3))),
        h('p.text-center', {}, left),
        h('p.hint.text-center', {}, 'Only confirm a code on a machine you expected to pair. Anyone who knows it can control this computer.')),
      h('div.dialog-actions', {}, h('button.btn.btn-secondary', { type: 'button', id: 'code-dismiss', onclick: () => this.close() }, 'Dismiss')));
    const dlg = this.dialog;
    dlg.addEventListener('close', () => { dlg.remove(); if (this.dialog === dlg) { this.dialog = null; clearInterval(this.timer); } });
    document.body.append(this.dialog);
    this.dialog.showModal();
    const until = Date.now() + ttlSec * 1000;
    const tick = () => {
      const s = Math.max(0, Math.round((until - Date.now()) / 1000));
      left.textContent = s > 0 ? `Expires in ${s} s` : 'Expired';
      if (s <= 0) clearInterval(this.timer);
    };
    tick();
    this.timer = setInterval(tick, 1000);
  }

  /** Feed an edgehop.pairResult event. */
  result(r) {
    if (!this.dialog) return;
    const status = this.dialog.querySelector('#code-status');
    if (r.ok) {
      toast(`Paired with ${r.name || 'the machine'}`, { kind: 'success' });
      setTimeout(() => this.close(), 600);
    } else if (status) {
      status.replaceChildren(h('span.badge.badge-danger.badge-dot', {}, 'Failed'), ' ', `Pairing with ${r.name || 'the machine'} failed (${r.error || 'error'}). It can try again.`);
      clearInterval(this.timer);
      this.dialog.querySelector('#code-ttl').textContent = '';
    }
  }

  close() {
    clearInterval(this.timer);
    if (this.dialog?.open) this.dialog.close();
    this.dialog = null;
  }
}
