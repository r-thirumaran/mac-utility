# mac-utility

A suite of small, dependency-light desktop utilities, built from one repository.
Each app ships as a macOS disk image (`.dmg`) and, where the app makes sense on
Windows, a standalone Windows executable (`.exe`). Reach also ships a phone app.

| App | What it does | macOS | Windows | Phone |
|---|---|---|---|---|
| [Clipwell](apps/clipwell/) | Windows-style window snapping (`⌃⌘←/→/↑/↓`) and `Win+V`-style clipboard history | DMG | — | — |
| [Vitals](apps/vitals/) | CPU, memory, GPU and process monitor in one window, plus lid-close, sleep and display-off settings | DMG | EXE | — |
| [Edgehop](apps/edgehop/) | One mouse and keyboard across several Macs and Windows PCs: push the pointer off a screen edge and control hops to the next machine | DMG | EXE | — |
| [Reach](apps/reach/) | Control any laptop on your network from your phone: trackpad, keyboard, open apps, media keys, screen preview | DMG | EXE | PWA + Android APK + iOS project |

Prebuilt downloads: see the [Releases](../../releases) page. Historical Clipwell
images are kept in [`releases/clipwell/`](releases/clipwell/).

## Downloads

Every push builds all artifacts on GitHub Actions (see the **build** workflow):

| Artifact | Job | What you get |
|---|---|---|
| `clipwell-macos-dmg`, `vitals-macos-dmg`, `edgehop-macos-dmg`, `reach-macos-dmg` | macOS DMG (…) | drag-to-install disk image per app, ad-hoc signed, universal (Apple silicon + Intel) |
| `windows-exe` | Go tests + exe build (Windows) | `Vitals-<ver>-windows-amd64.exe`, `Edgehop-…exe`, `Reach-…exe` — single-file portable executables, no installer needed |
| `reach-android-apk` | Reach Android APK (Capacitor) | `Reach-<ver>-android-debug.apk` for sideloading; the iOS project is in `apps/reach/mobile/ios` (open in Xcode, sign with your Apple ID) |

Pushing a tag `v*` publishes all of them on a GitHub Release. Historical Clipwell
images are kept in [`releases/clipwell/`](releases/clipwell/).

**macOS first launch.** The builds are ad-hoc signed, so macOS says it "could not
verify" the app. Either run `xattr -dr com.apple.quarantine /Applications/<App>.app`
once, or open the app once, dismiss the message, then allow it under
System Settings ▸ Privacy & Security ▸ *Open Anyway*. Each app then asks for the
permissions it needs (Accessibility, Input Monitoring, Screen Recording) and
links you to the right settings pane. Those grants are tied to the code
signature, so after you rebuild an app yourself you may have to grant them again.

**Windows first launch.** Run the exe; allow the Windows Firewall prompt for
Edgehop and Reach (they listen on the local network). Vitals asks to restart as
administrator only when you change power settings. WebView2 (shipped with Windows
10/11) hosts the window; without it the app opens in your browser.

## Building

```bash
make help        # list targets
make dmg         # every macOS app → dist-out/*.dmg      (macOS with Command Line Tools)
make windows     # every Windows exe → dist-out/*.exe    (any OS with Go 1.24+)
make test        # Go test suites + Playwright web-UI tests (Linux/macOS)
```

Individual apps: `make dmg-vitals`, `make exe-edgehop`, `make -C apps/clipwell install`, and so on.
The phone app: `cd apps/reach/mobile && npm ci && npm run android` (then open `android/` in
Android Studio) or `npm run ios` (then `npx cap open ios`).

## Layout

```
apps/<app>/mac/    Swift sources + Info.plist for the macOS app
apps/<app>/win/    Go sources for the Windows app (the Linux build of the same code serves the UI for tests)
apps/<app>/ui/     web UI shared by both (rendered in WKWebView / WebView2; Reach serves it to phones)
apps/reach/mobile  Capacitor wrapper (Android + iOS projects) around the Reach phone app
shared/swift       Swift module compiled into every macOS app (menu bar, web window, HTTP/WebSocket server, Bonjour, crypto)
shared/go          Go packages used by every Windows app (bridge, HTTP/WebSocket, tray, WebView2 host, mDNS, crypto)
shared/ui          JS/CSS kit shared by every web UI (bridge shim, design tokens, charts, components)
shared/keymap      the canonical USB-HID ⇄ macOS keycode ⇄ Windows scancode table
Tools/             build scripts (generic swiftc builder, DMG wrapper, icon generator, keymap generator)
releases/          historical binaries copied from the original repositories
docs/              architecture, protocols (Edgehop wire protocol, Reach API), research notes, status checklist
tests/ui           Playwright tests that drive the real UIs against the Linux builds of the Go backends
```

## How the pieces fit

* **Vitals** samples CPU, memory, GPU, processes, network, disk and battery natively
  (Mach/IOKit on macOS, PDH and Win32 on Windows) and renders them in one shared
  dashboard; its Power page writes `pmset` settings (one admin prompt per Apply) or
  `powercfg` settings (elevated relaunch) and schedules sleep/shutdown.
* **Edgehop** runs the same symmetric protocol on every machine: whichever machine
  the pointer is on is *active*; a mouse or keyboard attached to any other machine
  forwards its input there, and pushing the pointer off a screen edge hands control
  to the neighbour in your layout. Peers find each other with Bonjour, pair once
  with a six-digit code and talk over an encrypted channel
  ([protocol](docs/edgehop-protocol.md)). Cameras, microphones and headsets are
  not virtualised: they stay with the computer they are physically plugged into.
* **Reach** puts a small agent on each laptop that advertises itself on the LAN,
  pairs with your phone by code, and exposes a trackpad, keyboard, media keys, app
  launcher, live screen preview and power actions ([API](docs/reach-api.md)). The
  phone app is a PWA served by any agent (`http://<laptop-ip>:7788`) and also ships
  as a native Android/iOS app.

## Status

See [`docs/STATUS.md`](docs/STATUS.md) for the live build checklist.
