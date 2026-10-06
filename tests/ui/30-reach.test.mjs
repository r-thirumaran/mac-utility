// Reach end to end on the Linux fake agent: the agent's own settings page (name,
// port, QR), then a second browser context acting as the phone — add the
// laptop, pair with the code the laptop shows, drive the trackpad, keyboard,
// media bar, apps, screen preview and power page, and check every injection in
// the fake backend's action log (reach.debug.actions). Screenshots land in
// test-results/reach-*.png.
import { mkdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

export default async function (t) {
  mkdirSync(join(root, 'test-results'), { recursive: true });
  const cfgDir = mkdtempSync(join(tmpdir(), 'reach-ui-'));
  const errors = [];
  const watch = (page, tag) => {
    page.on('pageerror', (e) => errors.push(`${tag} pageerror: ${e.message}`));
    // The deliberate wrong pairing code below answers 403, which Chromium logs as a resource error.
    page.on('console', (m) => { if (m.type() === 'error' && !/403 \(Forbidden\)/.test(m.text())) errors.push(`${tag} console: ${m.text()}`); });
  };

  // ------------------------------------------------------------ the laptop (local settings view)
  const { page: local, url } = await t.launchApp('reach', ['--fake', '--listen', '127.0.0.1:0', '--config-dir', cfgDir, '--exit-with-parent', '--name', 'Test Laptop']);
  watch(local, 'local');
  const origin = new URL(url).origin;
  const port = Number(new URL(url).port);
  await local.waitForFunction(() => window.__reach && window.__reach.mode === 'local' && window.__reach.status === 'online', null, { timeout: 15000 });
  await local.waitForSelector('#agent-settings');
  await t.expect(await local.evaluate(() => window.__reach.page), 'settings', 'local view opens on Settings');
  await t.expect(await local.$eval('#agent-name', (el) => el.value), 'Test Laptop', 'agent name');
  await t.expect(await local.$eval('#agent-port', (el) => el.value), '7788', 'configured port');
  await t.expect(await local.$eval('#agent-settings .setting:nth-child(2) .setting-desc', (el) => el.textContent), new RegExp(`Listening on ${port} this run`), 'bound port shown');
  await t.expect(await local.$$eval('.qr-box svg', (els) => els.length), 1, 'QR code rendered');
  const urls = await local.$$eval('#agent-urls code', (els) => els.map((e) => e.textContent));
  await t.expect(urls.length > 0, true, 'LAN URLs listed');
  await t.expect(urls[0], new RegExp(`^http://\\d+\\.\\d+\\.\\d+\\.\\d+:${port}/$`), `LAN URL shape (${urls[0]})`);
  await t.expect(await local.$eval('#conn-badge', (el) => el.className), /is-online/, 'local badge online');

  // ------------------------------------------------------------ the phone (no token, same origin)
  const phone = await t.browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, colorScheme: 'light' });
  const p = await phone.newPage();
  watch(p, 'phone');
  await p.goto(`${origin}/#laptops`, { waitUntil: 'load' });
  await p.waitForFunction(() => window.__reach && window.__reach.mode === 'phone', null, { timeout: 15000 });
  await t.expect(await p.evaluate(() => window.__reach.status), 'idle', 'phone starts unconnected');
  await p.fill('#laptop-address', `127.0.0.1:${port}`);
  await p.click('#laptop-add');
  await p.waitForSelector('[data-laptop] .badge-success', { timeout: 10000 });
  await t.expect(await p.$$eval('[data-laptop]', (els) => els.length), 1, 'adding the serving laptop again does not duplicate it');
  await t.expect(await p.$eval('[data-laptop] .laptop-name', (el) => el.textContent), 'Test Laptop', 'laptop name from /api/info');
  await t.screenshot(p, 'reach-laptops');

  // pair: the phone asks, the laptop shows the code, the phone types it
  await p.click('[data-laptop] button[data-action="pair"]');
  await p.waitForSelector('dialog.pair-dialog[open]');
  await local.waitForFunction(() => window.__reach.lastPairCode, null, { timeout: 10000 });
  const code = await local.evaluate(() => window.__reach.lastPairCode);
  await t.expect(code, /^\d{6}$/, 'laptop received a 6-digit pairing code');
  await local.waitForSelector('#pair-code');
  await t.expect(await local.$eval('#pair-code', (el) => el.textContent), code, 'settings page shows the code');
  await p.fill('dialog.pair-dialog input', '000000' === code ? '000001' : '000000');
  await p.click('dialog.pair-dialog .btn-primary');
  await p.waitForSelector('.toast-region .toast-danger');
  await p.waitForSelector('dialog.pair-dialog[open]');
  await p.fill('dialog.pair-dialog input', code);
  await p.click('dialog.pair-dialog .btn-primary');
  await p.waitForFunction(() => window.__reach.status === 'online', null, { timeout: 10000 });
  await t.expect(await p.evaluate(() => window.__reach.page), 'remote', 'pairing lands on Remote');
  const laptop = await p.evaluate(() => window.__reach.laptops[0]);
  await t.expect(laptop.token.length, 43, 'token stored on the phone');
  await t.expect(await p.$eval('#conn-badge', (el) => el.textContent), 'Test Laptop', 'badge shows the laptop');
  await local.waitForSelector('#pairings [data-revoke]', { timeout: 10000 });
  await t.expect(await local.$eval('#pairings .setting-label', (el) => el.textContent), /phone|browser|Phone/i, 'laptop lists the paired device');

  const actions = async (clear = false) => (await p.evaluate((c) => window.__reach.call('reach.debug.actions', { clear: c }), clear)).actions;
  const settle = () => p.waitForTimeout(150);

  // ------------------------------------------------------------ Remote: trackpad gestures
  await p.waitForSelector('#trackpad');
  await t.screenshot(p, 'reach-remote');
  await actions(true);
  const box = await p.$eval('#trackpad', (el) => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; });
  const cx = box.x + box.w / 2, cy = box.y + box.h / 2;
  await p.mouse.move(cx - 60, cy);
  await p.mouse.down();
  for (let i = 1; i <= 8; i++) { await p.mouse.move(cx - 60 + i * 15, cy + i * 4); await p.waitForTimeout(16); }
  await p.waitForTimeout(400);
  await p.mouse.up();
  await settle();
  let acts = await actions(true);
  const moves = acts.filter((a) => a.kind === 'mouse.move');
  await t.expect(moves.length > 0, true, `one-finger move → mouse.move (${JSON.stringify(acts).slice(0, 200)})`);
  await t.expect(moves.reduce((s, a) => s + a.dx, 0) > 60, true, 'moves go right with acceleration');
  await t.expect(acts.some((a) => a.kind === 'mouse.click'), false, 'a stroke is not a click');

  await p.mouse.click(cx, cy);
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'mouse.click' && a.button === 'left' && a.count === 1), true, `tap → left click (${JSON.stringify(acts)})`);

  const touch = (steps) => p.evaluate((steps) => {
    const pad = document.getElementById('trackpad');
    const r = pad.getBoundingClientRect();
    const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
    for (const [type, id, dx, dy] of steps) {
      pad.dispatchEvent(new PointerEvent(type, { pointerId: id, pointerType: 'touch', clientX: cx + dx, clientY: cy + dy, bubbles: true, cancelable: true, isPrimary: id === 1, button: 0, buttons: 1 }));
    }
  }, steps);
  // two-finger drag upwards → scroll
  await touch([['pointerdown', 1, -20, 0], ['pointerdown', 2, 20, 0], ['pointermove', 1, -20, -15], ['pointermove', 2, 20, -15], ['pointermove', 1, -20, -30], ['pointermove', 2, 20, -30], ['pointermove', 1, -20, -45], ['pointermove', 2, 20, -45], ['pointerup', 1, -20, -45], ['pointerup', 2, 20, -45]]);
  await settle();
  acts = await actions(true);
  const scrolls = acts.filter((a) => a.kind === 'mouse.scroll');
  await t.expect(scrolls.length > 0 && scrolls.reduce((s, a) => s + a.dy, 0) > 20, true, `two-finger drag → mouse.scroll (${JSON.stringify(acts)})`);
  await t.expect(acts.some((a) => a.kind === 'mouse.click'), false, 'two-finger drag is not a click');
  // two-finger tap → right click, three-finger tap → middle click
  await touch([['pointerdown', 1, -20, 0], ['pointerdown', 2, 20, 0], ['pointerup', 1, -20, 0], ['pointerup', 2, 20, 0]]);
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'mouse.click' && a.button === 'right'), true, `two-finger tap → right click (${JSON.stringify(acts)})`);
  await touch([['pointerdown', 1, -30, 0], ['pointerdown', 2, 0, 0], ['pointerdown', 3, 30, 0], ['pointerup', 1, -30, 0], ['pointerup', 2, 0, 0], ['pointerup', 3, 30, 0]]);
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'mouse.click' && a.button === 'middle'), true, 'three-finger tap → middle click');
  // tap, then tap-and-hold-move → drag (mouse.down … mouse.up)
  await touch([['pointerdown', 1, 0, 0], ['pointerup', 1, 0, 0]]);
  await touch([['pointerdown', 1, 0, 0], ['pointermove', 1, 10, 0], ['pointermove', 1, 25, 5], ['pointermove', 1, 40, 10]]);
  await p.waitForTimeout(400);
  await touch([['pointerup', 1, 40, 10]]);
  await settle();
  acts = await actions(true);
  const kinds = acts.map((a) => a.kind);
  await t.expect(kinds.indexOf('mouse.down') >= 0 && kinds.indexOf('mouse.up') > kinds.indexOf('mouse.down'), true, `double-tap-hold → drag (${kinds.join(',')})`);

  // ------------------------------------------------------------ Remote: keyboard sheet and media bar
  await p.click('#open-keyboard');
  await p.waitForSelector('#keyboard-sheet[open]');
  await p.keyboard.type('hello');
  await p.keyboard.press('Enter');
  await p.keyboard.press('Backspace');
  await p.click('#keyboard-sheet button[data-mod="cmdOrCtrl"]');
  await t.expect(await p.$eval('#keyboard-sheet button[data-mod="cmdOrCtrl"]', (el) => el.getAttribute('aria-pressed')), 'true', 'modifier latched');
  await p.click('#keyboard-sheet button[data-key="ArrowLeft"]');
  await settle();
  acts = await actions(true);
  const typed = acts.filter((a) => a.kind === 'key.text').map((a) => a.text).join('');
  await t.expect(typed, 'hello', `keyboard sheet types text (${JSON.stringify(acts)})`);
  await t.expect(acts.some((a) => a.kind === 'key.press' && a.key === 'Enter'), true, 'Enter → key.press Enter');
  await t.expect(acts.some((a) => a.kind === 'key.press' && a.key === 'Backspace'), true, 'Backspace → key.press Backspace');
  const arrow = acts.find((a) => a.kind === 'key.press' && a.key === 'ArrowLeft');
  await t.expect(JSON.stringify(arrow?.mods), '["cmdOrCtrl"]', 'latched modifier applied to the next key');
  await t.expect(await p.$eval('#keyboard-sheet button[data-mod="cmdOrCtrl"]', (el) => el.getAttribute('aria-pressed')), 'false', 'modifier released after use');
  await t.screenshot(p, 'reach-keyboard');
  await p.click('#keyboard-sheet .sheet-actions .btn');
  await p.waitForSelector('#keyboard-sheet[open]', { state: 'detached' }).catch(() => {});
  await p.click('[data-media="playPause"]');
  await p.click('[data-media="volumeUp"]');
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'media' && a.action === 'playPause'), true, 'media bar → playPause');
  await t.expect(acts.some((a) => a.kind === 'media' && a.action === 'volumeUp'), true, 'media bar → volumeUp');
  await p.waitForSelector('#volume:not([disabled])');
  await t.expect(await p.$eval('.volume-row', (el) => el.hidden), false, 'volume slider shown when volume.get works');

  // ------------------------------------------------------------ Apps
  await p.click('#tabbar button[data-tab="apps"]');
  await p.waitForSelector('#app-grid .app-tile', { timeout: 10000 });
  const tiles = await p.$$eval('#app-grid .app-tile', (els) => els.map((e) => ({ id: e.dataset.app, name: e.querySelector('.app-name').textContent, icon: !!e.querySelector('img') })));
  await t.expect(tiles.length > 0, true, 'apps listed');
  await t.expect(tiles.every((x) => x.icon), true, 'every app has an icon');
  await t.expect(await p.$$eval('#running-row .running-chip', (els) => els.length), 3, 'running apps listed');
  await t.expect(await p.$$eval('#running-row .running-chip.is-active', (els) => els.length), 1, 'active app marked');
  await actions(true);
  await p.click('#app-grid .app-tile >> nth=0');
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'apps.launch' && a.id === tiles[0].id), true, `tap launches (${JSON.stringify(acts)})`);
  await p.click('#running-row .running-chip >> nth=1 >> button >> nth=0');
  await p.waitForFunction(() => document.querySelector('#running-row .running-chip:nth-child(2)')?.classList.contains('is-active'), null, { timeout: 5000 });
  await t.screenshot(p, 'reach-apps');

  // ------------------------------------------------------------ Screen
  await p.click('#tabbar button[data-tab="screen"]');
  await p.waitForFunction(() => window.__reach.frames >= 2, null, { timeout: 15000 });
  const painted = await p.$eval('#screen-canvas', (c) => {
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let colored = 0;
    for (let i = 0; i < d.length; i += 4) if (d[i] + d[i + 1] + d[i + 2] > 60) colored++;
    return { w: c.width, h: c.height, colored, total: d.length / 4 };
  });
  await t.expect(painted.w > 0 && painted.w <= 1280 && Math.abs(painted.w / painted.h - 16 / 9) < 0.02, true, `frame is 16:9 within the width limit (${painted.w}×${painted.h})`);
  await t.expect(painted.colored > painted.total * 0.8, true, `canvas painted (${painted.colored}/${painted.total})`);
  await t.expect(await p.$eval('#screen-overlay', (el) => el.hidden), true, 'overlay hidden once frames arrive');
  await actions(true);
  const cbox = await p.$eval('#screen-canvas', (el) => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; });
  await p.mouse.click(cbox.x + cbox.w * 0.5, cbox.y + cbox.h * 0.5);
  await settle();
  acts = await actions(true);
  const tap = acts.find((a) => a.kind === 'mouse.click');
  await t.expect(!!tap && Math.abs(tap.x - 0.5) < 0.03 && Math.abs(tap.y - 0.5) < 0.03 && tap.button === 'left', true, `screen tap → click at the normalised point (${JSON.stringify(acts)})`);
  await t.expect(acts.some((a) => a.kind === 'mouse.moveTo' && a.display === 0), true, 'click carries the display');
  const framesBefore = await p.evaluate(() => window.__reach.frames);
  await p.waitForFunction((n) => window.__reach.frames > n, framesBefore, { timeout: 5000 });
  await t.screenshot(p, 'reach-screen');

  // ------------------------------------------------------------ Power
  await p.click('#tabbar button[data-tab="power"]');
  await p.waitForSelector('[data-power="lock"]');
  await p.waitForTimeout(300);
  await t.expect(await p.evaluate(() => window.__reach.frames), await p.evaluate(() => window.__reach.frames), 'stream stops when the Screen tab is hidden');
  await actions(true);
  await p.click('[data-power="shutdown"]');
  await p.waitForSelector('dialog.dialog[open]');
  await t.expect(await p.evaluate(() => document.activeElement?.textContent), 'Cancel', 'destructive action focuses Cancel');
  await p.click('dialog.dialog[open] .btn-secondary');
  await p.waitForSelector('dialog.dialog[open]', { state: 'detached' });
  await p.click('[data-power="lock"]');
  await p.waitForSelector('dialog.dialog[open]');
  await t.screenshot(p, 'reach-power');
  await p.click('dialog.dialog[open] .btn-primary');
  await p.waitForSelector('dialog.dialog[open]', { state: 'detached' });
  await settle();
  acts = await actions(true);
  await t.expect(acts.some((a) => a.kind === 'power' && a.action === 'lock'), true, `Power → lock recorded (${JSON.stringify(acts)})`);
  await t.expect(acts.some((a) => a.kind === 'power' && a.action === 'shutdown'), false, 'cancelled shutdown not sent');

  // ------------------------------------------------------------ Settings (phone) + desktop settings screenshot
  await p.click('#tabbar button[data-tab="settings"]');
  await p.waitForSelector('#device-name');
  await t.screenshot(p, 'reach-settings');
  await local.setViewportSize({ width: 1000, height: 700 });
  await local.waitForTimeout(200);
  await t.screenshot(local, 'reach-settings-desktop');

  // ------------------------------------------------------------ dark mode + 360 px overflow on every tab
  const narrow = await t.browser.newContext({ viewport: { width: 360, height: 740 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, colorScheme: 'dark' });
  const n = await narrow.newPage();
  watch(n, 'narrow');
  await n.goto(`${origin}/#laptops`, { waitUntil: 'load' });
  await n.waitForFunction(() => window.__reach && window.__reach.mode === 'phone');
  await t.expect(await n.evaluate(() => getComputedStyle(document.body).backgroundColor), 'rgb(18, 18, 20)', 'dark mode follows the OS');
  for (const tab of ['laptops', 'remote', 'apps', 'screen', 'power', 'settings']) {
    await n.click(`#tabbar button[data-tab="${tab}"]`);
    await n.waitForTimeout(250);
    const overflow = await n.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth, body: document.body.scrollWidth }));
    await t.expect(overflow.scroll <= overflow.client && overflow.body <= overflow.client, true, `no horizontal scroll on ${tab} at 360px (${JSON.stringify(overflow)})`);
  }
  await t.screenshot(n, 'reach-dark');
  await narrow.close();

  // ------------------------------------------------------------ revoke from the laptop drops the phone
  await local.click('#pairings [data-revoke]');
  await local.waitForSelector('dialog.dialog[open]');
  await local.click('dialog.dialog[open] .btn-danger');
  await p.waitForFunction(() => window.__reach.status !== 'online', null, { timeout: 10000 });
  await local.waitForFunction(() => !document.querySelector('#pairings [data-revoke]'), null, { timeout: 5000 });

  await phone.close();
  await t.expect(errors.length, 0, `page errors:\n   ${errors.join('\n   ')}`);
}
