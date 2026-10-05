// Exercises shared/ui/native.js in a plain page with a fake WKWebView handler.
export default async function (t) {
  const context = await t.browser.newContext();
  const page = await context.newPage();
  await page.addInitScript(() => {
    window.webkit = { messageHandlers: { native: { postMessage: (req) => {
      if (req.method === 'app.info') return { result: { name: 'Fake', platform: 'test', echo: req.params } };
      return { error: { code: 'not_supported', message: 'nope' } };
    } } } };
  });
  const base = await t.serveRepo();
  await page.goto(`${base}/tests/ui/fixtures/shim.html`);
  const r = await page.evaluate(async () => {
    const { native } = await import('/shared/ui/native.js');
    const out = { transport: native.transport };
    out.info = await native.call('app.info', { a: 1 });
    try { await native.call('nope'); } catch (e) { out.err = e.code; }
    const got = [];
    native.on('ping', d => got.push(d));
    window.__nativeEvent(JSON.stringify({ event: 'ping', data: { x: 2 } }));
    out.events = got;
    return out;
  });
  await t.expect(r.transport, 'wkwebview');
  await t.expect(r.info.name, 'Fake');
  await t.expect(r.info.echo.a, 1);
  await t.expect(r.err, 'not_supported');
  await t.expect(r.events.length, 1);
  await context.close();
}
