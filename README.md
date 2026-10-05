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

## Building

```bash
make help        # list targets
make dmg         # every macOS app → dist-out/*.dmg      (macOS with Command Line Tools)
make windows     # every Windows exe → dist-out/*.exe    (any OS with Go 1.22+)
make test        # Go + web UI test suites
```

Individual apps: `make dmg-vitals`, `make exe-edgehop`, `make -C apps/clipwell install`, and so on.

## Layout

```
apps/<app>/mac/    Swift sources + Info.plist for the macOS app
apps/<app>/win/    Go sources for the Windows app
apps/<app>/ui/     web UI shared by both (rendered in WKWebView / WebView2)
shared/swift       Swift code compiled into every macOS app
shared/go          Go packages used by every Windows app
shared/ui          JS/CSS shared by every web UI
Tools/             build scripts (generic swiftc builder, DMG wrapper, icon generator)
releases/          historical binaries copied from the original repositories
docs/              architecture, protocols, status checklist
```

The macOS builds are ad-hoc signed. After dragging an app to `/Applications`
run `xattr -dr com.apple.quarantine /Applications/<App>.app` once, or open it
once and choose *Open Anyway* in System Settings ▸ Privacy & Security.

## Status

See [`docs/STATUS.md`](docs/STATUS.md) for the live build checklist.
