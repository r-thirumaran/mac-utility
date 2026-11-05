// Edgehop layout canvas, backend independent: one instance, the editor is fed
// fixture states through the same module instance the app uses. Covers
// to-scale drawing, drag + snapping, boundary highlight, the exact Save
// payload, overlap rejection, keyboard nudges, Place and the legacy tile layout.
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const A = 'aaaaaaaa-0000-0000-0000-000000000001';
const B = 'bbbbbbbb-0000-0000-0000-000000000002';
const mac = { id: A, name: 'Mac', plat: 'macos', active: true, status: 'online', displays: [{ id: 'd1', x: 0, y: 0, w: 1440, h: 900, scale: 2, primary: true }] };
const office = { id: B, name: 'Office Mac', plat: 'macos', active: false, status: 'online', paired: true, displays: [{ id: 'd2', x: 0, y: 0, w: 2560, h: 1440, scale: 1, primary: true }] };
const fixture = (layout, peer = office) => ({ self: mac, peers: [peer], layout, settings: {}, port: 1 });

export default async function (t) {
  const errors = [];
  const dir = mkdtempSync(join(tmpdir(), 'edgehop-canvas-'));
  const { page } = await t.launchApp('edgehop', ['--fake-input', '--no-mdns', '--name', 'Mac', '--config-dir', dir, '--port', '0']);
  page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
  await page.waitForFunction(() => window.__edgehop && window.__edgehop.ready, null, { timeout: 30000 });
  await page.setViewportSize({ width: 1000, height: 900 });
  await page.click('#nav .tab[data-tab="layout"]');

  // Capture saves instead of calling the backend; feed the editor a fixture.
  await page.evaluate(async ([fx]) => {
    const { layoutEditor } = await import('/app.js');
    window.__saves = [];
    layoutEditor.onSave = async (l) => { window.__saves.push(l); };
    window.__fx = fx;
    window.__setState = (layout) => layoutEditor.setState({ ...fx, layout });
    window.__ed = layoutEditor;
    window.__setState({ updatedAt: 1, machines: [{ id: fx.self.id, x: 0, y: 0 }, { id: fx.peers[0].id, x: 3000, y: 0 }] });
  }, [fixture()]);

  const pos = () => page.evaluate(() => Object.fromEntries([...window.__ed.pos].map(([id, p]) => [id, `${p.x},${p.y}`])));
  const count = (sel) => page.locator(sel).count();
  const box = async (id) => page.locator(`.eh-node[data-id="${id}"] rect.eh-d`).first().boundingBox();
  const scale = () => page.$eval('#layout-canvas', (s) => +s.dataset.scale);
  async function drag(id, dx, dy, { release = true } = {}) {
    const b = await box(id);
    const x = b.x + b.width / 2, y = b.y + b.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + dx / 2, y + dy / 2, { steps: 4 });
    await page.mouse.move(x + dx, y + dy, { steps: 6 });
    if (release) await page.mouse.up();
  }

  // ------------------------------------------------ drawn to scale, with a gap
  await t.expect(await count('.eh-node'), 2, 'two machines drawn');
  await t.expect(await count('.eh-boundary'), 0, 'gap: no boundary');
  await t.expect(await page.textContent('#layout-boundaries'), /No touching edges/);
  await t.expect(await page.textContent('#layout-help'), /Drag screens so their edges touch\. The pointer moves to the other computer only across the highlighted edges\. Monitors inside one computer keep the arrangement from that computer’s Display settings\./);
  await t.expect(await page.textContent('.eh-node[data-id="' + A + '"]'), /Mac.*online · this Mac/, 'label with this Mac');
  const ba = await box(A), bb = await box(B);
  const s0 = await scale();
  await t.expect(Math.abs((bb.width - 3) / (ba.width - 3) - 2560 / 1440) < 0.02, true, 'widths are to scale');
  await t.expect(Math.abs(ba.width - 1440 * s0) < 4, true, 'canvas scale matches');

  // ------------------------------------------------ drag B to touch A (6 px away, 5 px low)
  const bA = await box(A);
  const bB = await box(B);
  await drag(B, (bA.x + bA.width + 6) - bB.x, 5);
  await t.expect((await pos())[B], '1440,0', 'snapped exactly to A’s right edge and aligned tops');
  await t.expect(await count('.eh-boundary[data-edge="right"]'), 1, 'right-edge boundary highlighted');
  await t.expect(await page.getAttribute('.eh-boundary', 'data-len'), '900', 'boundary is the 900 unit overlap');
  await t.expect(await page.textContent('#layout-boundaries'), /Mac ↔ Office Mac: 1 boundary \(right edge of Main display\)/);
  await t.expect(await page.$eval('#layout-save', (b) => b.disabled), false, 'dirty after drag');
  await page.click('#layout-save');
  await page.waitForFunction(() => window.__saves.length === 1);
  const saved = await page.evaluate(() => window.__saves[0]);
  await t.expect(JSON.stringify(saved), JSON.stringify({ machines: [{ id: A, x: 0, y: 0 }, { id: B, x: 1440, y: 0 }] }), 'exact layout.set payload');
  await t.expect(await page.$eval('#layout-save', (b) => b.disabled), true, 'clean after save');

  // ------------------------------------------------ overlap is flagged while dragging and reverted on drop
  await drag(B, -400 * s0, 0, { release: false });
  await t.expect(await page.$eval(`.eh-node[data-id="${B}"]`, (e) => e.classList.contains('is-invalid')), true, 'red outline while overlapping');
  await page.mouse.up();
  await t.expect((await pos())[B], '1440,0', 'overlapping drop reverts');
  await t.expect(await page.$eval('#layout-hint', (e) => e.dataset.error), 'true', 'explains the rejection');
  await t.expect(await page.$eval('#layout-save', (b) => b.disabled), true, 'still clean');

  // ------------------------------------------------ keyboard
  await page.focus(`.eh-node[data-id="${B}"]`);
  await page.keyboard.press('ArrowLeft'); // would overlap by 10
  await t.expect((await pos())[B], '1440,0', 'overlapping nudge refused');
  await page.keyboard.press('ArrowRight');
  await t.expect((await pos())[B], '1450,0', 'arrow nudges 10 units');
  await t.expect(await count('.eh-boundary'), 0, 'gap again: no boundary');
  await page.keyboard.press('Shift+ArrowRight');
  await t.expect((await pos())[B], '1550,0', 'shift+arrow nudges 100 units');
  await page.keyboard.press('Shift+ArrowDown');
  await t.expect((await pos())[B], '1550,100');
  await page.click('#layout-revert');
  await t.expect((await pos())[B], '1440,0', 'revert restores the saved layout');

  // ------------------------------------------------ unplaced machine + Place
  await page.evaluate(([a]) => window.__setState({ updatedAt: 2, machines: [{ id: a, x: 0, y: 0 }] }), [A]);
  await t.expect(await count('.eh-node'), 1);
  await t.expect(await count('#layout-unplaced [data-action="place"]'), 1, 'Place offered for the unplaced machine');
  await page.click(`#layout-unplaced [data-action="place"][data-id="${B}"]`);
  await t.expect((await pos())[B], '1440,0', 'Place touches the right of the rightmost machine, tops aligned');
  await t.expect(await count('.eh-boundary'), 1);
  await page.click('#layout-revert');
  await page.click('#layout-auto');
  await t.expect((await pos())[B], '1440,0', 'auto-arrange: left to right, touching');

  // ------------------------------------------------ legacy tile layout is converted, never-seen peer still draws
  await page.evaluate(([a, b]) => { window.__ed.dirty = false; window.__setState({ updatedAt: 3, tiles: [{ id: a, col: 1, row: 2 }, { id: b, col: 2, row: 2 }] }); }, [A, B]);
  await t.expect(await count('.eh-boundary'), 1, 'legacy tiles become touching machines');
  await page.evaluate(() => { window.__ed.dirty = false; window.__ed.setState({ ...window.__fx, peers: [{ ...window.__fx.peers[0], displays: [], status: 'offline' }], layout: { updatedAt: 4, machines: [{ id: window.__fx.self.id, x: 0, y: 0 }, { id: window.__fx.peers[0].id, x: 1440, y: 0 }] } }); });
  await t.expect(await count('.eh-node.is-offline'), 1, 'offline machine dimmed');
  await t.expect(await count('.eh-d.is-placeholder'), 1, 'machine without displays gets a placeholder');

  // ------------------------------------------------ settings toggle exists
  await t.expect(await count('#set-shake'), 1, 'shake-to-locate toggle');
  await t.expect(await page.$eval('#set-shake', (e) => e.checked), true, 'on by default');

  await t.screenshot(page, 'edgehop-canvas');
  await t.expect(errors.length, 0, `page errors: ${errors.join('; ')}`);
}
