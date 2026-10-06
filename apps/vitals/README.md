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

The macOS app (Swift, `apps/vitals/mac`) implements the same bridge surface
on top of `host_statistics`, IOKit, `pmset` and IOPMAssertion. Its README
section is written alongside that backend.
