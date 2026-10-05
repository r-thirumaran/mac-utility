# Architecture

One repository, four apps, two desktop platforms, one phone app. This document is
the contract every component is built against. Read it before touching code.

## 1. Apps

| App | Platforms | Native backend | UI |
|---|---|---|---|
| Clipwell | macOS | Swift (unchanged import of r-thirumaran/Clipwell) | SwiftUI/AppKit (its own) |
| Vitals | macOS, Windows | Swift / Go | shared web UI (`apps/vitals/ui`) |
| Edgehop | macOS, Windows | Swift / Go | shared web UI (`apps/edgehop/ui`) |
| Reach | macOS, Windows agents; phone | Swift / Go | agent settings web UI + phone PWA (`apps/reach/ui`), Capacitor wrapper (`apps/reach/mobile`) |

## 2. Stack decisions (and why)

* **macOS apps are Swift, AppKit, zero third-party dependencies**, compiled with
  `swiftc` by `Tools/build_mac_app.sh` (the Clipwell approach). Everything that
  needs a TCC permission (Accessibility, Input Monitoring, Screen Recording) runs
  inside the app process so the grant attaches to one bundle.
* **Windows apps are pure Go (no cgo)** so they cross-compile from Linux and CI
  with `GOOS=windows`. Win32 is called through `golang.org/x/sys/windows` and
  `NewLazySystemDLL`. The exe is a tray app (`-H windowsgui`) that opens its UI
  in an embedded WebView2 window (`github.com/jchv/go-webview2`, pure Go) and
  falls back to the default browser if the WebView2 runtime is missing.
* **UI is plain HTML/CSS/ES-module JavaScript, no framework, no bundler.** The
  same files are rendered by WKWebView on macOS and WebView2 on Windows, and
  (for Reach) served to phones. Everything the UI needs from the OS goes through
  one bridge (section 4).
* **Linux is a first-class *test* target**, never a product. Every Go package
  has a Linux build (real `/proc` stats for Vitals, no-op input backends for
  Edgehop/Reach that record what they were asked to do) so the protocol, state
  machines and UIs are exercised end-to-end on the Linux CI box with Playwright.
* macOS code cannot be compiled on the Linux build box. It is verified by the
  macOS GitHub Actions runner on every push. Keep macOS pushes small.

## 3. Repository layout

```
apps/<app>/mac/            Swift sources, Info.plist, AppIcon.icns, Entitlements.plist (optional)
apps/<app>/win/            Go `package main` for the Windows exe (+ _linux.go test backends)
apps/<app>/ui/ui.go        `package ui` exposing `embed.FS` of ./static
apps/<app>/ui/static/      index.html, app.js, app.css … (the shared UI)
apps/<app>/dist/           READ-ME-FIRST.txt placed in the DMG
apps/<app>/VERSION         semantic version, single source of truth for the Windows build
apps/<app>/README.md       user-facing: install, permissions, how it works
shared/swift/              compiled into every macOS app (menu bar, bridge, server, crypto …)
shared/go/                 Go packages used by every Windows app (tray, webview, bridge, mdns …)
shared/ui/                 native.js (bridge shim), base.css, charts.js, icons
shared/keymap/             hid-keymap.json: the canonical cross-platform key table
docs/                      this file, protocol specs, research notes, STATUS.md
Tools/                     build scripts
```

Go: one module at the repo root (`go.mod`, module `github.com/r-thirumaran/mac-utility`).
Swift: no SwiftPM for the apps; `Tools/build_mac_app.sh` globs `apps/<app>/mac/**/*.swift`
and `shared/swift/**/*.swift`. Files under any `Tests/` directory are excluded from app
builds. A SwiftPM package may exist purely for Linux unit tests of Foundation-only code
(`shared/swift/Package.swift`), it is not used for the apps.

## 4. The native bridge (UI ⇄ backend)

`shared/ui/native.js` exports a single object:

```js
import { native } from './shared/native.js';
const info = await native.call('app.info');              // → Promise<result>
const off  = native.on('vitals.sample', sample => …);    // push events from the backend
```

Wire format is JSON-RPC-like and identical on every transport:

```
request   {"id": 7, "method": "power.apply", "params": {...}}
response  {"id": 7, "result": {...}}   |   {"id": 7, "error": {"code": "not_authorized", "message": "..."}}
event     {"event": "vitals.sample", "data": {...}}
```

Transports, auto-selected by the shim:

| Host | Transport |
|---|---|
| WKWebView (macOS) | `window.webkit.messageHandlers.native.postMessage(request)` (WKScriptMessageHandlerWithReply → Promise); events via `window.__nativeEvent(json)` called from `evaluateJavaScript` |
| WebView2 (Windows) | bound function `window.__nativeCall(requestJson)` → Promise<string>; events via `window.__nativeEvent(json)` from `Eval` |
| Browser / phone | WebSocket at `ws(s)://<host>/ws`, same JSON frames; `?token=` query for auth |

Method names are namespaced per app (`app.*` common; `vitals.*`, `power.*`, `edgehop.*`,
`reach.*`). Common methods every backend implements:

| Method | Result |
|---|---|
| `app.info` | `{name, version, platform: "macos"|"windows"|"linux", machine, permissions: {…}}` |
| `app.openURL {url}` | `{}` |
| `app.quit` | `{}` |
| `app.openSettingsPane {pane}` | `{}` — deep link to the OS privacy pane (macOS) or Settings page (Windows) |

Error codes are short snake_case strings: `not_authorized`, `needs_permission`,
`not_supported`, `invalid_params`, `internal`.

## 5. Per-app method surface

### Vitals
* `vitals.subscribe {intervalMs}` → starts `vitals.sample` events:
  `{t, cpu: {total, perCore: [], loadAvg?}, mem: {total, used, app, wired, compressed, cached, swapUsed, swapTotal, pressure}, gpu: [{name, utilization, memUsed, memTotal, renderer?, tiler?}], net: {rxBps, txBps}, disk: {readBps, writeBps}, battery: {percent, charging, onAC, timeToEmptyMin?}}`
* `vitals.unsubscribe`
* `vitals.processes {sortBy, limit}` → `[{pid, name, cpu, memBytes, icon?}]`
* `vitals.killProcess {pid}` (asks for confirmation in UI first)
* `power.get` → the current settings model (below)
* `power.apply {changes}` → applies; may raise an OS admin prompt. Returns `{applied: [...], failed: [{key, reason}]}`
* `power.action {action}` → `sleep | displayOff | lock | restart | shutdown`
* `power.schedule.get` / `power.schedule.set {items}` — recurring sleep/shutdown/wake
* `power.keepAwake {enabled, mode}` — root-free IOPMAssertion / SetThreadExecutionState

Settings model (keys are platform-neutral, backends map them):

```
{ onBattery: { displaySleepMin, systemSleepMin, lidClose: "sleep"|"nothing"|"hibernate"|"shutdown"|"displayOff" },
  onPower:   { displaySleepMin, systemSleepMin, lidClose: … },
  lidOpenWakes: bool, wakeOnNetwork: bool, powerNap?: bool, lowPowerMode?: bool,
  hibernate?: { enabled, mode }, powerButton?: "sleep"|"nothing"|"shutdown"|"hibernate"|"displayOff" }
```

### Edgehop
* `edgehop.state` → `{self: Machine, peers: [Machine], layout: Layout, active: machineId|null, role: "idle"|"sending"|"receiving", permissions}`
* `edgehop.layout.set {layout}`; `edgehop.pair.start {peerId}` / `edgehop.pair.confirm {peerId, code}` / `edgehop.unpair {peerId}`
* `edgehop.settings.get/set` — `{switchDelayMs, edgeCornerDeadZonePx, swapCmdCtrl, clipboardSync, hotkeySwitch, lockCursorWhileRemote}`
* events: `edgehop.state` (whole state on change), `edgehop.log {level, msg}`
* Machines are discovered with Bonjour `_edgehop._tcp`; see `docs/edgehop-protocol.md`.

### Reach (agent)
* Local settings UI: `reach.state`, `reach.settings.set`, `reach.pairings.list/revoke`, `reach.pair.showCode`
* Phone API over WebSocket: see `docs/reach-api.md`.

## 6. Shared Swift (`shared/swift`)

```
MenuBar/StatusItemHost.swift        NSStatusItem with a menu + optional live title
Web/WebWindowController.swift        NSWindow hosting WKWebView loading app://ui/index.html via WKURLSchemeHandler
Web/NativeBridge.swift               WKScriptMessageHandlerWithReply dispatcher: register("name") { params in … }
Web/BridgeTypes.swift                BridgeError, JSON helpers (Codable → [String: Any])
Net/FrameCodec.swift                 length-prefixed frames (shared with Edgehop protocol)
Net/HTTPServer.swift                 minimal HTTP/1.1 static server + WebSocket upgrade (Network.framework I/O)
Net/WebSocketFrame.swift             RFC 6455 framing (Foundation only, Linux-testable)
Net/Bonjour.swift                    advertise + browse wrappers over NWListener/NWBrowser
Crypto/SecureChannel.swift           X25519 + HKDF + ChaChaPoly channel (CryptoKit / swift-crypto)
System/AdminShell.swift              run a privileged shell script via osascript "with administrator privileges"
System/LaunchAtLogin.swift           SMAppService wrapper
System/Permissions.swift             Accessibility / Input Monitoring / Screen Recording checks + prompts + pane deep links
System/Log.swift                     os.Logger wrapper
```

## 7. Shared Go (`shared/go`)

```
tray/        tray icon + menu (getlantern/systray on Windows; no-op on Linux)
webhost/     WebView2 window (jchv/go-webview2) with the bridge bound; browser fallback; Linux: serves http://127.0.0.1 and opens nothing
bridge/      JSON-RPC dispatcher: Register(name, func(ctx, json.RawMessage) (any, error)); Emit(event, data); transports: webview2 bind + websocket
httpserve/   net/http static server from embed.FS + WebSocket (coder/websocket) endpoint wired to bridge
mdnsdisc/    advertise + browse (grandcat/zeroconf)
framing/     length-prefixed frames
securechan/  X25519 + HKDF + ChaCha20-Poly1305 channel (x/crypto)
winutil/     (windows only) elevation check/relaunch, single instance, autostart, open URL, toast
sysinfo/     Vitals stats: windows.go (PDH/GetSystemTimes/…), linux.go (/proc)
powerctl/    Vitals power settings: windows.go (powercfg/powrprof), linux.go (stub)
input/       Edgehop/Reach input capture+injection: windows.go (hooks/SendInput), linux.go (fake, records calls)
screen/      display geometry + capture: windows.go, linux.go (synthetic frame)
apps/        installed-app listing + launch: windows.go, linux.go (.desktop files)
keymap/      loads shared/keymap/hid-keymap.json; HID ⇄ mac keycode ⇄ win scancode
```

## 8. Conventions

* Versions: `apps/<app>/VERSION` (Windows) and `CFBundleShortVersionString` in
  `apps/<app>/mac/Info.plist` (macOS) must match; CI fails if they differ.
* Bundle ids: `com.thiru.<app>`; Bonjour types `_edgehop._tcp`, `_reach._tcp`.
* Config: macOS `~/Library/Application Support/<App>/config.json`; Windows
  `%APPDATA%\<App>\config.json`; Linux `$XDG_CONFIG_HOME/<app>/config.json`.
* Logs: macOS unified logging (subsystem `com.thiru.<app>`); Windows
  `%LOCALAPPDATA%\<App>\<app>.log` (rotating, 5 MB).
* Secrets (pairing keys) live in the config file with 0600 permissions; never in
  the UI's localStorage except the Reach phone's session token.
* Swift: 4-space indent, `// MARK:` sections, one type per file, no force unwraps
  outside tests, `@MainActor` for anything touching AppKit. Match Clipwell's style.
* Go: `gofmt`, `go vet` clean, `//go:build windows` / `linux` files, errors wrapped
  with `%w`, no panics outside `main`.
* JS: ES modules, `const`/`let`, no globals except the bridge shim's two hooks,
  CSS variables for theming, `prefers-color-scheme` dark mode, no external CDN.
* Every backend method is covered by at least one test on Linux (Go) or in CI
  (Swift via the macOS runner's `swift test` for Foundation-only packages).
