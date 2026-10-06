// Vitals end to end on the Linux build of apps/vitals/win (--fake-power): live
// samples reach the Overview, the Processes table sorts and filters, the Power
// page round-trips settings through power.apply / power.get and keep-awake, and
// the three pages plus a dark Overview are screenshotted.
import { mkdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

export default async function (t) {
  mkdirSync(resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'test-results'), { recursive: true });
  const { page, context, url } = await t.launchApp('vitals', ['--fake-power']);
  const errors = [];
  page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`console: ${m.text()}`); });

  // ------------------------------------------------------------ overview
  await page.waitForFunction(() => window.__vitals?.ready && window.__vitals.samples >= 3, null, { timeout: 30000 });
  const info = await page.evaluate(() => window.__vitals.info);
  await t.expect(info.name, 'Vitals');
  await t.expect(info.platform, 'linux');
  await t.expect(info.machine.cores > 0, true, `cores reported (${info.machine.cores})`);
  await t.expect(await page.textContent('#conn'), 'Live');

  const cpuText = (await page.textContent('#tile-cpu .stat-value')).trim();
  await t.expect(cpuText, /^\d{1,3}%$/, `CPU tile shows a percentage (${cpuText})`);
  await t.expect(await page.textContent('#tile-mem .stat-value'), /\d+(\.\d+)? [KMG]B/, 'memory tile shows bytes');

  const coreSeries = await page.evaluate(() => window.__vitals.charts.cores.series.length);
  await t.expect(coreSeries, info.machine.cores, 'per-core chart has one series per core');
  const corePushes = await page.evaluate(() => window.__vitals.charts.cores.pushes);
  await t.expect(corePushes >= 3, true, `per-core chart received pushes (${corePushes})`);
  const legendKeys = await page.$$eval('#card-cores .chart-legend .chart-key', (els) => els.length);
  await t.expect(legendKeys, info.machine.cores, 'legend lists every core');
  if (info.machine.cores > 8) {
    await t.expect(await page.$eval('#card-cores .vt-core-legend', (el) => el.dataset.collapsed), 'true', 'legend collapsed for >8 cores');
  }

  const memSegs = await page.$$eval('#mem-bar .barmeter-seg:not([hidden])', (els) => els.map((e) => e.style.flexBasis));
  await t.expect(memSegs.length >= 1, true, `memory bar renders segments (${memSegs.join(', ')})`);
  await t.expect(memSegs.some((v) => parseFloat(v) > 0), true, 'memory bar has a non-zero segment');
  await t.expect(await page.textContent('#mem-pressure'), /Normal|Elevated|Critical/);

  const sparkPainted = await page.evaluate(() => {
    const c = window.__vitals.charts.sparks.cpu.canvas;
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let n = 0;
    for (let i = 3; i < d.length; i += 4) if (d[i]) n++;
    return n;
  });
  await t.expect(sparkPainted > 50, true, `cpu sparkline painted (${sparkPainted})`);
  await page.waitForFunction(() => window.__vitals.samples >= 6, null, { timeout: 15000 });
  await page.evaluate(() => window.scrollTo(0, 0));
  await t.screenshot(page, 'vitals-overview');

  // ----------------------------------------------------------- processes
  await page.click('#nav .tab[data-tab="processes"]');
  await page.waitForFunction(() => document.querySelectorAll('#proc-table tbody tr').length > 5, null, { timeout: 15000 });
  const rowCount = await page.$$eval('#proc-table tbody tr', (els) => els.length);
  await t.expect(rowCount > 5, true, `process rows (${rowCount})`);
  await t.expect(await page.$eval('#proc-table th[data-key="cpu"]', (el) => el.getAttribute('aria-sort')), 'descending', 'cpu sorted by default');

  await page.click('#proc-table th[data-key="memBytes"]');
  await t.expect(await page.$eval('#proc-table th[data-key="memBytes"]', (el) => el.getAttribute('aria-sort')), 'descending', 'memory column sorted');
  const mems = await page.$$eval('#proc-table tbody tr', (els) => els.map((e) => Number(e.dataset.mem)));
  const sortedDesc = mems.every((v, i) => i === 0 || mems[i - 1] >= v);
  await t.expect(sortedDesc, true, `rows ordered by memory descending (${mems.slice(0, 5).join(', ')}…)`);
  await t.expect(mems[0] > 0, true, 'top memory row is non-zero');
  await page.click('#proc-table th[data-key="memBytes"]');
  const memsAsc = await page.$$eval('#proc-table tbody tr', (els) => els.map((e) => Number(e.dataset.mem)));
  await t.expect(memsAsc.every((v, i) => i === 0 || memsAsc[i - 1] <= v), true, 'second click sorts ascending');
  await page.click('#proc-table th[data-key="memBytes"]');

  const firstName = await page.$eval('#proc-table tbody tr td', (el) => el.textContent);
  const needle = firstName.slice(0, Math.min(4, firstName.length)).toLowerCase();
  await page.fill('.vt-proc-tools input[type=search]', needle);
  await page.waitForFunction((n) => {
    const rows = [...document.querySelectorAll('#proc-table tbody tr')];
    return rows.length > 0 && rows.every((r) => r.firstChild.textContent.toLowerCase().includes(n));
  }, needle, { timeout: 5000 });
  const filtered = await page.$$eval('#proc-table tbody tr', (els) => els.length);
  await t.expect(filtered < rowCount, true, `search filters rows (${filtered} of ${rowCount} for "${needle}")`);
  await t.expect(await page.textContent('.vt-proc-card .badge'), new RegExp(`^${filtered} of \\d+$`), 'count badge shows filtered/total');
  await page.fill('.vt-proc-tools input[type=search]', '');
  await page.waitForFunction((n) => document.querySelectorAll('#proc-table tbody tr').length > n, filtered, { timeout: 5000 });

  // selecting a row enables End task; the dialog can be cancelled
  await page.click('#proc-table tbody tr:nth-child(3)');
  await t.expect(await page.$eval('.vt-proc-tools .btn-danger', (b) => b.disabled), false, 'End task enabled after selection');
  await page.click('.vt-proc-tools .btn-danger');
  await page.waitForSelector('dialog.dialog[open]');
  await t.expect(await page.textContent('dialog.dialog[open] .dialog-title'), /^End "/);
  await page.keyboard.press('Escape');
  await page.waitForSelector('dialog.dialog[open]', { state: 'detached' });
  await page.evaluate(() => window.scrollTo(0, 0));
  await t.screenshot(page, 'vitals-processes');

  // ---------------------------------------------------------------- power
  await page.click('#nav .tab[data-tab="power"]');
  await page.waitForFunction(() => window.__vitals.pages.power.model?.fake === true, null, { timeout: 15000 });
  const model = await page.evaluate(() => window.__vitals.pages.power.model);
  await t.expect(model.onBattery.lidClose, 'sleep', 'fake model loaded');
  await t.expect(await page.$$eval('[data-source]', (els) => els.length), 2, 'battery and plugged-in cards');
  const lidOptions = await page.$$eval('select[data-key="onBattery.lidClose"] option', (els) => els.map((o) => o.value));
  await t.expect(lidOptions.join(','), model.supported.lidClose.join(','), 'lid options follow supported.lidClose');
  await t.expect(await page.$eval('#power-apply', (b) => b.disabled), true, 'Apply disabled with no changes');
  await t.expect(await page.textContent('#power-status'), /Simulated/, 'simulated badge');

  await page.selectOption('select[data-key="onBattery.lidClose"]', 'nothing');
  await t.expect(await page.$eval('#power-apply', (b) => b.disabled), false, 'Apply enabled after a change');
  await t.expect(await page.textContent('.vt-applybar .vt-dirty'), '1 unsaved change');
  await page.click('#power-apply');
  await page.waitForSelector('.toast-region .toast.toast-success', { timeout: 10000 });
  await t.expect(await page.textContent('.toast-region .toast.toast-success .toast-text'), /lid close \(battery\)/);
  await page.waitForFunction(() => window.__vitals.pages.power.model?.onBattery.lidClose === 'nothing', null, { timeout: 5000 });
  const after = await page.evaluate(() => window.__vitals.call('power.get'));
  await t.expect(after.onBattery.lidClose, 'nothing', 'power.get reflects the applied change');
  await t.expect(after.onPower.lidClose, 'nothing', 'untouched keys unchanged');
  await t.expect(await page.$eval('#power-apply', (b) => b.disabled), true, 'Apply disabled again after reload');

  // keep-awake round trip
  await page.click('#card-awake label.toggle');
  await page.waitForFunction(() => document.querySelector('#card-awake .badge').textContent.startsWith('Awake'), null, { timeout: 5000 });
  let awake = await page.evaluate(() => window.__vitals.call('power.keepAwake.get'));
  await t.expect(awake.enabled, true, 'keep-awake enabled via the toggle');
  await t.expect(awake.mode, 'system');
  await page.click('#keep-awake-mode button[data-value="display"]');
  await page.waitForFunction(() => document.querySelector('#card-awake .badge').textContent.includes('display'), null, { timeout: 5000 });
  awake = await page.evaluate(() => window.__vitals.call('power.keepAwake.get'));
  await t.expect(awake.mode, 'display', 'mode switched to display');
  await page.click('#card-awake label.toggle');
  await page.waitForFunction(() => document.querySelector('#card-awake .badge').textContent === 'Off', null, { timeout: 5000 });
  awake = await page.evaluate(() => window.__vitals.call('power.keepAwake.get'));
  await t.expect(awake.enabled, false, 'keep-awake disabled again');

  // actions: shutdown asks for confirmation and can be cancelled
  await page.click('#card-actions button[data-action="shutdown"]');
  await page.waitForSelector('dialog.dialog[open]');
  await page.click('dialog.dialog[open] .btn-secondary');
  await page.waitForSelector('dialog.dialog[open]', { state: 'detached' });

  // schedule: add one item through the dialog
  await page.click('#schedule-add');
  await page.waitForSelector('#schedule-dialog[open]');
  await page.selectOption('#sched-type', 'shutdown');
  await page.fill('#sched-time', '23:30');
  await page.click('#schedule-dialog .btn-primary');
  await page.waitForSelector('#schedule-list .vt-schedule-row', { timeout: 5000 });
  const sched = await page.evaluate(() => window.__vitals.call('power.schedule.get'));
  await t.expect(sched.items.length, 1, 'schedule item stored');
  await t.expect(sched.items[0].time, '23:30');
  await t.expect(sched.items[0].days.join(','), 'mon,tue,wed,thu,fri', 'weekday default');
  await t.expect(await page.textContent('#schedule-list .vt-sched-days'), 'Weekdays');
  await page.evaluate(() => { window.scrollTo(0, 0); document.querySelectorAll('.toast-region .toast').forEach((el) => el.remove()); });
  await t.screenshot(page, 'vitals-power');

  // ------------------------------------------------------- overview, dark
  await page.click('#nav .tab[data-tab="overview"]');
  await page.click('#theme-switch button[data-value="dark"]');
  await t.expect(await page.evaluate(() => document.documentElement.dataset.theme), 'dark');
  await page.waitForTimeout(400);
  await page.evaluate(() => { window.scrollTo(0, 0); document.querySelectorAll('.toast-region .toast').forEach((el) => el.remove()); });
  await t.screenshot(page, 'vitals-overview-dark');
  await page.click('#theme-switch button[data-value="auto"]');

  // ------------------------------------------------------- 800 px layout
  const narrow = await t.browser.newContext({ viewport: { width: 800, height: 700 } });
  const p2 = await narrow.newPage();
  p2.on('pageerror', (e) => errors.push(`pageerror(800): ${e.message}`));
  await p2.goto(url, { waitUntil: 'load' });
  await p2.waitForFunction(() => window.__vitals?.ready && window.__vitals.samples >= 2, null, { timeout: 30000 });
  for (const tab of ['processes', 'power', 'overview']) {
    await p2.click(`#nav .tab[data-tab="${tab}"]`);
    await p2.waitForTimeout(tab === 'power' ? 600 : 300);
    const overflow = await p2.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
    await t.expect(overflow.scroll <= overflow.client, true, `no horizontal overflow at 800px on ${tab} (${JSON.stringify(overflow)})`);
  }
  await narrow.close();
  await context.close();

  await t.expect(errors.length, 0, `page errors:\n   ${errors.join('\n   ')}`);
}
