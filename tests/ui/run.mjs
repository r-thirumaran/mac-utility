// Minimal test runner: node tests/ui/run.mjs [filter]
import { readdirSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
// Playwright may be installed globally (NODE_PATH, which ES imports ignore) or locally.
const { chromium } = createRequire(import.meta.url)('playwright');

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..', '..');
const filter = process.argv[2] || '';
const files = readdirSync(here).filter(f => f.endsWith('.test.mjs') && f.includes(filter)).sort();

class Harness {
  constructor(browser) { this.browser = browser; this.procs = []; }
  async expect(actual, expected, msg = '') {
    const ok = expected instanceof RegExp ? expected.test(String(actual)) : actual === expected;
    if (!ok) throw new Error(`expect failed ${msg}: got ${JSON.stringify(actual)}, want ${expected}`);
  }
  async screenshot(page, name) {
    await page.screenshot({ path: resolve(root, 'test-results', `${name}.png`), fullPage: true });
  }
  /** Start the Linux build of apps/<name>/win and open its UI. */
  async launchApp(name, args = []) {
    const proc = spawn('go', ['run', `./apps/${name}/win`, '--listen', '127.0.0.1:0', '--no-ui', ...args],
      { cwd: root, env: { ...process.env, GOTOOLCHAIN: 'local' }, stdio: ['ignore', 'pipe', 'pipe'] });
    this.procs.push(proc);
    const url = await new Promise((res, rej) => {
      let buf = '';
      const onData = d => { buf += d.toString(); const m = buf.match(/listening on (\S+)/); if (m) res(m[1]); };
      proc.stdout.on('data', onData); proc.stderr.on('data', onData);
      proc.on('exit', code => rej(new Error(`${name} exited with ${code}\n${buf}`)));
      setTimeout(() => rej(new Error(`${name} did not report a listen address\n${buf}`)), 120000);
    });
    const context = await this.browser.newContext({ viewport: { width: 1100, height: 760 } });
    const page = await context.newPage();
    page.on('pageerror', e => console.error('  [pageerror]', e.message));
    page.on('console', m => { if (m.type() === 'error') console.error('  [console]', m.text()); });
    await page.goto(url, { waitUntil: 'load' });
    return { page, context, url, proc, server: { url } };
  }
  async cleanup() { for (const p of this.procs) p.kill('SIGTERM'); this.procs = []; }
}

const browser = await chromium.launch({ headless: !process.env.HEADED });
let failed = 0;
for (const f of files) {
  const t = new Harness(browser);
  const started = Date.now();
  try {
    const mod = await import(resolve(here, f));
    await mod.default(t);
    console.log(`✓ ${f} (${Date.now() - started} ms)`);
  } catch (e) {
    failed++;
    console.log(`✗ ${f}\n   ${e.stack || e}`);
  } finally {
    await t.cleanup();
  }
}
await browser.close();
console.log(`\n${files.length - failed} passed, ${failed} failed`);
process.exit(failed ? 1 : 0);
