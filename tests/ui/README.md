# Web UI tests

`npm test` runs every `tests/ui/*.test.mjs` file with the globally installed
Playwright (`playwright` 1.56.1, Chromium at `/opt/pw-browsers`). Each test file
exports `export default async function (t) { … }` and receives a small harness:

```js
export default async function (t) {
  const { page, server } = await t.launchApp('vitals');   // builds+starts the Linux Go backend, opens its UI
  await t.expect(await page.textContent('h1'), /Vitals/);
  await t.screenshot(page, 'vitals-dashboard');            // → test-results/vitals-dashboard.png
}
```

`t.launchApp(name)` runs `go run ./apps/<name>/win --listen 127.0.0.1:0 --no-ui`
(the Linux build serves the UI over HTTP + WebSocket exactly like the Windows
exe does inside WebView2), waits for the `listening on` line, and opens the URL
in headless Chromium. Set `HEADED=1` to watch.
