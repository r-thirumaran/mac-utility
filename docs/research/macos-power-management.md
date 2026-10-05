# macOS power management from an app (Vitals) — API fact sheet

## Summary
- Reading is root-free (`pmset -g custom`, `pmset -g`, `pmset -g sched`, IOKit); **every write via `pmset` needs uid 0** (pmset prints `pmset: Must be run as root to modify settings`, exit 1).
- One `pmset` invocation accepts many `setting value` pairs and interleaved `-a/-b/-c/-u` flags, so a whole settings panel can be committed with **one** `do shell script … with administrator privileges` → one password prompt.
- Lid-close sleep is decided in the kernel: `!clamshellDisabled && !(desktopMode && acAdaptorConnected) && !clamshellSleepDisableMask`; IOPM assertions do not veto it; only `pmset -a disablesleep 1` (`SleepDisabled`, the "user-space sleep kill switch") does.
- Root-free keep-awake: `IOPMAssertionCreateWithName` (idle system / idle display) or `/usr/bin/caffeinate`.
- Lock screen on 14/15: private `SACLockScreenImmediate` (login.framework) is what shipping Swift apps use; `CGSession -suspend` is reported dead since Big Sur; keystroke ⌃⌘Q needs Automation+Accessibility consent.

Sources verified against Apple open source: `PowerManagement-1846.0.25.0.1` (`pmset/pmset.m`, `pmset/pmset.1`, `caffeinate/caffeinate.8`), `IOKitUser` (`pwr_mgt.subproj/IOPMLib.h`, `IOPMLibPrivate.h`, `IOPMKeys.h`, `IOPMEnergyPrefs.c`, `ps.subproj/IOPowerSources.h`, `IOPSKeys.h`), `xnu-12377.1.9` (`iokit/IOKit/pwr_mgt/IOPM.h`, `iokit/Kernel/IOPMrootDomain.cpp`). Line numbers below refer to those files.

## 1. pmset settings

Scopes (`pmset.1`): `-a` all, `-b` battery, `-c` charger (wall power), `-u` UPS. Per-source dictionaries are keyed `"Battery Power"`, `"AC Power"`, `"UPS Power"` (`IOPowerSources.h:201-215`). In `pmset.m:5276-5310` the `apply` mask is reset and re-evaluated every time a flag is seen, so `pmset -b displaysleep 5 -c displaysleep 15` is one invocation; with no flag at all the pairs apply to every source present ("Unless specified, apply changes to both battery and AC"). Every argument is lower-cased in place before matching. Settings are persisted by powerd in `/Library/Preferences/SystemConfiguration/com.apple.PowerManagement.plist` (`pmset.1` FILES); schedules in `/Library/Preferences/SystemConfiguration/com.apple.AutoWake.plist`. System-wide settings (not per power source) are written by `IOPMSetSystemPowerSetting()` into CFPreferences domain `com.apple.PowerManagement`, key `SystemPowerSettings` (`IOPMEnergyPrefs.c:1622-1660`, which also enforces `getuid()==0 || geteuid()==0` → `kIOReturnNotPrivileged`).

| CLI name | Internal key (`IOPMLibPrivate.h`) | Values | Meaning (`pmset.1`) | Root |
|---|---|---|---|---|
| `displaysleep` | `Display Sleep Timer` | minutes, 0 = never | display sleep timer | write |
| `sleep` | `System Sleep Timer` | minutes, 0 = never | system idle sleep timer | write |
| `disksleep` | `Disk Sleep Timer` | minutes, 0 = never | disk spindown | write |
| `womp` | `Wake On LAN` | 0/1 | "Wake for network access" | write |
| `lidwake` | `Wake On Clamshell Open` | 0/1 | wake when lid opened | write |
| `acwake` | `Wake On AC Change` | 0/1 | wake when power source changes | write |
| `halfdim` | `Display Sleep Uses Dim` | 0/1 | dim to half brightness before display off | write |
| `ttyskeepawake` | `TTYSPreventSleep` | 0/1 | active tty (ssh) prevents idle sleep | write |
| `powernap` | `DarkWakeBackgroundTasks` | 0/1 | Power Nap (supported machines) | write |
| `proximitywake` | `ProximityDarkWake` | 0/1 | wake from nearby same-iCloud devices (supported systems) | write |
| `tcpkeepalive` | `TCPKeepAlivePref` | 0/1 | keep TCP connections alive during sleep (undocumented in man page; `pmset.m:120,331`) | write |
| `lowpowermode` | `LowPowerMode` | 0/1 | Low Power Mode (macOS 12+). Shown/accepted as `powermode` 0/1/2 (off/low/high) on Macs that support High Power Mode (`pmset.m:1143-1152`, `4422-4500`); value validated per power source with `IOPMFeatureIsAvailable`, prints `LowPowerMode not supported on Battery Power` otherwise | write |
| `standby` | `Standby Enabled` | 0/1 | hibernate after sleeping `standbydelay*` seconds | write |
| `standbydelaylow` | `Standby Delay` | seconds | used when battery < `highstandbythreshold` | write |
| `standbydelayhigh` | `Standby Delay High` | seconds | used when battery ≥ threshold | write |
| `highstandbythreshold` | `Standby Battery Threshold` | percent (default 50) | threshold for the two delays | write |
| `autopoweroff` | `AutoPowerOff Enabled` | 0/1 | EU Lot 6 chipset-off after `autopoweroffdelay` | write |
| `autopoweroffdelay` | `AutoPowerOff Delay` | seconds | delay before autopoweroff | write |
| `hibernatemode` | `Hibernate Mode` | 0 (desktop default), 3 (portable default), 25 (hibernate; pmset-only) | see SAFE SLEEP in `pmset.1` | write |
| `hibernatefile` | `Hibernate File` | path on root volume | sleep image location | write |
| `destroyfvkeyonstandby` | `DestroyFVKeyOnStandby` (system-wide) | 0/1 | drop FileVault key on standby | write |
| `disablesleep` | `SleepDisabled` (system-wide, `IOPMLibPrivate.h:1984`) | 0/1 (any non-zero = disable) | kernel-level veto on idle **and** demand sleep (lid close, Apple menu). `pmset.m:5813-5836` sets `kIOPMSleepDisabledKey` in `SystemPowerSettings`; powerd pushes it to IOPMrootDomain, which sets `userDisabledAllSleep` (`IOPMrootDomain.cpp:2055-2058`, `8386-8388`) and `checkSystemSleepAllowed()` then fails with `kPMUserDisabledAllSleep` ("user-space sleep kill switch", `7679-7682`) | write |
| `ring`, `autorestart`, `lessbright`, `sms`, `networkoversleep`, `gpuswitch` | — | 0/1 | legacy/desktop; `networkoversleep` "changing its value is unsupported" | write |

Not scoped: `disablesleep` and `destroyfvkeyonstandby` ignore `-a/-b/-c` (system-wide dict). `pmset restoredefaults` resets everything (root). All reads (`-g …`) are root-free.

`disablesleep` caveats:
- It is a preference, not a runtime flag: persists across reboots until `pmset -a disablesleep 0`. Apple's menu *Sleep* item becomes inert. Thermal/battery responsibility is on the app; the internal panel stays lit under a closed lid unless `displaysleep` is short or you call `pmset displaysleepnow`.
- Apple silicon: firsthand reports (Sleepless README, "confirmed on macOS 26.3", M-series) say the flag keeps the Mac awake with the lid closed on battery; other guides claim Ventura+ Apple-silicon machines still sleep. Treat as hardware/OS dependent and verify `pmset -g | grep SleepDisabled` plus a lid test; critical-battery sleep (`kIOPMPowerEmergency`) is reported to override it.
- Alternative that needs no root: clamshell mode proper (external display + power; §4).

## 2. Output formats (parse by name, never by position; CFDictionary iteration order is unspecified)

`pmset -g` ≡ `pmset -g live` (`pmset.m:567-570`, `594`). Printing code `show_system_power_settings` (`1184-1210`) then `show_live_pm_settings` (`1524-1537`) → `show_pm_settings_dict` (`1071-1183`):

```
System-wide power settings:
 SleepDisabled\t\t0                       ← only lines present in SystemPowerSettings: SleepDisabled, DestroyFVKeyOnStandby, VACTDisabled (tabs, not padding)
Currently in use:
 displaysleep         10 (display sleep prevented by Vitals)
 sleep                0 (sleep prevented by coreaudiod, powerd)
 lowpowermode         0                   ← or "powermode 0|1|2" when High Power Mode is supported
 hibernatefile        /var/vm/sleepimage
 Sleep On Power Button 1                  ← unknown keys printed with their raw key name
```
Line format: `" %-20s "` + value (`%d` for CFNumber/CFBoolean, `%s` for CFString), optional suffix ` (sleep prevented by a, b)` / ` (display sleep prevented by …)` (`1264`, `1306`, names joined with `", "`, closed with `")"`), optional ` (Graphics dim captured)` after `displaysleep`. Live view prunes settings the hardware does not support (`IOPMFeatureIsAvailable`). Parse: `^\s(\S+)\s+(\S+)(?:\s\((.*)\))?$`.

`pmset -g custom` (`1480-1520`): for each power source `"%s:\n"` then the same `" %-20s value"` lines, no suffixes, no pruning:
```
Battery Power:
 lidwake              1
 lowpowermode         0
 standbydelayhigh     86400
 ...
AC Power:
 ...
```
Parse: section header `^(\S[^:]*):$` (`Battery Power`, `AC Power`, `UPS Power`), setting `^\s(\S+)\s+(.+)$`.

`pmset -g sched` (`1690-1830`):
```
No scheduled events.                          ← when both IOPMCopyRepeatingPowerEvents and IOPMCopyScheduledPowerEvents return NULL
Repeating power events:
  wakepoweron at 7:00AM every day             ← "  %s at %s %s": raw type (wake|poweron|wakepoweron|sleep|shutdown|restart),
  shutdown at 11:59PM weekdays only              time "%d:%d%d%cM" (12-hour, no leading zero), days: every day | weekdays only |
                                                 weekends only | Monday…Sunday | Some days
Scheduled power events:
 [0]  wakeorpoweron at 07/04/2026 20:00:00 by 'Vitals'   ← " [%ld]  %s at %s" + optional " by '%s'" + " leeway secs: %d" + " User visible: true"
```
Scheduled date format is `MM/dd/yyyy HH:mm:ss` (`kDateAndTimeFormat`, `pmset.m:267`); the type `wakepoweron` is renamed `wakeorpoweron` in this list only (`1780-1784`). Older macOS printed `MM/dd/yy`; accept both. Repeating events also appear in the scheduled list with author `Repeating` on some versions (observed in third-party docs; treat as possible).

## 3. Scheduling (all writes need root; `kIOReturnNotPrivileged` → "pmset: Must be run as root to modify settings")

```
pmset schedule <type> "MM/dd/yy HH:mm:ss" [owner]        # one-off; type ∈ sleep|wake|poweron|shutdown|restart|wakeorpoweron
pmset schedule cancel <type> "MM/dd/yy HH:mm:ss" [owner]  # args must match the original exactly (IOPMCancelScheduledPowerEvent)
pmset schedule cancelall                                  # IOPMCancelAllScheduledPowerEvents()
pmset repeat <type> <days> HH:mm:ss [<type> <days> HH:mm:ss]   # days ⊆ MTWRFSU, case-insensitive; R=Thursday, U=Sunday
pmset repeat cancel                                       # IOPMCancelAllRepeatingPowerEvents()
pmset relative wake|poweron <seconds>                     # cannot be cancelled, imprecise
```
Rules (`pmset.1` SCHEDULED EVENT ARGUMENTS; `IOPMLibPrivate.h:276-303`): at most one repeating "on" event (wake/poweron/wakeorpoweron) and one "off" event (sleep/shutdown/restart); every `pmset repeat` call replaces both. Day bits: M=1 T=2 W=4 R=8 F=16 S=32 U=64 (`IOPMLibPrivate.h:258-266`). Input date is parsed with `CFDateFormatter` using `MM/dd/yy HH:mm:ss` (24-hour, quote it); the time-only format is `HH:mm:ss`. Type keyword is lower-cased before matching; the owner string keeps its case. Error strings to detect: `Error: Unspecified scheduled event type`, `Error: Badly formatted date`, `Error: badly formatted repeating power event`, `pmset: Error 0x… scheduling repeating events`. Examples from the man page: `pmset repeat wakeorpoweron T 12:00:00 sleep MTWRFSU 20:00:00`, `pmset schedule wake "07/04/16 20:00:00"`.

## 4. Clamshell facts (kernel, `IOPMrootDomain.cpp`)

- Lid close sleeps iff `shouldSleepOnClamshellClosed()` = `!clamshellDisabled && !(desktopMode && acAdaptorConnected) && !clamshellSleepDisableMask` (`4461-4471`). `clamshellDisabled` is set by graphics when an external display appears (`kIOPMDisableClamshell`, `8877-8882`) and cleared when it goes away; `desktopMode` is set by graphics (`kIOPMSetDesktopMode`, `8790-8793`). Hence native clamshell mode = external display present (+ AC in desktop mode). Apple's support article lists the user-facing requirements as: power adapter (or USB-C/Thunderbolt display that powers the Mac), external display, external keyboard and mouse/trackpad; with none of these, closing the lid sleeps the Mac.
- On every change the root domain sets registry properties `AppleClamshellState` (Bool, true = closed) and `AppleClamshellCausesSleep` (Bool) and sends `kIOPMMessageClamshellStateChange` = `iokit_family_msg(sub_iokit_powermanagement, 0x100)` with bit flags `kClamshellStateBit = 1<<0`, `kClamshellSleepBit = 1<<1` (`IOPM.h:430-444`, `IOPMrootDomain.cpp:4489-4508`). Properties are absent on Macs without a lid (`clamshellExists == false`).
- Closing the lid on battery with no external display and `SleepDisabled = 1`: sleep is vetoed (`checkSystemSleepAllowed`), but the internal display stays on until `displaysleep` fires. Full wake after a dark wake is refused with the lid closed and no AC (`7802-7812`).
- `lidwake 0` stops lid-open wake; unrelated to clamshell sleep.

## 5. Running privileged commands from an unsandboxed Swift app

| Mechanism | Status | Notes |
|---|---|---|
| `NSAppleScript` / `osascript` with `do shell script "…" with administrator privileges` | Supported; Apple DTS calls it "the pragmatic choice" for one-shot elevation (Quinn, *BSD Privilege Escalation on macOS*, forums thread 708765) | One auth dialog per `do shell script … with administrator privileges` statement; TN2065: the same script is not re-prompted for 5 minutes. Runs under `/bin/sh` as uid 0. No TCC prompt (no target app). Batch everything into one statement. Use `quoted form of` / shell-escape. In-process `NSAppleScript` shows the dialog with your app's name; `osascript` shows "osascript". |
| `AuthorizationExecuteWithPrivileges` | Deprecated macOS 10.7 (introduced 10.1); still links. Apple doc deprecation text: "Use a launchd-launched helper tool and/or the Service Management framework." | Signature `OSStatus AuthorizationExecuteWithPrivileges(AuthorizationRef, const char *pathToTool, AuthorizationFlags, char *const *arguments, FILE **communicationsPipe)`. Avoid. |
| `SMAppService.daemon(plistName:)` + XPC (macOS 13+) | Modern "ongoing privileges" path. **Future**: needs Developer ID signing (and notarization is effectively required on 14), an XPC helper, `Contents/Library/LaunchDaemons/<plist>`, `BundleProgram`, `AssociatedBundleIdentifiers` | `register()` throws; `status == .requiresApproval` until the user enables it in System Settings › General › Login Items & Extensions (`openSystemSettingsLoginItems()`); daemon bootstraps on each boot afterwards. `SMJobBless` deprecated in 13. |
| sudoers drop-in | Works, no Apple API | Install once with admin prompt: `/etc/sudoers.d/vitals` containing `<user> ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 0, /usr/bin/pmset -a disablesleep 1`, mode 0440 root:wheel, validated with `visudo -cf <tmp>`; then `sudo -n /usr/bin/pmset -a disablesleep 1` with no prompt. (Sleepless `grant.sh`.) Only enumerable exact command lines are safe; never wildcard `pmset`. |

`NSAppleScript` gotchas: call `executeAndReturnError(_:)` not `compileAndReturnError`; its Swift return type is non-optional but can be nil — cast `as NSAppleEventDescriptor?`; error dict key `NSAppleScript.errorNumber` = -128 means user cancelled. Run on the main thread. `NSAppleEventsUsageDescription` and the `com.apple.security.automation.apple-events` entitlement are only needed when the script targets another app (e.g. System Events), not for `do shell script`.

## 6. Root-free keep-awake

`IOPMLib.h:756-782`: `IOReturn IOPMAssertionCreateWithName(CFStringRef AssertionType, IOPMAssertionLevel AssertionLevel, CFStringRef AssertionName, IOPMAssertionID *AssertionID)` — "No special privileges are necessary"; name ≤ 128 chars, non-NULL. `IOPMAssertionRelease(IOPMAssertionID)`. Levels: `kIOPMAssertionLevelOn = 255`, `kIOPMAssertionLevelOff = 0`. Assertions die with the process.

| Constant | String | Effect |
|---|---|---|
| `kIOPMAssertionTypePreventUserIdleSystemSleep` (alias `kIOPMAssertPreventUserIdleSystemSleep`) | `PreventUserIdleSystemSleep` | no idle system sleep; display may sleep; "system may still sleep for lid close, Apple menu, low battery" (Apple doc) |
| `kIOPMAssertionTypePreventUserIdleDisplaySleep` (alias `kIOPMAssertPreventUserIdleDisplaySleep`) | `PreventUserIdleDisplaySleep` | no idle display sleep (implies no idle system sleep); does not turn a dark display on |
| `kIOPMAssertionTypePreventSystemSleep` | `PreventSystemSleep` | blocks system sleep incl. lid, **AC only** (caffeinate `-s`); header marks it deprecated 10.9 but caffeinate still uses it |
| `kIOPMAssertPreventDiskIdle` | `PreventDiskIdle` | caffeinate `-m` |
| `kIOPMAssertionTypeNoIdleSleep` / `…NoDisplaySleep` | `NoIdleSleepAssertion` / `NoDisplaySleepAssertion` | deprecated 10.7, avoid |

`IOPMAssertionDeclareUserActivity(CFStringRef name, IOPMUserActiveType kIOPMUserActiveLocal, IOPMAssertionID *)` lights the display like a key press. `IOPMAssertionCreateWithDescription(type, name, details, humanReadableReason, localizationBundlePath, CFTimeInterval timeout, CFStringRef timeoutAction /* kIOPMAssertionTimeoutActionRelease */, &id)` for timed assertions. Inspect with `pmset -g assertions`.

`/usr/bin/caffeinate` (`caffeinate.8`, `caffeinate.c:53-57`): `-d` display, `-i` idle system (default when no flag), `-m` disk, `-s` system (AC only), `-u` user active (default 5 s unless `-t`), `-t secs`, `-w pid`, or `caffeinate [flags] utility args…`. Kill the process to release.

## 7. Immediate actions

| Action | Command / API | Privilege | Notes |
|---|---|---|---|
| Sleep now | `pmset sleepnow` → `IOPMSleepSystem(IOPMFindPowerManagement(MACH_PORT_NULL))` (`pmset.m:695-712`) | "root **or the console user**" (`IOPMLib.h:78-84`) | Call IOKit directly from Swift (Hammerspoon does); on `kIOReturnNotPrivileged` pmset prints `Sleep error 0x…; You must run this as root.` exit 77 |
| Display off | `pmset displaysleepnow` → `SLSDisplayManagerRequestDisplaysIdle()` or `IODisplayWrangler` `IORequestIdle=true` (`982-1008`) | none | Locks only if *Require password after display off* is "immediately". Prefer shelling out; the IODisplayWrangler path is the legacy branch. |
| Lock screen | `SACLockScreenImmediate()` from `/System/Library/PrivateFrameworks/login.framework/login` via `dlopen`/`dlsym` | none | Private API (used by Hammerspoon `hs.caffeinate.lockScreen`, and 2024-25 Swift apps: EdgeSwipe, AnyDoor, MacTools, adrafinil…). Returns `Int32` in those bindings. Guard `dlsym == nil` → fall back. |
| Lock screen (fallback) | `osascript -e 'tell application "System Events" to key code 12 using {control down, command down}'` | Automation (System Events) + Accessibility; "osascript is not allowed to send keystrokes (1002)" otherwise | Breaks if the user remapped ⌃⌘Q. |
| Lock screen (legacy) | `/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession -suspend` | — | Reported non-functional since Big Sur (forums 682918); do not rely on it. |
| Screen saver | `open -a ScreenSaverEngine` | none | Locks only if the password-after-screensaver setting is immediate. |
| Shut down / restart / sleep / log out | `tell application "System Events" to shut down` / `restart` / `sleep` / `log out` | Automation consent for System Events (`NSAppleEventsUsageDescription`; hardened runtime → `com.apple.security.automation.apple-events`) | Equivalent to Apple events `kAEShutDown`/`kAERestart`/`kAESleep`/`kAEReallyLogOut` sent to loginwindow (QA1134); apps can cancel. `shutdown -h now` needs root. |

## 8. Reading lid state and power source

- Lid: `IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"))` → `IORegistryEntryCreateCFProperty(svc, "AppleClamshellState" as CFString, kCFAllocatorDefault, 0)?.takeRetainedValue() as? Bool` (nil on Macs without a lid). `AppleClamshellCausesSleep` the same way. Change events: `IOServiceAddInterestNotification(port, svc, kIOGeneralInterest, cb, ctx, &notifier)` and test `messageType == kIOPMMessageClamshellStateChange`, `UInt(bitPattern: arg) & 1` (closed), `& 2` (would sleep). `ioreg -r -k AppleClamshellState -d 4` for debugging.
- Power source: `IOPSCopyPowerSourcesInfo()` → `IOPSGetProvidingPowerSourceType(snapshot)` returns `"AC Power"` / `"Battery Power"` / `"UPS Power"` (`IOPowerSources.h:307-317`); detail dicts via `IOPSCopyPowerSourcesList` + `IOPSGetPowerSourceDescription` (keys `Power Source State`, `Is Charging`, `Current Capacity`). Notifications: `IOPSNotificationCreateRunLoopSource` (every % change) or `IOPSCreateLimitedPowerNotification` (10.9+, AC↔battery only).

## Pitfalls
1. `pmset` exits 1 and prints to **stderr** when not root; check exit status, not stdout.
2. `-g custom` lists values for *all* sources, including settings this Mac ignores; `-g` (live) prunes unsupported ones — use `-g cap` or the live list to decide which controls to show.
3. `lowpowermode` vs `powermode`: on High-Power-Mode Macs the live/custom output shows `powermode 0|1|2` and `pmset -a lowpowermode 1` prints "not supported" if LPM is unavailable on a source; write per source and check the printed error.
4. Assertions never stop lid-close sleep; `caffeinate -s` only on AC. Only `disablesleep` does, and it persists until cleared.
5. Repeating schedule: one on + one off event max; `pmset repeat` replaces both every time; `sleep`/`shutdown`/`restart` cannot be mixed in the same pair with mismatched on-type (sleep↔wake, shutdown↔poweron).
6. `schedule cancel` needs the identical type, date string and owner; otherwise `cancelall`.
7. `do shell script` runs `sh`; `$PATH` is minimal — use `/usr/bin/pmset`. Each separate `with administrator privileges` statement is another prompt (same script reuses the 5-minute cache).
8. `NSAppleScript` must run on the main thread; result cast to optional.
9. `displaysleepnow`/screen saver do not lock unless the password-delay preference is immediate; `SACLockScreenImmediate` always locks.
10. `kIOMasterPortDefault` is deprecated; use `kIOMainPortDefault` (macOS 12+).
11. Parsing `-g` lines: hibernatefile value contains a path; values may be followed by a parenthesised suffix — strip ` (…)` before converting.
12. Shutdown/restart via System Events require the user to grant Automation to Vitals once; denial yields error -1743.

## Swift sketches

```swift
import Foundation
import IOKit
import IOKit.pwr_mgt
import IOKit.ps

// --- 1. Read settings (root-free) ------------------------------------------
struct PMSettings { var bySource: [String: [String: String]] = [:]; var systemWide: [String: String] = [:] }

func runPmset(_ args: [String]) throws -> (status: Int32, out: String, err: String) {
    let p = Process(); p.executableURL = URL(fileURLWithPath: "/usr/bin/pmset"); p.arguments = args
    let o = Pipe(), e = Pipe(); p.standardOutput = o; p.standardError = e
    try p.run(); p.waitUntilExit()
    return (p.terminationStatus,
            String(decoding: o.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self),
            String(decoding: e.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self))
}

func parseCustom(_ text: String) -> PMSettings {          // output of `pmset -g custom`
    var s = PMSettings(); var section = ""
    for raw in text.split(separator: "\n") {
        let line = String(raw)
        if !line.hasPrefix(" "), line.hasSuffix(":") { section = String(line.dropLast()); continue } // "Battery Power:"
        guard line.hasPrefix(" ") else { continue }
        let parts = line.trimmingCharacters(in: .whitespaces).split(separator: " ", maxSplits: 1)
        guard parts.count == 2 else { continue }
        var value = String(parts[1])
        if let paren = value.range(of: " (") { value = String(value[..<paren.lowerBound]) } // "(sleep prevented by …)"
        if section == "System-wide power settings" { s.systemWide[String(parts[0])] = value }
        else { s.bySource[section, default: [:]][String(parts[0])] = value }
    }
    return s
}

// --- 2. Write settings: ONE prompt for the whole batch ---------------------
enum PMScope: String { case all = "-a", battery = "-b", charger = "-c" }
func shellQuote(_ s: String) -> String { "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'" }

/// pairs: [(scope, name, value)] → `/usr/bin/pmset -b displaysleep 5 -c displaysleep 15 -a disablesleep 1`
func applyAsAdmin(_ pairs: [(PMScope, String, String)], extra: [String] = []) throws {
    var argv = ["/usr/bin/pmset"]
    for (scope, k, v) in pairs { argv += [scope.rawValue, k, v] }          // flags may be interleaved (pmset.m:5276-5310)
    var cmd = argv.map(shellQuote).joined(separator: " ")
    for e in extra { cmd += " && " + e }                                   // e.g. "/usr/bin/pmset repeat cancel"
    let src = "do shell script \(appleScriptString(cmd)) with administrator privileges"
    var err: NSDictionary?
    let result = NSAppleScript(source: src)!.executeAndReturnError(&err) as NSAppleEventDescriptor?
    if result == nil {
        let code = (err?[NSAppleScript.errorNumber] as? Int) ?? -1
        throw NSError(domain: "pmset", code: code,                          // -128 = user cancelled
                      userInfo: [NSLocalizedDescriptionKey: (err?[NSAppleScript.errorMessage] as? String) ?? "failed"])
    }
}
func appleScriptString(_ s: String) -> String {
    "\"" + s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\""
}
// Repeating schedule: ["/usr/bin/pmset", "repeat", "wakeorpoweron", "MTWRF", "07:00:00", "sleep", "MTWRFSU", "23:00:00"]
// One-off:            ["/usr/bin/pmset", "schedule", "shutdown", "10/06/26 23:30:00", "Vitals"]

// --- 3. Root-free keep-awake ----------------------------------------------
final class KeepAwake {
    private var id = IOPMAssertionID(0)
    func start(keepDisplayOn: Bool) -> Bool {
        let type = (keepDisplayOn ? kIOPMAssertionTypePreventUserIdleDisplaySleep
                                  : kIOPMAssertionTypePreventUserIdleSystemSleep) as CFString
        return IOPMAssertionCreateWithName(type, IOPMAssertionLevel(kIOPMAssertionLevelOn),
                                           "Vitals: keep awake" as CFString, &id) == kIOReturnSuccess
    }
    func stop() { if id != 0 { IOPMAssertionRelease(id); id = 0 } }
}

// --- 4. Immediate actions -------------------------------------------------
func sleepNow() -> Bool {                                  // console user, no root
    let port = IOPMFindPowerManagement(kIOMainPortDefault)
    guard port != 0 else { return false }
    defer { IOServiceClose(port) }
    return IOPMSleepSystem(port) == kIOReturnSuccess
}
func displaySleepNow() { _ = try? runPmset(["displaysleepnow"]) }

func lockScreen() -> Bool {                                // private API; fall back on failure
    guard let h = dlopen("/System/Library/PrivateFrameworks/login.framework/login", RTLD_LAZY),
          let sym = dlsym(h, "SACLockScreenImmediate") else { return false }
    typealias Fn = @convention(c) () -> Int32
    _ = unsafeBitCast(sym, to: Fn.self)(); return true
}
func lockScreenFallback() {                                // needs Automation + Accessibility consent
    NSAppleScript(source: "tell application \"System Events\" to key code 12 using {control down, command down}")?
        .executeAndReturnError(nil)
}
func systemEvents(_ verb: String) {                        // "shut down" | "restart" | "sleep" | "log out"
    NSAppleScript(source: "tell application \"System Events\" to \(verb)")?.executeAndReturnError(nil)
}

// --- 5. Lid + power source ---------------------------------------------------
func lidClosed() -> Bool? {
    let svc = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"))
    guard svc != 0 else { return nil }
    defer { IOObjectRelease(svc) }
    return IORegistryEntryCreateCFProperty(svc, "AppleClamshellState" as CFString, kCFAllocatorDefault, 0)?
        .takeRetainedValue() as? Bool                     // nil → no lid (desktop)
}
func onACPower() -> Bool {
    guard let snap = IOPSCopyPowerSourcesInfo()?.takeRetainedValue(),
          let type = IOPSGetProvidingPowerSourceType(snap)?.takeUnretainedValue() else { return true }
    return (type as String) == kIOPMACPowerKey              // "AC Power"
}
// Lid change notifications
final class LidWatcher {
    private var port: IONotificationPortRef?; private var notifier: io_object_t = 0
    var onChange: ((Bool, Bool) -> Void)?                   // (closed, wouldSleep)
    func start() {
        port = IONotificationPortCreate(kIOMainPortDefault)
        CFRunLoopAddSource(CFRunLoopGetMain(), IONotificationPortGetRunLoopSource(port).takeUnretainedValue(), .commonModes)
        let svc = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"))
        let ctx = Unmanaged.passUnretained(self).toOpaque()
        IOServiceAddInterestNotification(port, svc, kIOGeneralInterest, { ctx, _, type, arg in
            guard type == UInt32(kIOPMMessageClamshellStateChange), let ctx else { return }
            let bits = UInt(bitPattern: arg)
            Unmanaged<LidWatcher>.fromOpaque(ctx).takeUnretainedValue().onChange?(bits & 1 != 0, bits & 2 != 0)
        }, ctx, &notifier)
        IOObjectRelease(svc)
    }
}
```
`kIOPMMessageClamshellStateChange` is a macro over `iokit_family_msg`; if the Swift importer does not expose it, compute `UInt32(0xe0000000 | (sub_iokit_powermanagement << 16) | 0x100)` — `sub_iokit_powermanagement` is `err_sub(13)` = `13 << 14` in `IOMessage.h` (verify in the SDK).

## Unverified
- Exact on-disk file written by `IOPMSetSystemPowerSetting` on 14/15 (CFPreferences `com.apple.PowerManagement`, AnyUser/CurrentHost → ByHost plist vs `/Library/Preferences/com.apple.PowerManagement.plist`). Use `pmset`, never edit the plist.
- Whether `disablesleep 1` reliably keeps every Apple-silicon model awake with the lid closed on battery on macOS 14/15 (conflicting third-party reports; Sleepless claims yes on macOS 26.3). Needs a device test.
- Whether `CGSession -suspend` still exists/works on macOS 14/15 (forum reports say broken since 11).
- Whether `IOPMSleepSystem` from a GUI app without root succeeds on 14/15 as the console user (header says yes; Hammerspoon relies on it).
- Whether the one-off `schedule` parser accepts a 4-digit year (`MM/dd/yy` formatter leniency) — pass 2-digit years.
- Whether sending `kAEShutDown` directly to loginwindow (QA1134) triggers a TCC Automation prompt on 14/15; the System Events route definitely does.
- Return type/value of `SACLockScreenImmediate` (community bindings use `Int32`; Hammerspoon ignores it).
- `pmset -g sched` showing repeating events under "Scheduled power events … by Repeating" on current macOS (seen only in older docs).

## Sources
- apple-oss-distributions/PowerManagement `PowerManagement-1846.0.25.0.1`: `pmset/pmset.m` (ARG_* 94-236, feature table 308-340, getters 592-640, sleepnow 695-712, displaysleepnow 982-1008, print 1041-1210, prevented-by 1216-1430, sched 1600-1830, powermode 4422-4500, repeat/schedule parse 4690-5075, scope flags 5276-5345, disablesleep 5813-5836), `pmset/pmset.1`, `caffeinate/caffeinate.8`, `caffeinate/caffeinate.c:53-61,211-214` — https://github.com/apple-oss-distributions/PowerManagement
- apple-oss-distributions/IOKitUser: `pwr_mgt.subproj/IOPMLib.h` (72-84, 237-263, 438-439, 541-575, 756-782, 816-875), `IOPMLibPrivate.h` (148, 257-303, 1563-1605, 1682-1684, 1760-1812, 1984-2023), `IOPMKeys.h:45-77`, `IOPMEnergyPrefs.c:235-238,1604-1660`, `ps.subproj/IOPowerSources.h:195-362`, `IOPSKeys.h:311-768` — https://github.com/apple-oss-distributions/IOKitUser
- apple-oss-distributions/xnu `xnu-12377.1.9`: `iokit/IOKit/pwr_mgt/IOPM.h:211,221,430-444,520-535`, `iokit/IOKit/pwr_mgt/IOPMPrivate.h:132,148,172`, `iokit/Kernel/IOPMrootDomain.cpp:1985,2055-2058,4461-4508,7661-7690,7802-7812,8386-8388,8752-8900` — https://github.com/apple-oss-distributions/xnu
- Apple docs (JSON of developer.apple.com): `kIOPMAssertionTypePreventUserIdleSystemSleep`, `kIOPMAssertionTypePreventUserIdleDisplaySleep` (macOS 10.7+), `AuthorizationExecuteWithPrivileges` (10.1–10.7 deprecated), `SMAppService`, `SMAppService.daemon(plistName:)`, `register()`, `SMAppService.Status`, `SMJobBless` (deprecated 13.0), `NSAppleScript.executeAndReturnError(_:)` — https://developer.apple.com/documentation/
- TN2065 "do shell script in AppleScript" — https://developer.apple.com/library/archive/technotes/tn2065/_index.html
- QA1134 "Programmatically causing restart, shutdown and/or logout" — https://developer.apple.com/library/mac/qa/qa1134/_index.html
- Quinn (DTS), "BSD Privilege Escalation on macOS" — https://developer.apple.com/forums/thread/708765 ; "How do I run AppleScript safely inside my Swift app" — https://developer.apple.com/forums/thread/730884 ; lock screen on Big Sur — https://developer.apple.com/forums/thread/682918 ; SMAppService daemon UX — https://developer.apple.com/forums/thread/721919
- Hammerspoon `extensions/caffeinate/libcaffeinate.m` (SACLockScreenImmediate, IOPMSleepSystem, assertion types) — https://github.com/Hammerspoon/hammerspoon/blob/master/extensions/caffeinate/libcaffeinate.m
- Swift `SACLockScreenImmediate` users: Raysance/EdgeSwipe `Sources/EdgeSwipe/ActionRunner.swift`, ZingerLittleBee/AnyDoor `Sources/AnyDoor/Services/Providers/LockScreenProvider.swift`, mactools-app/MacTools `Plugins/LockScreen/Sources/LockScreenPlugin.swift`; `AppleClamshellState` users: Lakr233/Sentry `Sentry/DeviceCheck.swift`, Beingpax/VoiceInk `…/ClamshellStateMonitor.swift`, paniclock/paniclock `PanicLock/PowerMonitor.swift` (GitHub code search)
- Sleepless (`pmset disablesleep` + sudoers) — https://github.com/Aboudjem/Sleepless (README, `grant.sh`); KeepingYouAwake README (lid-closed limitation) — https://github.com/newmarcel/KeepingYouAwake; ForceLock (`pmset displaysleepnow`) — https://github.com/guomengtao/ForceLock
- Clamshell-mode requirements (Apple support article "Use your Mac notebook computer in closed-display mode", via search excerpts; page itself not reachable from this host): https://support.apple.com/en-us/102320
- Keystroke/TCC errors on Sonoma — https://macscripter.net/t/sonoma-wont-allow-my-script-to-use-gui-scripting/76591 , https://communities.apple.com/thread/255224976
