# Vitals

A small Task-Manager / Activity-Monitor style utility: CPU (total and per
core), memory, GPU, processes, network, disk and battery at a glance, plus the
power settings that are otherwise scattered across the OS (what the lid and
the power button do, display and sleep timers, hibernation, keep-awake,
scheduled sleep/shutdown).

One web UI (`apps/vitals/ui`) is shared by both platforms; each platform has
its own native backend that implements the same bridge methods
(`docs/ARCHITECTURE.md` §4–5).

## Windows

### Install

Download `Vitals-<version>-windows-amd64.exe` (built with `make exe-vitals`,
which bakes `apps/vitals/VERSION` into the exe) and run it. Vitals is a single
exe: no installer, no service, nothing written outside your profile.

* It lives in the notification area. The menu has **Open Vitals**, **Launch at
  login** (an `HKCU\…\Run` entry) and **Quit**.
* The window is an embedded WebView2 view of the UI served on a loopback port
  with a per-run token. If the WebView2 runtime is missing the UI opens in the
  default browser instead.
* Only one instance runs per user; starting a second one just exits.
* Log: `%LOCALAPPDATA%\Vitals\vitals.log` (5 MB, one backup). Schedule items:
  `%APPDATA%\Vitals\schedule.json`.

### Permissions

Reading stats and changing timers, lid and button actions works as a standard
user. Two things need administrator rights:

| Action | Why |
|---|---|
| Turning hibernation on/off, changing the hiberfile type | `powercfg /hibernate` creates or deletes `hiberfil.sys` |
| (nothing else) | sleep, lock, display off, shutdown/restart, keep-awake and Task Scheduler tasks run as you |

When an Apply needs elevation the UI shows a **Restart as administrator**
action; Vitals relaunches itself through UAC and the old instance exits. A
Group Policy that locks a power setting is reported as a failed key, never
auto-elevated.

### How the numbers are produced

| Reading | Source |
|---|---|
| CPU total / per core | `GetSystemTimes`, `NtQuerySystemInformation(SystemProcessorPerformanceInformation)` |
| Memory | `GlobalMemoryStatusEx` (used = total − available, commit charge, page file), `GetPerformanceInfo` (system cache) |
| GPU | PDH `\GPU Engine(*)\Utilization Percentage` aggregated the way Task Manager does (busiest engine per adapter), `\GPU Adapter Memory(*)` for VRAM, D3DKMT for names and sizes |
| Processes | `NtQuerySystemInformation(SystemProcessInformation)`; CPU % is per logical core against wall time, memory is the private working set; End task = `TerminateProcess` |
| Network | `GetIfTable2` byte counters over physical interfaces |
| Disk | PDH `\PhysicalDisk(_Total)\Disk Read/Write Bytes/sec` |
| Battery | `GetSystemPowerStatus` |
| Power settings | `powercfg /q` (parsed by hex index, so localized output is fine), written with `powercfg /change` and `/setacvalueindex` + `/setdcvalueindex` + `/setactive SCHEME_CURRENT`; hibernation from the registry (`HibernateEnabled`, `HiberFileType`) |
| Actions | `SetSuspendState`, `LockWorkStation`, `WM_SYSCOMMAND SC_MONITORPOWER`, `shutdown.exe /s|/r` |
| Keep awake | `SetThreadExecutionState(ES_CONTINUOUS \| ES_SYSTEM_REQUIRED [\| ES_DISPLAY_REQUIRED])` on a locked OS thread |
| Schedule | Task Scheduler tasks under `Vitals\` (`schtasks /create … /sc daily|weekly`); a scheduled *sleep* runs `Vitals.exe --power-action sleep` |

On Modern Standby machines `SetSuspendState` can be refused; Vitals then turns
the display off, which is the sleep entry on those systems.

### Command line

```
Vitals.exe [--listen 127.0.0.1:0] [--no-ui] [--debug] [--fake-power] [--interval 1s]
Vitals.exe --power-action sleep|displayOff|lock|restart|shutdown
Vitals.exe --version
```

`--no-ui` serves the UI only and prints `listening on http://127.0.0.1:<port>/?token=…`
(what the tests use). `--fake-power` swaps in an in-memory laptop model so the
Power page can be exercised anywhere. The same program builds on Linux as the
test backend (`go run ./apps/vitals/win --no-ui --fake-power`).

### Bridge methods

| Method | Handler (`apps/vitals/win/bridge.go`) | Backend |
|---|---|---|
| `app.info` | `handleInfo` | `Stats.Info()` |
| `app.openURL {url}` | `handleOpenURL` | `winutil.OpenURL` (http, https, mailto, ms-settings) |
| `app.quit` | `handleQuit` | cancels the run context |
| `app.openSettingsPane {pane}` | `handleOpenSettingsPane` | `ms-settings:powersleep` / `batterysaver` / `startupapps` |
| `vitals.subscribe {intervalMs}` → `vitals.sample` events | `handleSubscribe` → `sampler` | `Stats.Sample()` every 250 ms – 10 s |
| `vitals.unsubscribe` | `handleUnsubscribe` | stops with the last subscriber (or when no page is connected) |
| `vitals.processes {sortBy, limit}` | `handleProcesses` | `Stats.Processes()` sorted by cpu / mem / name / pid |
| `vitals.killProcess {pid, force}` | `handleKillProcess` | `Stats.Kill()` → `not_authorized` on access denied |
| `power.get` | `handlePowerGet` | `Power.Get()` + `elevated` |
| `power.apply {changes}` | `handlePowerApply` | `Power.Apply()` → `{applied, failed[{key, reason, needsElevation}]}`; `not_authorized` + `details.needsElevation` when nothing could be applied without elevation |
| `power.relaunchElevated` | `handleRelaunchElevated` | `winutil.RelaunchElevated` then exit (`not_supported` off Windows) |
| `power.action {action}` | `handlePowerAction` | `Power.Action()` |
| `power.schedule.get` / `.set {items}` | `handleScheduleGet` / `handleScheduleSet` | `Power.ScheduleGet/Set()` → `{items}` |
| `power.keepAwake {enabled, mode}` (+ `power.keepAwake` event) | `handleKeepAwake` | `Power.KeepAwake()` |
| `power.keepAwake.get` | `handleKeepAwakeGet` | `Power.AwakeState()` |

### Tests

```
GOTOOLCHAIN=local go test ./apps/vitals/...                          # parsers, power mapping, sampler, bridge
GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 go vet ./apps/vitals/...  # Windows compile check
node tests/ui/run.mjs 10-vitals                                       # Playwright against the Linux build
```

The Windows-only code paths (PDH, powercfg, Task Scheduler, WebView2, tray)
are compile-checked on Linux and exercised on the Windows CI job; everything
else runs against the Linux `/proc` backend.

## macOS

### Install

Download `Vitals-<version>.dmg` (built with `make dmg-vitals`, which runs
`Tools/build_mac_app.sh apps/vitals` and wraps `build/Vitals.app`), drag Vitals
to Applications and launch it. The build is ad-hoc signed, not notarised, so
the first launch shows "Apple could not verify Vitals is free of malware";
either run `xattr -dr com.apple.quarantine /Applications/Vitals.app` or open it
once, dismiss the error and use System Settings ▸ Privacy & Security ▸
"Open Anyway" (the DMG's `READ-ME-FIRST.txt` spells this out). Requires
macOS 14.

* It lives in the menu bar (no Dock icon). The item shows `CPU 27% · 9.6 GB`,
  refreshed every 2 s even while the window is closed; its menu has **Open
  Vitals** (⌘O), **Launch at Login** (`SMAppService`) and **Quit** (⌘Q). The
  window opens on first launch.
* The window is a `WKWebView` showing the shared UI straight from the app
  bundle (`app://ui/`); the bridge is `WKScriptMessageHandlerWithReply`, so
  nothing listens on a port and there is no token.
* A closed window keeps its page but stops receiving samples until it is
  shown again; sampling drops to the 2 s menu bar cadence meanwhile.
* Log: Console.app, subsystem `com.thiru.vitals` (categories `sampler`,
  `processes`, `power`, `bridge`). Disabled schedule items are kept in the
  app's UserDefaults.

### Permissions

Reading every statistic needs nothing — no root, no entitlement, no privacy
prompt. Writing is where macOS asks:

| Action | What macOS asks |
|---|---|
| **Apply** on the Power page, saving the **Schedule** | one administrator password prompt per Apply. Every `pmset` write of the batch goes into a single `do shell script … with administrator privileges`, so changing five settings costs one prompt. Cancelling the prompt changes nothing and comes back as `not_authorized`. |
| **Restart**, **Shut down** | a one-time Automation consent for System Events (the same Apple events the  menu sends; open apps may still refuse). A denial is reported as `needs_permission`. |
| **Lock** | nothing on macOS 14/15 (`SACLockScreenImmediate` in the login framework). If that symbol is ever missing, the fallback is a synthetic ⌃⌘Q, which needs Accessibility. |
| **Sleep**, **Turn off the display**, **Keep awake**, **End task** | nothing — `pmset sleepnow` / `displaysleepnow` run as the console user, keep-awake is an `IOPMAssertion`, End task is `kill(2)`. |
| Processes | other users' processes (root daemons, `_windowserver`, …) cannot be inspected without root; they are left out of the list and the gap is logged once. Run Vitals as root to see them. |

### What macOS cannot do

* **Lid close** is a single system-wide switch: *Sleep* (the default) or
  *Do nothing* (`pmset -a disablesleep 1`, which also disables sleep from the
   menu). Both the battery and the plugged-in row show the same value, and
  the kernel may still sleep on critical battery. There is no
  hibernate-on-lid, shut-down-on-lid or display-off-on-lid.
* **Hibernation** (`hibernatemode`) and the **power button** action are not
  exposed; `supported.hibernate` and `supported.powerButton` are `false`.
* **Wake on lid open**, **Wake for network access**, **Power Nap** and
  **Low Power Mode** are one toggle each (read from the AC section of
  `pmset -g custom`, written with `-a`). Low Power Mode appears only on Macs
  that report it in `pmset -g cap` (Apple silicon and recent Intel, macOS 12+).
* **Schedule**: `pmset repeat` holds at most one repeating wake and one
  repeating sleep/shutdown/restart, and has no "disabled" state. Vitals parks
  switched-off items in its own preferences and merges them back on read.
  Types: `sleep`, `shutdown`, `restart`, `wake`, `wakeorpoweron`.
* `power.relaunchElevated` is `not_supported`: there is no elevated mode; the
  password prompt covers each Apply.

### How the numbers are produced

| Reading | Source |
|---|---|
| CPU total / per core | `host_processor_info(PROCESSOR_CPU_LOAD_INFO)` tick deltas, `getloadavg` |
| Memory | `host_statistics64(HOST_VM_INFO64)`: app = anonymous − purgeable, wired, compressed, cached = file-backed + purgeable (Activity Monitor's rows); swap from `vm.swapusage`; pressure from `kern.memorystatus_vm_pressure_level` |
| GPU | `IOAccelerator` `PerformanceStatistics` (`Device Utilization %`, `Renderer`/`Tiler Utilization %`, `In use system memory`, or the Intel/AMD `GPU Activity(%)` / `vramUsedBytes` keys), names and sizes from Metal |
| Processes | `proc_listpids` + `proc_pidinfo(PROC_PIDTASKINFO)` (mach-time CPU deltas ÷ wall time ÷ cores), memory = `proc_pid_rusage` physical footprint (resident size when refused), names from `NSRunningApplication` or the executable path; `vitals.processes {icons: true}` adds 32 px PNG icons (base64) for GUI apps; End task = `SIGTERM` (`force` → `SIGKILL`) |
| Network | `sysctl NET_RT_IFLIST2` 64-bit `if_data64` counters summed over non-loopback, non-tunnel interfaces |
| Disk | `IOBlockStorageDriver` `Statistics` → `Bytes (Read)` / `Bytes (Write)` deltas over every driver |
| Battery | `IOPSCopyPowerSourcesInfo` (`Current Capacity`, `Is Charging`, `Time to Empty`, providing source) |
| Power settings | `pmset -g custom` (per source), `pmset -g` (`SleepDisabled`), `pmset -g cap` (what the hardware supports); written with one `pmset -b … -c … -a …` batch |
| Actions | `pmset sleepnow` (System Events `sleep` as fallback), `pmset displaysleepnow`, `SACLockScreenImmediate` → ⌃⌘Q → `CGSession -suspend`, System Events `restart` / `shut down` |
| Keep awake | `IOPMAssertionCreateWithName` — `PreventUserIdleSystemSleep` (system) or `PreventUserIdleDisplaySleep` (display); released on quit |
| Schedule | `pmset -g sched` ("Repeating power events"; the day mask comes from `com.apple.AutoWake.plist` when pmset only says "Some days"), `pmset repeat …` / `pmset repeat cancel` |

### Bridge methods

| Method | Handler (`apps/vitals/mac/Bridge/VitalsBridge.swift`) | Backend |
|---|---|---|
| `app.info` | `registerApp` → `info()` | `CPUSampler.brand/coreCount`, `ProcessInfo`; `platform: "macos"`, `elevated: false` |
| `app.openURL {url}` | `registerApp` | `NSWorkspace.open` (http, https, mailto, x-apple.systempreferences) |
| `app.quit` | `registerApp` | `NSApp.terminate` after the reply |
| `app.openSettingsPane {pane}` | `registerApp` → `openSettingsPane` | Battery / Energy Saver pane, `startup` → Login Items, `accessibility`, `automation` |
| `vitals.subscribe {intervalMs}` → `vitals.sample` events | `registerVitals` → `SystemSampler.subscribe` | one timer, 250 ms – 10 s, emitted on the main actor |
| `vitals.unsubscribe` | `registerVitals` → `SystemSampler.unsubscribe` | drops to the 2 s background cadence with the last subscriber |
| `vitals.processes {sortBy, limit, icons?}` | `registerVitals` → `ProcessSampler.list` on the process queue | sorted by cpu / mem / name / pid |
| `vitals.killProcess {pid, force}` | `registerVitals` | `kill(2)` → `not_authorized` on `EPERM`, `invalid_params` on `ESRCH` |
| `power.get` | `registerPower` → `PowerSettings.read` on the power queue | `pmset -g custom` / `-g` / `-g cap` + `KeepAwake.state` |
| `power.apply {changes}` | `registerPower` → `PowerSettings.apply` | one admin prompt → `{applied, failed[{key, reason}]}`; `not_authorized` when cancelled |
| `power.relaunchElevated` | `registerPower` | `not_supported` |
| `power.action {action}` | `registerPower` → `PowerActions.perform` | pmset / login framework / System Events |
| `power.schedule.get` / `.set {items}` | `registerPower` → `PowerSchedule.items/set` | `pmset -g sched`, `pmset repeat` → `{items}` |
| `power.keepAwake {enabled, mode}` (+ `power.keepAwake` event) | `registerPower` → `KeepAwake.set` | `IOPMAssertionCreateWithName` |
| `power.keepAwake.get` | `registerPower` → `KeepAwake.state` | in-memory state |

### Build

```
make vitals        # Tools/build_mac_app.sh apps/vitals release → build/Vitals.app
make dmg-vitals    # + Tools/make_dmg.sh → dist-out/Vitals-<version>.dmg
```

Only the Command Line Tools are needed (`swiftc`, `codesign`). The sources
under `apps/vitals/mac` are compiled together with `shared/swift` into one
module, Swift 5 language mode, macOS 14 target, ad-hoc signed and not
sandboxed (it spawns `pmset` and `osascript`).
