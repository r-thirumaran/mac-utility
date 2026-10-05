// Exercises the shared UI kit demo (shared/ui/demo.html): every section renders, the
// charts receive live pushes, tabs/dialog/toast behave, the theme toggle works, both
// themes are screenshotted, and a 360×740 phone viewport has no horizontal overflow.
import { mkdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const SECTIONS = ['tokens', 'typography', 'layout', 'buttons', 'forms', 'tabs', 'badges', 'stats', 'charts', 'gauges', 'meters', 'table', 'settings', 'feedback', 'empty'];

export default async function (t) {
  mkdirSync(resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'test-results'), { recursive: true });
  const base = await t.serveRepo();
  const url = `${base}/shared/ui/demo.html`;
  const errors = [];
  const watch = (page) => {
    page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
    page.on('console', (m) => { if (m.type() === 'error') errors.push(`console: ${m.text()}`); });
  };
  const ready = (page) => page.waitForFunction(() => window.__demo && window.__demo.ready, null, { timeout: 15000 });

  // ---------------------------------------------------------- desktop, light
  const desktop = await t.browser.newContext({ viewport: { width: 1100, height: 760 }, colorScheme: 'light' });
  const page = await desktop.newPage();
  watch(page);
  await page.goto(url, { waitUntil: 'load' });
  await ready(page);

  for (const name of SECTIONS) {
    const box = await page.$eval(`[data-section="${name}"]`, (el) => ({ height: el.getBoundingClientRect().height, children: el.children.length }));
    await t.expect(box.height > 40, true, `section "${name}" has height (${box.height})`);
    await t.expect(box.children > 1, true, `section "${name}" has content`);
  }

  // live charts: counters exist, keep increasing, and the canvas actually has paint on it
  const before = await page.evaluate(() => ({ ...window.__demo.counters }));
  await t.expect(before.sparklinePushes > 0, true, 'sparkline received pushes at startup');
  await t.expect(before.linePushes > 0, true, 'line chart received pushes');
  await t.expect(before.gaugeSets > 0, true, 'gauges were set');
  await page.waitForFunction((n) => window.__demo.counters.sparklinePushes > n, before.sparklinePushes, { timeout: 5000 });
  const sparkState = await page.evaluate(() => {
    const sp = window.__demo.charts.sparks.cpu;
    const c = sp.canvas;
    const data = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let painted = 0;
    for (let i = 3; i < data.length; i += 4) if (data[i]) painted++;
    return { painted, values: sp.values().length, pushes: sp.pushes, backing: [c.width, c.height], css: [c.clientWidth, c.clientHeight] };
  });
  await t.expect(sparkState.painted > 100, true, `sparkline canvas painted (${JSON.stringify(sparkState)})`);
  await t.expect(sparkState.values > 60, true, 'sparkline ring buffer holds history');
  await t.expect(sparkState.backing[0] >= sparkState.css[0], true, 'backing store is at least CSS size (DPR aware)');
  const legend = await page.$$eval('[data-section="charts"] .chart-legend .chart-key', (els) => els.map((e) => e.textContent));
  await t.expect(legend.length, 6, `legend keys across the line charts: ${legend.join(' | ')}`);
  await t.expect(legend[0], /^Core 1\d+%$/, 'legend key shows name + current value');
  const barSegments = await page.$$eval('#barmeter-mem .barmeter-seg:not([hidden])', (els) => els.length);
  await t.expect(barSegments, 4, 'bar meter segments');

  // legend key toggles a series
  await page.click('[data-section="charts"] .chart-legend .chart-key:nth-child(2)');
  await t.expect(await page.$eval('[data-section="charts"] .chart-legend .chart-key:nth-child(2)', (e) => e.getAttribute('aria-pressed')), 'false');
  await page.click('[data-section="charts"] .chart-legend .chart-key:nth-child(2)');

  // chart hover tooltip
  const chartBox = await page.$eval('#line-cores', (c) => { const r = c.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; });
  await page.mouse.move(chartBox.x + chartBox.w * 0.7, chartBox.y + chartBox.h / 2);
  await page.waitForSelector('[data-section="charts"] .chart-tooltip.is-visible', { timeout: 3000 });
  const tipRows = await page.$$eval('[data-section="charts"] .chart-tooltip.is-visible .chart-tooltip-row', (els) => els.map((e) => e.textContent));
  await t.expect(tipRows.length, 4, `tooltip lists every series: ${tipRows.join(' | ')}`);
  await page.mouse.move(5, 5);
  await page.waitForSelector('[data-section="charts"] .chart-tooltip.is-visible', { state: 'detached', timeout: 3000 }).catch(() => {});

  // tabs: keyboard arrows move selection and panels
  await page.focus('#demo-tabs .tab[aria-selected="true"]');
  await page.keyboard.press('ArrowRight');
  await t.expect(await page.$eval('#demo-tabs .tab[aria-selected="true"]', (el) => el.dataset.tab), 'processes', 'ArrowRight selects next tab');
  await t.expect(await page.$eval('#panel-processes', (el) => el.hidden), false, 'panel shown');
  await t.expect(await page.$eval('#panel-overview', (el) => el.hidden), true, 'previous panel hidden');
  await t.expect(await page.$eval('#demo-tabs .tabs', (el) => el.getAttribute('role')), 'tablist');
  await page.keyboard.press('Home');
  await t.expect(await page.$eval('#demo-tabs .tab[aria-selected="true"]', (el) => el.dataset.tab), 'overview');

  // segmented control
  await page.click('#view-switch button[data-value="grid"]');
  await t.expect(await page.evaluate(() => window.__demo.viewSwitch.value), 'grid');
  await page.click('#view-switch button[data-value="list"]');

  // confirm dialog: cancel → false, ok → true (the promise settles on the dialog's queued
  // `close` event, so wait for the demo's hook rather than for the [open] attribute alone)
  const confirmResult = async () => {
    await page.waitForSelector('dialog.dialog[open]', { state: 'detached' });
    await page.waitForFunction(() => window.__demo.lastConfirm !== null, null, { timeout: 3000 });
    return page.evaluate(() => window.__demo.lastConfirm);
  };
  await page.click('#btn-confirm');
  await page.waitForSelector('dialog.dialog[open]');
  await page.click('dialog.dialog[open] .btn-secondary');
  await t.expect(await confirmResult(), false, 'cancel resolves false');
  await page.click('#btn-confirm');
  await page.waitForSelector('dialog.dialog[open]');
  await page.keyboard.press('Enter');
  await t.expect(await confirmResult(), true, 'Enter on focused OK resolves true');
  await page.click('#btn-delete');
  await page.waitForSelector('dialog.dialog[open]');
  await t.expect(await page.evaluate(() => document.activeElement?.textContent), 'Cancel', 'danger dialog focuses Cancel');
  await page.keyboard.press('Escape');
  await t.expect(await confirmResult(), false, 'Escape resolves false');

  // toast appears and can be dismissed
  await page.click('#btn-toast-success');
  await page.waitForSelector('.toast-region .toast.toast-success');
  await page.click('.toast-region .toast .toast-close');
  await page.waitForSelector('.toast-region .toast', { state: 'detached' });

  // sheet opens as a modal and closes
  await page.click('#btn-sheet');
  await t.expect(await page.$eval('#demo-sheet', (d) => d.open), true, 'sheet open');
  await page.click('#btn-sheet-close');
  await t.expect(await page.$eval('#demo-sheet', (d) => d.open), false, 'sheet closed');

  // light screenshot
  await page.evaluate(() => { window.scrollTo(0, 0); window.__demo.stop(); });
  await page.waitForTimeout(150);
  await t.screenshot(page, 'ui-kit-light');

  // ----------------------------------------------------------- theme toggle
  await page.click('#theme-switch button[data-value="dark"]');
  await t.expect(await page.evaluate(() => document.documentElement.dataset.theme), 'dark', 'data-theme set');
  await t.expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor), 'rgb(18, 18, 20)', 'dark background applied');
  await t.expect(await page.evaluate(() => localStorage.getItem('ui.theme')), 'dark', 'theme persisted');
  await page.waitForTimeout(250); // charts re-read their tokens on the next frame
  const darkSpark = await page.evaluate(() => {
    const c = window.__demo.charts.sparks.cpu.canvas;
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
    let painted = 0;
    for (let i = 3; i < d.length; i += 4) if (d[i]) painted++;
    return painted;
  });
  await t.expect(darkSpark > 100, true, 'sparkline repainted after theme change');
  await t.expect(await page.evaluate(() => window.__demo.charts.sparks.cpu.theme.surface), '#1c1c1e', 'chart re-read its CSS tokens for dark');
  await t.expect(await page.evaluate(() => window.__demo.charts.cores.series[0].color), '#3987e5', 'series color follows the dark palette');
  await t.screenshot(page, 'ui-kit-dark');
  await page.click('#theme-switch button[data-value="auto"]');
  await t.expect(await page.evaluate(() => document.documentElement.dataset.theme ?? null), null, 'auto clears data-theme');
  await desktop.close();

  // ------------------------------------------------------------ phone width
  const phone = await t.browser.newContext({ viewport: { width: 360, height: 740 }, deviceScaleFactor: 2, hasTouch: true, isMobile: true, colorScheme: 'light' });
  const p2 = await phone.newPage();
  watch(p2);
  await p2.goto(url, { waitUntil: 'load' });
  await ready(p2);
  await p2.waitForTimeout(100);
  const overflow = await p2.evaluate(() => {
    const doc = document.documentElement;
    const clipped = (el) => { for (let p = el.parentElement; p; p = p.parentElement) { const o = getComputedStyle(p).overflowX; if (o === 'auto' || o === 'scroll' || o === 'hidden' || o === 'clip') return true; } return false; };
    const wide = [];
    for (const el of document.querySelectorAll('body *')) {
      const r = el.getBoundingClientRect();
      if (r.width && r.right > doc.clientWidth + 1 && getComputedStyle(el).position !== 'fixed' && !clipped(el)) {
        wide.push(`${el.tagName.toLowerCase()}${el.id ? `#${el.id}` : ''}.${String(el.className).split(' ')[0]}→${Math.round(r.right)}`);
      }
    }
    return { scrollWidth: doc.scrollWidth, clientWidth: doc.clientWidth, bodyScrollWidth: document.body.scrollWidth, wide: wide.slice(0, 8) };
  });
  await t.expect(overflow.scrollWidth <= overflow.clientWidth, true, `no horizontal page overflow at 360px: ${JSON.stringify(overflow)}`);
  await t.expect(overflow.bodyScrollWidth <= overflow.clientWidth, true, `body does not overflow at 360px: ${JSON.stringify(overflow)}`);
  await t.expect(overflow.wide.length, 0, `no element wider than the viewport: ${overflow.wide.join(', ')}`);
  const controlH = await p2.$eval('#btn-toast', (el) => el.getBoundingClientRect().height);
  await t.expect(controlH >= 34, true, `touch-sized controls on coarse pointers (${controlH}px)`);
  await p2.evaluate(() => { window.scrollTo(0, 0); window.__demo.stop(); });
  await t.screenshot(p2, 'ui-kit-phone');
  await phone.close();

  await t.expect(errors.length, 0, `page errors:\n   ${errors.join('\n   ')}`);
}
