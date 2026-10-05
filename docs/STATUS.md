# Build orchestration status

Living checklist for the multi-app build. Any resumed session (including the
automatic 2-hourly orchestrator check-in) starts here. Keep it honest: a box is
ticked only when the thing exists on this branch and, where it has CI, CI is green.

Branch: `claude/busy-meitner-hqorah` · CI: `.github/workflows/build.yml`

## Apps

| App | What | Mac DMG | Windows exe | Mobile |
|---|---|---|---|---|
| Clipwell | window snapping + clipboard history (copied from r-thirumaran/Clipwell) | CI builds DMG (green) | n/a (macOS-only app) | n/a |
| Vitals | CPU / RAM / GPU / process monitor + power & lid-close settings | not started | not started | n/a |
| Edgehop | one mouse + keyboard across Macs and Windows PCs (screen-edge switching) | not started | not started | n/a |
| Reach | control any laptop from your phone (agent + mobile app) | not started | not started | not started |

## Phase 0 — scaffold
- [x] Copy Clipwell source into `apps/clipwell/` (from r-thirumaran/Clipwell@dbdd2cd)
- [x] Copy the six release DMGs into `releases/clipwell/` and verify SHA-256
- [x] Top-level Makefile, `Tools/build_mac_app.sh`, `Tools/make_dmg.sh`, `Tools/make_icns.py`
- [x] CI workflow building the Clipwell DMG on a macOS runner
- [x] First CI run green: run 37361736347 built Clipwell-2.1.dmg on macos-15 in 51 s

## Phase 1 — design & research
- [x] `docs/ARCHITECTURE.md` (stack, layout, shared bridge contract)
- [ ] Platform API research notes in `docs/research/` (GPU counters, pmset/powercfg, CGEventTap, SendInput, media keys, WebView2)
- [x] `docs/edgehop-protocol.md` (wire protocol, pairing, crypto)
- [x] `docs/reach-api.md` (agent HTTP/WebSocket API)

## Phase 2 — implementation
- [ ] shared/go (tray, webview host, bridge, mdns, framing, crypto, http+ws)
- [ ] shared/swift (menubar, WKWebView bridge, mini http+ws server, framing, crypto, admin runner)
- [ ] shared/ui (native.js bridge shim, base CSS, charts)
- [ ] Vitals mac / win / ui
- [ ] Edgehop core + mac + win + ui
- [ ] Reach agent mac / win + PWA + Capacitor mobile project
- [ ] Icons for every app (`Tools/make_icns.py`, Windows .ico via go-winres)

## Phase 3 — verification
- [ ] Go unit + integration tests green on Linux and Windows runners
- [ ] Web UI Playwright tests green
- [ ] macOS DMGs for all four apps built by CI
- [ ] Windows exes for Vitals, Edgehop, Reach built by CI
- [ ] Android APK for Reach built by CI
- [ ] Adversarial code review pass per component, findings fixed
- [ ] README per app with install + permission walkthrough

## Blockers / notes
- This build box is Linux: Swift cannot be compiled here (download.swift.org is blocked), so macOS code is verified only by the GitHub Actions macOS runner. Keep pushes small and watch the runs.
- Android SDK downloads (dl.google.com) are blocked locally; the APK is built only in CI.
