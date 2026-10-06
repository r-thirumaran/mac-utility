// Edgehop end to end: two Linux instances with fake input devices pair
// through the UI (address on the left, code read from the right), lay
// themselves out side by side with the drag-and-drop editor, then the left
// machine's fake pointer is pushed over its right edge and control moves to
// the right machine, whose fake injector receives MoveTo + relative moves.
import { mkdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

/** Call a bridge method from inside the page (same module instance as the app). */
const rpc = (page, method, params = {}) => page.evaluate(async ([m, p]) => {
  const { native } = await import('/shared/native.js');
  return native.call(m, p);
}, [method, params]);

const state = (page) => page.evaluate(() => window.__edgehop.state);
const PAIR_TIMEOUT = 60000; // pairing and handshake stages: slow CI runners
const waitState = (page, pred, msg, timeout = 20000) =>
  page.waitForFunction(pred, null, { timeout }).catch(async (e) => {
    await page.screenshot({ path: resolve(here, '..', '..', 'test-results', 'edgehop-failure.png') }).catch(() => {});
    throw new Error(`${msg}: ${e.message}`);
  });

/** Remove transient toasts so screenshots show the page itself. */
const clearToasts = (page) => page.evaluate(() => document.querySelectorAll('.toast-region .toast').forEach((el) => el.remove()));

/** Drag a layout tile onto a grid cell with real pointer events. */
async function dragTile(page, id, col, row) {
  const tile = page.locator(`.eh-tile[data-id="${id}"]`).first();
  const cell = page.locator(`.eh-cell[data-col="${col}"][data-row="${row}"]`);
  await tile.scrollIntoViewIfNeeded();
  const tb = await tile.boundingBox();
  const cb = await cell.boundingBox();
  await page.mouse.move(tb.x + tb.width / 2, tb.y + tb.height / 2);
  await page.mouse.down();
  await page.mouse.move(tb.x + tb.width / 2 + 12, tb.y + tb.height / 2 + 8, { steps: 3 });
  await page.mouse.move(cb.x + cb.width / 2, cb.y + cb.height / 2, { steps: 10 });
  await page.mouse.up();
}

export default async function (t) {
  mkdirSync(resolve(here, '..', '..', 'test-results'), { recursive: true });
  const errors = [];
  const watch = (page, who) => {
    page.on('pageerror', (e) => errors.push(`${who} pageerror: ${e.message}`));
    page.on('console', (m) => { if (m.type() === 'error') errors.push(`${who} console: ${m.text()}`); });
  };

  const dirL = mkdtempSync(join(tmpdir(), 'edgehop-left-'));
  const dirR = mkdtempSync(join(tmpdir(), 'edgehop-right-'));
  const left = await t.launchApp('edgehop', ['--fake-input', '--no-mdns', '--name', 'Left', '--config-dir', dirL, '--port', '0']);
  const right = await t.launchApp('edgehop', ['--fake-input', '--no-mdns', '--name', 'Right', '--config-dir', dirR, '--port', '0', '--fake-displays', '2560x1440;1920x1080+2560+180']);
  const L = left.page;
  const R = right.page;
  watch(L, 'left');
  watch(R, 'right');
  const ready = (page) => page.waitForFunction(() => window.__edgehop && window.__edgehop.ready, null, { timeout: 30000 });
  await ready(L);
  await ready(R);

  // ---------------------------------------------------------------- start state
  const sL = await state(L);
  const sR = await state(R);
  await t.expect(sL.self.name, 'Left');
  await t.expect(sR.self.name, 'Right');
  await t.expect(sL.self.active, true, 'a lone machine is active');
  await t.expect(sR.self.displays.length, 2, 'fake displays from the flag');
  await t.expect(sL.peers.length, 0, 'nothing paired yet');
  await t.expect(sR.port > 0, true, 'listening port in state');
  await t.expect(await L.textContent('#status-active'), /Pointer is here/);
  await t.expect(await L.$eval('#peer-list .empty-state-title', (e) => e.textContent), /No machines paired/);

  // ---------------------------------------------------------------- pairing
  await L.click('#btn-pair-address');
  await L.waitForSelector('#pair-dialog[open][data-stage="address"]');
  await L.fill('#pair-address', `127.0.0.1:${sR.port}`);
  await L.click('#pair-go');
  await R.waitForSelector('#code-dialog[open]', { timeout: PAIR_TIMEOUT });
  const code = await R.$eval('#pair-code-display', (el) => el.textContent.replace(/\D/g, ''));
  await t.expect(code.length, 6, `pairing code shown on Right (${code})`);
  await t.expect(await R.textContent('#code-status'), /Left.*wants to pair/);
  // The backend reports the stage explicitly; the dialog follows it.
  await waitState(L, () => window.__edgehop.state.pairing && window.__edgehop.state.pairing.stage === 'waiting_code', 'Left is waiting for the code', PAIR_TIMEOUT);
  await L.waitForSelector('#pair-dialog[data-stage="waiting_code"]', { timeout: PAIR_TIMEOUT });
  await R.locator('#code-dialog').evaluate((el) => Promise.all(el.getAnimations().map((a) => a.finished))); // fade-in done
  await t.screenshot(R, 'edgehop-pairing');

  // a wrong code is rejected and the dialog offers a retry
  const wrong = code === '000000' ? '111111' : '000000';
  await L.fill('#pair-code', wrong);
  await L.click('#pair-confirm');
  await waitState(L, () => window.__edgehop.state.pairing && window.__edgehop.state.pairing.stage === 'failed', 'backend reports the wrong code', PAIR_TIMEOUT);
  await L.waitForSelector('#pair-dialog[data-stage="failed"]', { timeout: PAIR_TIMEOUT });
  await t.expect(await L.textContent('#pair-dialog .dialog-text'), /not accepted/);
  await waitState(R, () => !window.__edgehop.state.peers.some((p) => p.paired), 'Right did not trust the wrong code', PAIR_TIMEOUT);
  await L.click('#pair-retry');
  await L.waitForSelector('#pair-dialog[data-stage="address"]');
  await t.expect(await L.inputValue('#pair-address'), `127.0.0.1:${sR.port}`, 'address remembered for the retry');
  await L.click('#pair-go');
  await R.waitForFunction((old) => {
    const d = document.querySelector('#code-dialog[open] #pair-code-display');
    return d && d.textContent.replace(/\D/g, '') !== old;
  }, code, { timeout: PAIR_TIMEOUT });
  const code2 = await R.$eval('#pair-code-display', (el) => el.textContent.replace(/\D/g, ''));
  await waitState(L, () => window.__edgehop.state.pairing && window.__edgehop.state.pairing.stage === 'waiting_code', 'Left is waiting for the second code', PAIR_TIMEOUT);
  await L.waitForSelector('#pair-dialog[data-stage="waiting_code"]', { timeout: PAIR_TIMEOUT });
  await L.fill('#pair-code', code2);
  await L.click('#pair-confirm');
  await waitState(L, () => window.__edgehop.state.pairing && window.__edgehop.state.pairing.stage === 'paired', 'backend reports paired', PAIR_TIMEOUT);
  await L.waitForSelector('#pair-dialog[data-stage="paired"]', { timeout: PAIR_TIMEOUT });
  await L.click('#pair-done');
  await R.waitForSelector('#code-dialog[open]', { state: 'detached', timeout: PAIR_TIMEOUT });

  await waitState(L, () => window.__edgehop.state.peers.some((p) => p.name === 'Right' && p.paired && p.status === 'online'), 'Left shows Right online', PAIR_TIMEOUT);
  await waitState(R, () => window.__edgehop.state.peers.some((p) => p.name === 'Left' && p.paired && p.status === 'online'), 'Right shows Left online', PAIR_TIMEOUT);
  const leftId = sL.self.id;
  const rightId = sR.self.id;
  await t.expect(await L.$eval(`.eh-peer[data-id="${rightId}"] .badge`, (e) => e.textContent), /Online/);
  await t.expect(await L.$$eval(`.eh-peer[data-id="${rightId}"] .eh-display`, (els) => els.length), 2, 'peer card draws the peer’s two displays');
  await clearToasts(L);
  await t.screenshot(L, 'edgehop-machines');

  // ---------------------------------------------------------------- layout
  await L.click('#nav .tab[data-tab="layout"]');
  await t.expect(await L.$$eval('#layout-unplaced .eh-tile', (els) => els.length), 2, 'both machines start unplaced');
  await dragTile(L, leftId, 1, 2);
  await dragTile(L, rightId, 2, 2);
  await t.expect(await L.$eval('.eh-cell[data-col="1"][data-row="2"] .eh-tile', (e) => e.dataset.id), leftId, 'Left tile dropped at (1,2)');
  await t.expect(await L.$eval('.eh-cell[data-col="2"][data-row="2"] .eh-tile', (e) => e.dataset.id), rightId, 'Right tile dropped at (2,2)');
  await t.expect(await L.$eval('#layout-save', (b) => b.disabled), false, 'save enabled after edits');
  await L.click('#layout-save');
  await waitState(L, () => window.__edgehop.state.layout.tiles.length === 2, 'layout saved on Left');
  await waitState(R, () => window.__edgehop.state.layout.tiles.length === 2, 'layout shared with Right');
  const rl = (await state(R)).layout.tiles.find((x) => x.id === rightId);
  await t.expect(`${rl.col},${rl.row}`, '2,2', 'Right received the tiles');
  await t.expect(await L.$eval('#layout-save', (b) => b.disabled), true, 'save disabled once clean');
  // keyboard editing works too: nudge Right one cell right and back
  await L.focus(`.eh-tile[data-id="${rightId}"]`);
  await L.keyboard.press('ArrowRight');
  await t.expect(await L.$eval('.eh-cell[data-col="3"][data-row="2"] .eh-tile', (e) => e.dataset.id), rightId, 'arrow key moved the tile');
  await L.keyboard.press('ArrowLeft');
  await t.expect(await L.$eval('#layout-save', (b) => b.disabled), true, 'back to the saved layout');
  await clearToasts(L);
  await t.screenshot(L, 'edgehop-layout');

  // ---------------------------------------------------------------- the hop
  const emit = (page, event) => rpc(page, 'edgehop.debug.emit', { event });
  await rpc(R, 'edgehop.debug.injected', { clear: true });
  await emit(L, { kind: 'mouse_move', dx: 5000, dy: 0 });
  await waitState(R, () => window.__edgehop.state.self.active && window.__edgehop.state.active === window.__edgehop.state.self.id, 'Right becomes active');
  await waitState(L, () => !window.__edgehop.state.self.active && window.__edgehop.state.role === 'sending', 'Left is sending');
  await t.expect(await L.textContent('#status-active'), /Controlling Right/);
  await t.expect(await R.textContent('#status-active'), /Pointer is here/);
  await t.expect(await L.$eval(`.eh-tile[data-id="${rightId}"]`, (e) => e.classList.contains('is-active')), true, 'active tile highlighted live on the layout page');
  for (const [dx, dy] of [[10, 5], [7, -3], [1, 1]]) await emit(L, { kind: 'mouse_move', dx, dy });
  await emit(L, { kind: 'key', wire: 0x04, down: true });
  await emit(L, { kind: 'key', wire: 0x04, down: false });
  await R.waitForFunction(async () => {
    const { native } = await import('/shared/native.js');
    const r = await native.call('edgehop.debug.injected');
    return r.calls.filter((c) => c.op === 'MoveRelative').length >= 3 && r.calls.filter((c) => c.op === 'Key').length >= 2;
  }, null, { timeout: 20000 });
  const inj = await rpc(R, 'edgehop.debug.injected');
  const ops = inj.calls.map((c) => c.op);
  await t.expect(ops[0], 'LockCursor', `Right unlocks first: ${ops.join(',')}`);
  await t.expect(ops[1], 'MoveTo', 'then warps the cursor to the entry point');
  const moveTo = inj.calls[1].args;
  await t.expect(moveTo[0], 1, 'enters one pixel inside the left edge');
  await t.expect(moveTo[1] > 0 && moveTo[1] < 1620, true, `at the same fraction of Right’s union (${moveTo})`);
  const rel = inj.calls.filter((c) => c.op === 'MoveRelative').map((c) => c.args.join(','));
  await t.expect(rel.slice(0, 3).join(' '), '10,5 7,-3 1,1', 'relative moves arrive in order');
  await t.expect(inj.calls.filter((c) => c.op === 'Key').map((c) => c.args[1]).join(','), '1,0', 'key down/up injected');
  const lockL = await rpc(L, 'edgehop.debug.injected');
  await t.expect(lockL.locked, true, 'Left parked its pointer');
  await t.expect((await state(R)).role, 'receiving');

  // hotkey Ctrl+Alt+Home on Left brings the pointer back
  await emit(L, { kind: 'key', wire: 0xE0, down: true });
  await emit(L, { kind: 'key', wire: 0xE2, down: true });
  await emit(L, { kind: 'key', wire: 0x4A, down: true });
  await waitState(L, () => window.__edgehop.state.self.active, 'Left active again after the hotkey');
  await waitState(R, () => !window.__edgehop.state.self.active, 'Right sees Left active');
  for (const w of [0x4A, 0xE2, 0xE0]) await emit(L, { kind: 'key', wire: w, down: false });
  await t.expect((await rpc(L, 'edgehop.debug.injected')).locked, false, 'Left pointer released');
  // and the UI button sends it there again
  await L.click('#nav .tab[data-tab="machines"]');
  await L.click(`.eh-peer[data-id="${rightId}"] [data-action="switch"]`);
  await waitState(R, () => window.__edgehop.state.self.active, 'switch button hands control to Right');
  await R.click('#status-take-back').catch(() => {});
  await L.click('#status-take-back').catch(() => {});
  await waitState(L, () => window.__edgehop.state.self.active, 'bring-back button on Left');

  // ---------------------------------------------------------------- settings
  await L.click('#nav .tab[data-tab="settings"]');
  await t.expect(await L.inputValue('#set-name'), 'Left');
  await L.fill('#set-deadzone', '48');
  await L.$eval('#set-deadzone', (e) => e.dispatchEvent(new Event('change')));
  await waitState(L, () => window.__edgehop.state.settings.edgeCornerDeadZonePx === 48, 'dead zone saved');
  await L.click('#set-clipboard + .toggle-track');
  await waitState(L, () => window.__edgehop.state.settings.clipboardSync === false, 'clipboard toggle saved');
  await L.click('#set-clipboard + .toggle-track');
  await waitState(L, () => window.__edgehop.state.settings.clipboardSync === true, 'clipboard toggle restored');
  await L.selectOption('#set-hotkey', 'ctrl+shift');
  await waitState(L, () => window.__edgehop.state.settings.hotkeySwitch === 'ctrl+shift', 'hotkey saved');
  await L.fill('#set-name', 'Left desk');
  await L.click('#set-name-save');
  await waitState(R, () => window.__edgehop.state.peers.some((p) => p.name === 'Left desk'), 'rename reaches Right');
  await L.fill('#set-name', 'Left');
  await L.click('#set-name-save');
  await waitState(R, () => window.__edgehop.state.peers.some((p) => p.name === 'Left'), 'renamed back');
  await t.expect(await L.$eval('#set-login', (e) => e.disabled), true, 'launch at login is disabled on the Linux test build');
  await L.click('#log-panel summary');
  await t.expect(await L.$$eval('#log-list .eh-log-line', (els) => els.length) > 3, true, 'log panel lists entries');
  await t.expect(await L.textContent('#log-list'), /control → Right/, 'hand-over logged');
  await clearToasts(L);
  await t.screenshot(L, 'edgehop-settings');

  // ---------------------------------------------------------------- unpair
  await L.click('#nav .tab[data-tab="machines"]');
  await L.click(`.eh-peer[data-id="${rightId}"] [data-action="unpair"]`);
  await L.waitForSelector('dialog.dialog[open]');
  await L.click('dialog.dialog[open] .btn-danger');
  await waitState(L, () => window.__edgehop.state.peers.length === 0, 'Left forgot Right');
  await waitState(R, () => window.__edgehop.state.peers.some((p) => p.name === 'Left' && p.status === 'offline'), 'Right shows Left offline');
  await t.expect((await state(L)).layout.tiles.length, 1, 'unpair drops the tile');

  // ---------------------------------------------------------------- phone width
  const phone = await t.browser.newContext({ viewport: { width: 360, height: 740 }, deviceScaleFactor: 2, hasTouch: true, isMobile: true });
  const P = await phone.newPage();
  watch(P, 'phone');
  await P.goto(left.url, { waitUntil: 'load' });
  await ready(P);
  await P.click('#nav .tab[data-tab="layout"]');
  await P.waitForTimeout(100);
  const overflow = await P.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }));
  await t.expect(overflow.scrollWidth <= overflow.clientWidth, true, `no horizontal overflow at 360px: ${JSON.stringify(overflow)}`);
  await phone.close();

  await t.expect(errors.length, 0, `page errors:\n   ${errors.join('\n   ')}`);
}
