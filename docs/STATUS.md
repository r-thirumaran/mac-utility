# Build orchestration status

Living checklist for the multi-app build. Any resumed session (including the
automatic 2-hourly orchestrator check-in) starts here. Keep it honest: a box is
ticked only when the thing exists on this branch and, where it has CI, CI is green.

Branch: `claude/busy-meitner-hqorah` · CI: `.github/workflows/build.yml`

## Apps

| App | What | Mac DMG | Windows exe | Mobile |
|---|---|---|---|---|
| Clipwell | window snapping + clipboard history (copied from r-thirumaran/Clipwell) | CI builds DMG (green) | n/a (macOS-only app) | n/a |
| Vitals | CPU / RAM / GPU / process monitor + power & lid-close settings | DMG built by CI (f6ad584) | exe built by CI, Go tests green on Linux + Windows | n/a |
| Edgehop | one mouse + keyboard across Macs and Windows PCs (screen-edge switching) | DMG built by CI (d98fa5b) | Go core + Windows backend + UI committed, tests green | n/a |
| Reach | control any laptop from your phone (agent + mobile app) | DMG built by CI (cd17a03) | Go agent committed, tests green on Linux + Windows, exe built | PWA committed (Playwright-tested); Capacitor 8 Android + iOS projects; APK built by CI (989dbc4) |

## Phase 0 — scaffold
- [x] Copy Clipwell source into `apps/clipwell/` (from r-thirumaran/Clipwell@dbdd2cd)
- [x] Copy the six release DMGs into `releases/clipwell/` and verify SHA-256
- [x] Top-level Makefile, `Tools/build_mac_app.sh`, `Tools/make_dmg.sh`, `Tools/make_icns.py`
- [x] CI workflow building the Clipwell DMG on a macOS runner
- [x] First CI run green: run 37361736347 built Clipwell-2.1.dmg on macos-15 in 51 s

## Phase 1 — design & research
- [x] `docs/ARCHITECTURE.md` (stack, layout, shared bridge contract)
- [x] Platform API research notes in `docs/research/` (5 of the planned notes landed: macOS/Windows stats + power, macOS remote actions; the remaining topics — macOS/Windows input capture, Windows remote actions, Capacitor — are verified by the implementing agents themselves) (GPU counters, pmset/powercfg, CGEventTap, SendInput, media keys, WebView2)
- [x] `docs/edgehop-protocol.md` (wire protocol, pairing, crypto)
- [x] `docs/reach-api.md` (agent HTTP/WebSocket API)

## Phase 2 — implementation
- [x] shared/go (tray, webview host, bridge, mdns, framing, crypto, http+ws) — tests green on Linux, builds for windows/amd64
- [x] shared/swift (menubar, WKWebView bridge, mini http+ws server, framing, crypto, admin runner) — type-checks on macos-15; XCTest suites run on macOS + Linux in CI
- [x] shared/ui (native.js bridge shim, base CSS, charts, components, demo) — Playwright test green
- [x] Vitals mac / win / ui — win backend + ui (6447c63); mac backend compiles and the Vitals DMG job is green (f6ad584)
- [x] Edgehop core + mac + win + ui — core/win/ui DONE; mac app compiles, Edgehop DMG job green (d98fa5b)
- [ ] Reach agent mac / win + PWA + Capacitor mobile project — win agent + PWA DONE (commit 956f3ee); mac agent running (batch 2); Capacitor pending
- [x] Icons for every app (`Tools/make_icons.py` → icns, ico, PWA PNGs)

## Phase 3 — verification
- [x] Go unit + integration tests green on Linux and Windows runners (run 37408123728: Vitals, Edgehop, Reach all pass natively on windows-latest; three exes built)
- [x] Web UI Playwright tests green on CI (kit, Vitals, Edgehop two-instance pairing + hop, Reach phone flow) — run on fe3600d after fixing a real pairing-dialog race
- [x] macOS DMGs for all four apps built by CI (Clipwell, Vitals, Edgehop d98fa5b, Reach cd17a03)
- [x] Windows exes for Vitals, Edgehop, Reach built by CI (artifact windows-exe)
- [x] Android APK for Reach built by CI (job 'Reach Android APK (Capacitor)' green on 989dbc4; artifact reach-android-apk)
- [ ] Adversarial code review pass per component, findings fixed — DONE: Windows-only Go paths (23d3a81), Vitals + Reach Swift runtime (9a8e7b1); running: Edgehop Swift runtime
- [x] README per app with install + permission walkthrough (apps/*/README.md) and the top-level README

## Orchestration plan (resume here after a usage-limit pause)
1. Batch 1 (running): three Linux-testable agents — Vitals win+ui, Edgehop core/win/ui, Reach win+PWA. Commit each as it lands; CI (go-linux, go-windows) must be green.
2. Batch 2: Vitals mac (continue from apps/vitals/mac/Stats), Edgehop mac, Reach mac — Swift, verified only by the macOS CI jobs; add each app to the macOS DMG matrix in .github/workflows/build.yml when its sources exist.
3. Batch 3: Reach Capacitor wrapper DONE; next: conformance pass (mac vs win method surfaces); adversarial code review; READMEs; tag v0.1.0 to publish the release with all DMGs/exes/APK.
Run at most three implementation agents at once: six in parallel exhausted the usage window before any of them finished.

## Blockers / notes
- **GitHub Actions is blocked by billing (2026-10-06 04:00 UTC).** Every job of run 37411605784 failed instantly with "The job was not started because recent account payments have failed or your spending limit needs to be increased". The private repo's free Actions allowance is exhausted (macOS runner minutes count 10×; ~54 runs so far). Until the owner raises the spending limit / fixes payment under GitHub ▸ Settings ▸ Billing & plans, or makes the repository public (public repos get free Actions minutes, including macOS), no CI run — and therefore no Swift compile check and no artifacts — can happen. Last fully green states: Linux + Windows jobs fe3600d/989dbc4, all four DMG jobs d98fa5b, Android 989dbc4. Commit 9a8e7b1 (Swift review fixes) and later are NOT yet compile-verified. The workflow now skips docs-only pushes, cancels superseded runs and rebuilds each macOS app only when its sources change, so minutes go much further once re-enabled.
- This build box is Linux: Swift cannot be compiled here (download.swift.org is blocked), so macOS code is verified only by the GitHub Actions macOS runner. Keep pushes small and watch the runs.
- Android SDK downloads (dl.google.com) are blocked locally; the APK is built only in CI.
