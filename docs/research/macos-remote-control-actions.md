# macOS remote-control actions for "Reach" (beyond raw input)

Swift / AppKit, swiftc-built `.app` bundle, macOS 14+ target. Verified 2026-10 against Apple docs (JSON doc endpoint), SDK headers and the named open-source implementations. Items that could not be verified are listed at the end under **Unverified**.

## Summary

- Everything here runs as the logged-in user; nothing needs root (only `shutdown`/`reboot` CLIs and `pmset` *settings* do, so avoid them).
- Four TCC grants cover all actions: **Accessibility** (AX calls + posting CGEvents), **Screen Recording** (window titles, screenshots), **Automation** (AppleScript to System Events/Spotify/Music; needs `NSAppleEventsUsageDescription`), **Notifications** (bundle + code signature required).
- Media keys = `NSEvent.otherEvent(.systemDefined, subtype 8, data1 = key<<16 | 0xA00/0xB00)` posted as a CGEvent; brightness = private `DisplayServicesSetBrightness`; absolute volume = AppleScript `set volume output volume N` or CoreAudio `VirtualMainVolume`.
- Lock screen: private `SACLockScreenImmediate` (login.framework) or Ctrl+Cmd+Q via CGEvent; `CGSession -suspend` is reported broken since Big Sur; `pmset displaysleepnow` only locks if "require password immediately" is set.
- Screen preview: `SCScreenshotManager.captureImage` (14.0) / `SCStream` at `minimumFrameInterval = 1/5`; `CGDisplayCreateImage` is deprecated 14.0 and marked *obsoleted* in the macOS 15 SDK. Map preview pixels back via `SCDisplay.frame` (== `CGDisplayBounds`, top-left-origin global points).

## 1. Installed & running apps

| Need | API (verified) | Notes |
|---|---|---|
| Enumerate | `FileManager.contentsOfDirectory(at:includingPropertiesForKeys:options:[.skipsHiddenFiles])` over `/Applications`, `/Applications/Utilities`, `~/Applications`, `/System/Applications`, `/System/Applications/Utilities`; keep `pathExtension == "app"` | Recurse one level for vendor folders (`/Applications/Adobe …`). |
| Metadata | `Bundle(url:)?.bundleIdentifier`, `infoDictionary["CFBundleDisplayName"] ?? ["CFBundleName"]` | Localized name: `FileManager.default.displayName(atPath:)` (returns localized, extension-stripped name). |
| Icon | `NSWorkspace.shared.icon(forFile:) -> NSImage` | **Initial size is 32×32** (doc). Draw into a 64×64 `NSBitmapImageRep`, then `.representation(using: .png, properties: [:])`. Thread-safe. |
| Running | `NSWorkspace.shared.runningApplications` filter `activationPolicy == .regular` | KVO-observable; `localizedName`, `bundleIdentifier`, `processIdentifier`, `isActive`, `isHidden`. |
| Launch | `NSWorkspace.shared.openApplication(at: URL, configuration: NSWorkspace.OpenConfiguration) async throws -> NSRunningApplication` (10.15) | `urlForApplication(withBundleIdentifier:)` to resolve; `configuration.activates` defaults to `true`. |
| Focus | `NSRunningApplication.activate(options:) -> Bool` (10.6, still available) ; `activate(from: NSRunningApplication, options:)` (14.0) | `.activateIgnoringOtherApps` deprecated 14.0 ("Use NSApplication activate()"). On 14+ activation is a *request*; see pitfalls. |
| Quit | `terminate() -> Bool` (sends Quit AppleEvent; may return before exit; observe `isTerminated`), `forceTerminate()` | Both return `false` from a sandboxed app — do not sandbox Reach. |

macOS 14 cooperative activation (AppKit release notes): "app activation is driven by user intent … treated as a request and not always guaranteed"; `activate(ignoringOtherApps:)` / `.ignoringOtherApps` deprecated; handoff is guaranteed only when the *currently active* app calls `NSApp.yieldActivation(to:)` first. Forum reports on Sonoma: `activate(options: [.activateIgnoringOtherApps])` from a non-active process returns `false` and bounces the Dock icon. Practical ladder for a background agent: (1) `app.unhide()` + `activate(options: [.activateAllWindows])`; (2) if `false`/no effect, `NSWorkspace.openApplication(at: app.bundleURL!, configuration:)` on the already-running app (LaunchServices path; commonly honored — unverified); (3) AppleScript `tell application id "<bid>" to activate` (Automation permission); (4) private `_SLPSSetFrontProcessWithOptions` (yabai, see §2).

## 2. Windows

`CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) -> CFArray?` of dictionaries (10.5). Keys: `kCGWindowNumber` (CGWindowID), `kCGWindowOwnerPID`, `kCGWindowOwnerName`, `kCGWindowName` ("few applications set the Quartz window name" — it is the title for AppKit apps), `kCGWindowBounds` (CFDictionary → `CGRect(dictionaryRepresentation:)`; "screen space, origin upper-left of main display"), `kCGWindowLayer` (0 = normal windows), `kCGWindowAlpha`, `kCGWindowIsOnscreen`. Filter `layer == 0 && alpha > 0`.

Permission: without **Screen Recording**, `kCGWindowName` is absent (10.15+) and on recent releases `kCGWindowOwnerName` is `nil` for other apps' windows. Apple DTS (forum 839069): this gating is *undocumented*; detect with `CGPreflightScreenCaptureAccess()` and prompt with `CGRequestScreenCaptureAccess()` (both 10.15+). Alternative enumerator with the same permission: `SCShareableContent.windows` (`SCWindow.windowID/.title/.frame/.owningApplication`).

Focus a window (Accessibility API, needs **Accessibility** trust):

| Symbol | Value / signature |
|---|---|
| `AXIsProcessTrustedWithOptions(_ options: CFDictionary?) -> Bool`; `kAXTrustedCheckOptionPrompt` (10.9) | pass `[kAXTrustedCheckOptionPrompt.takeUnretainedValue(): true]` to show the system prompt once |
| `AXUIElementCreateApplication(_ pid: pid_t) -> AXUIElement` | |
| `AXUIElementCopyAttributeValue(_:_:_:) -> AXError` with `kAXWindowsAttribute` ("AXWindows") | → `[AXUIElement]` |
| `kAXTitleAttribute`, `kAXPositionAttribute`, `kAXSizeAttribute` | read via `AXValueGetValue(_, .cgPoint/.cgSize, &out)` to match CG bounds |
| `kAXMinimizedAttribute` ("AXMinimized") | set `kCFBooleanFalse` to de-minimize (yabai, AltTab do this) |
| `kAXMainAttribute` ("AXMain") | set `kCFBooleanTrue` ("window can be main without keyboard focus") |
| `AXUIElementPerformAction(_, kAXRaiseAction)` ("AXRaise") | raise within the app; then activate the app |
| private `_AXUIElementGetWindow(AXUIElementRef, uint32_t *wid) -> AXError` | AX element → CGWindowID (yabai `src/misc/extern.h`); avoids title matching |
| private `_SLPSSetFrontProcessWithOptions(ProcessSerialNumber*, uint32_t wid, uint32_t mode)` + `SLPSPostEventRecordTo` | yabai's `window_manager_focus_window_with_raise`: `_SLPSSetFrontProcessWithOptions(psn, wid, kCPSUserGenerated=0x200)` → synthesized key-window event → `kAXRaiseAction`. Private; last resort. |

Order used by Rectangle (public only): de-minimize → `kAXMainAttribute = true` → `kAXRaiseAction` → `NSRunningApplication.activate()`.
Errors: `.apiDisabled` (-25211) = not trusted; `.cannotComplete` (-25204) = app not responding to AX.

## 3. Media keys, volume, brightness

**Constants** (`IOKit/hidsystem/ev_keymap.h`, `IOLLEvent.h`, macOS 11.3 SDK):

| Name | Value | Name | Value |
|---|---|---|---|
| `NX_KEYTYPE_SOUND_UP` | 0 | `NX_KEYTYPE_PLAY` | 16 |
| `NX_KEYTYPE_SOUND_DOWN` | 1 | `NX_KEYTYPE_NEXT` | 17 |
| `NX_KEYTYPE_BRIGHTNESS_UP` | 2 | `NX_KEYTYPE_PREVIOUS` | 18 |
| `NX_KEYTYPE_BRIGHTNESS_DOWN` | 3 | `NX_KEYTYPE_FAST` | 19 |
| `NX_KEYTYPE_MUTE` | 7 | `NX_KEYTYPE_REWIND` | 20 |
| `NX_KEYTYPE_EJECT` | 14 | `NX_KEYTYPE_ILLUMINATION_UP/DOWN/TOGGLE` | 21/22/23 |
| `NX_SYSDEFINED` (event type) | 14 | `NX_SUBTYPE_AUX_CONTROL_BUTTONS` | 8 |
| `NX_KEYDOWN` / `NX_KEYUP` | 10 (0x0A) / 11 (0x0B) | `NX_NUMSPECIALKEYS` | 24 |

Event construction (verified against Hammerspoon `libeventtap_event.m` `newSystemKeyEvent`):
`NSEvent.otherEvent(with: .systemDefined, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil, subtype: 8, data1: (key << 16) | ((down ? 0x0A : 0x0B) << 8), data2: -1)` then `event.cgEvent?.post(tap: .cghidEventTap)`; send a down then an up. Hammerspoon passes `NX_KEYDOWN/NX_KEYUP` as `modifierFlags` and posts to `kCGSessionEventTap` ("kCGHIDEventTap … doesn't seem to be any different"); the common Swift snippet uses `0xA00/0xB00` flags and the HID tap — both work. **Posting any CGEvent requires the Accessibility grant** (`kTCCServicePostEvent`, surfaced under Privacy & Security ▸ Accessibility; enforced since 10.14).

Routing: play/pause/next/prev go through the system's media-key path to the current "Now Playing" app. Spotify and Music register there; Chrome ≥ 73 handles hardware media keys (works in background, flag `#hardware-media-key-handling`); Safari exposes HTML5 media (YouTube) to Now Playing. The synthetic event takes the same path as a physical key (this is what Hammerspoon `newSystemKeyEvent` relies on) — but see Unverified; keep an AppleScript fallback: `tell application "Spotify" to playpause` / `tell application "Music" to playpause` (Automation permission per target app).

**Absolute volume** — two routes:
- AppleScript (StandardAdditions, executes in-process via `NSAppleScript`): `set volume output volume 40` (0–100), `set volume output muted true`, read with `output volume of (get volume settings)` / `output muted of (get volume settings)`. No target app → no Automation prompt expected (unverified, but widely used).
- CoreAudio: device = `kAudioHardwarePropertyDefaultOutputDevice` on `kAudioObjectSystemObject`; selector `kAudioHardwareServiceDeviceProperty_VirtualMainVolume` (AudioToolbox; `…VirtualMasterVolume` is the deprecated alias), scope `kAudioDevicePropertyScopeOutput`, element `kAudioObjectPropertyElementMain`, `Float32` 0.0–1.0 via `AudioObjectSetPropertyData`; mute = `kAudioDevicePropertyMute` (`UInt32`). No permission. Not every device exposes a virtual main volume (AirPlay/aggregate) → fall back to AppleScript.

**Brightness** (built-in panel; external displays need DDC, out of scope):

| Route | Signature (nriley/brightness `brightness.c`) | Status |
|---|---|---|
| `DisplayServices.framework` (private, `/System/Library/PrivateFrameworks/DisplayServices.framework/DisplayServices`) | `int DisplayServicesGetBrightness(CGDirectDisplayID, float *)`, `int DisplayServicesSetBrightness(CGDirectDisplayID, float)`, `bool DisplayServicesCanChangeBrightness(CGDirectDisplayID)`, `void DisplayServicesBrightnessChanged(CGDirectDisplayID, double)` | "necessary on Apple Silicon/macOS 11"; used by MonitorControl (`AppleDisplay.swift`) and Hammerspoon; Homebrew `brightness` lists Sonoma/Apple Silicon support. Preferred. |
| `CoreDisplay.framework` | `double CoreDisplay_Display_GetUserBrightness(CGDirectDisplayID)`, `void CoreDisplay_Display_SetUserBrightness(CGDirectDisplayID, double)` | Second choice in nriley's chain; does not notify the UI slider unless followed by `DisplayServicesBrightnessChanged`. |
| IOKit `IODisplaySetFloatParameter(service, 0, kIODisplayBrightnessKey, v)` on `IODisplayConnect` | public | "does not drive the built-in panel on an Apple Silicon Mac" (go-macos/brightness); Intel only. |
| Key events `NX_KEYTYPE_BRIGHTNESS_UP/DOWN` | §3 event route | Relative steps only; public path; needs Accessibility. Use when DisplayServices is unavailable. |

Load with `dlopen`/`dlsym` (weak linking is impossible without a tbd); value range 0.0–1.0; display ID from `CGMainDisplayID()` or `CGGetActiveDisplayList`.

## 4. Power / session

| Action | Mechanism | Root? | Works on 14/15? |
|---|---|---|---|
| Lock screen (preferred) | `SACLockScreenImmediate()` from `/System/Library/PrivateFrameworks/login.framework/Versions/A/login` via `dlopen`+`dlsym` (Hammerspoon `libcaffeinate.m` uses `CFBundleGetFunctionPointerForName`; Chromium `login_util.cc` uses `dlopen`). Same call Apple's Keychain menu "Lock Screen" used. | no | Private; shipped by Hammerspoon today; no 14/15 breakage reports found (unverified) |
| Lock screen (public) | Post Ctrl+Cmd+Q: `CGEvent(keyboardEventSource:virtualKey: 0x0C, keyDown:)` with `flags = [.maskControl, .maskCommand]`, or `osascript -e 'tell application "System Events" to keystroke "q" using {control down, command down}'` | no | Yes if the user kept the default shortcut (10.13+). CGEvent route needs Accessibility; System Events route needs Automation + Accessibility. |
| Lock via login window | `/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession -suspend` | no | Reported "no longer works" on Big Sur (forum 682918); Chromium's curtain mode switched to private `SACSwitchToLoginWindow`. Don't rely on it. |
| Display sleep | `pmset displaysleepnow` (man: "causes display to go to sleep immediately") | no ("must be run as root in order to modify any settings" — this is not a setting) | Yes. Locks only if Lock Screen ▸ "Require password after screen saver begins or display is turned off" = Immediately. PanicLock (14+) relies on this. |
| System sleep | `pmset sleepnow`, or IOKit `IOPMSleepSystem(IOPMFindPowerManagement(MACH_PORT_NULL))` (Hammerspoon `systemSleep`), or `tell application "System Events" to sleep` | no | Yes |
| Shut down / restart / log out | `tell application "System Events" to shut down` / `restart` / `log out` (optional `with state saving preference`) | no | Needs Automation for System Events. Goes through loginwindow's normal flow: apps are asked to quit; an unsaved document or a confirmation sheet can block it (sheet behaviour on 14/15 unverified). "Really" variants that skip confirmation: `tell application "loginwindow" to «event aevtrsdn»` (shut down), `«event aevtrrst»` (restart), `«event aevtrlgo»` (log out) — undocumented but widely used in MDM scripts. |
| Lock state | `CGSessionCopyCurrentDictionary()?["CGSSessionScreenIsLocked"]`; `DistributedNotificationCenter` names `com.apple.screenIsLocked` / `com.apple.screenIsUnlocked` | no | Key names from Hammerspoon `sessionProperties` (unverified on 15) |

Avoid `shutdown -h now`, `reboot`, `pmset <setting>` — all need root.

## 5. URLs, files, shell

- `NSWorkspace.shared.open(_ url: URL) -> Bool` (thread-safe), `open(_ urls: [URL], withApplicationAt: URL, configuration:) async throws -> NSRunningApplication` (10.15). Allowlist schemes (`http`, `https`, `mailto`, `tel`?); treat `file:` as code execution (opening a `.command`, `.app`, `.pkg`, `.scpt` runs it).
- Shell: `Process` with `executableURL = /bin/zsh`, `arguments = ["-lc", cmd]`, `Pipe` for stdout+stderr, timeout via `DispatchWorkItem { process.terminate() }`, cap output (e.g. 1 MiB), run off the main thread, `qualityOfService = .utility`. Safety: off by default; per-pairing opt-in; log every command; never prepend `sudo`; child processes inherit Reach's TCC identity (prompts/grants attribute to Reach, so a shell command can trigger Automation/Files prompts); strip `DYLD_*` env; no PTY (interactive prompts hang → timeout).

## 6. Typing arbitrary text

- `CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: true/false)` + `keyboardSetUnicodeString(stringLength: Int, unicodeString: UnsafePointer<UniChar>?)` (10.4). Post down **and** up with the same string to `.cghidEventTap`. Doc: "application frameworks may ignore the Unicode string … and do their own translation based on the virtual keycode" — some Electron/Java apps need the paste route.
- **Limit: 20 UTF-16 units per event**; longer strings are truncated (enigo `macos_impl.rs`: "CGEventKeyboardSetUnicodeString … truncates strings down to 20 characters"; Qt forum). Chunk at Character boundaries so surrogate pairs/emoji are never split. Hammerspoon sends one character per event (`CGEventKeyboardSetUnicodeString(e, 1, &c)`).
- **Newline/tab:** a chunk *starting* with `\n`, `\r` or `\t` "silently fails" (enigo). Don't embed them: split on them and send real keys — `kVK_Return 0x24`, `kVK_Tab 0x30` (Carbon `Events.h`). Other useful codes: `kVK_ANSI_V 0x09`, `kVK_ANSI_Q 0x0C`, `kVK_Space 0x31`, `kVK_Delete 0x33`, `kVK_Escape 0x35`, `kVK_Command 0x37`, arrows `0x7B/0x7C/0x7E/0x7D`.
- Paste route for long text: `NSPasteboard.general.clearContents()`, `setString(_:forType: .string)`, post Cmd+V (`0x09` with `.maskCommand`), restore the previous contents after ~300 ms (apps read the pasteboard on key-down; Terminal on key-up). Check `changeCount` before restoring so a user copy in between isn't clobbered. Pasting into password fields and secure-input contexts is typically refused by the app; typing works.
- Needs Accessibility (posting events). Insert ~5–10 ms between chunks for slow apps.

## 7. Screen preview (~5 fps JPEG)

| API | Availability | Notes |
|---|---|---|
| `SCShareableContent.excludingDesktopWindows(_:onScreenWindowsOnly:) async throws` | 12.3 | First call triggers the Screen Recording prompt; throws `SCStreamError.Code.userDeclined` when denied. XPC round-trip — cache `displays`, refresh on `CGDisplayRegisterReconfigurationCallback`. |
| `SCDisplay.displayID: CGDirectDisplayID`, `.frame: CGRect`, `.width/.height` (points) | 12.3 | `frame` is in the same global, top-left-origin point space as `CGDisplayBounds(_:)`. |
| `SCContentFilter(display:excludingWindows: [])` | 12.3 | `contentRect`, `pointPixelScale` (14.0) give the filter's point rect and scale. |
| `SCStreamConfiguration.width/.height` (pixels), `.showsCursor`, `.pixelFormat` (`kCVPixelFormatType_32BGRA`), `.captureResolution = .nominal` (14.0; `.automatic/.best/.nominal`), `.minimumFrameInterval: CMTime` (reciprocal of max fps), `.scalesToFit` (window capture only) | 12.3 / 14.0 | Set `width/height` to the downscaled size; SCK scales for you. |
| `SCScreenshotManager.captureImage(contentFilter:configuration:) async throws -> CGImage` | 14.0 | One-shot. `captureImage(in: CGRect)` (15.2) takes display-space points. |
| `SCStream` + `SCStreamOutput.stream(_:didOutputSampleBuffer:of:)` with `minimumFrameInterval = CMTime(value: 1, timescale: 5)` | 12.3 | Cheaper than polling screenshots; only delivers frames when content changes (`SCStreamFrameInfo.status`). |
| `CGDisplayCreateImage(_:)`, `CGWindowListCreateImage` | deprecated 14.0; macOS 15 SDK: "unavailable: obsoleted in macOS 15.0 - Please use ScreenCaptureKit instead" | Compiles only while deployment target < 15 (warning). Treat as gone. |

Encode: `CGImageDestinationCreateWithData(data, UTType.jpeg.identifier as CFString, 1, nil)` + `[kCGImageDestinationLossyCompressionQuality: 0.5]` (0.0–1.0), or `NSBitmapImageRep(cgImage:).representation(using: .jpeg, properties: [.compressionFactor: 0.5])`. A 1440×900 BGRA frame → JPEG q0.5 at 5 fps is ~1–2 MB/s; downscale to ≤ 1280 px wide for Wi-Fi.

Coordinate mapping (multi-display): for a tap at preview pixel `(px, py)` in an image of `(W, H)` captured from `SCDisplay d`:
`global = CGPoint(x: d.frame.minX + px / W * d.frame.width, y: d.frame.minY + py / H * d.frame.height)` — feed directly to `CGEvent(mouseEventSource:mouseType:mouseCursorPosition: global, mouseButton:)`. Never use `NSScreen.frame` here (bottom-left origin). Retina: the image's pixel size ≠ points; the ratio above cancels that out. Enumerate displays with `CGGetActiveDisplayList` (first entry = main) and send one stream per selected display.

Sequoia (15.0+): apps holding Screen Recording get a recurring "<App> is requesting to bypass the system private window picker" / re-approval prompt (weekly in betas, monthly from 15.0 beta 6, "Allow For One Month"); applies to ScreenCaptureKit apps too; 15.1 changed `replayd` bookkeeping. `SCContentSharingPicker` (system picker) is the Apple-sanctioned way around it but is interactive — unsuitable for unattended remote control. Grants to Screen Recording take effect after the app relaunches.

## 8. User notifications (pairing code)

- `UNUserNotificationCenter.current()` (10.14); `requestAuthorization(options: [.alert, .sound]) async throws -> Bool`; `UNMutableNotificationContent` (`title`, `body`, `interruptionLevel = .timeSensitive` needs entitlement — skip); `UNNotificationRequest(identifier:content:trigger: nil)`; `center.add(_:)`.
- Hard requirements: the process must be a **bundle** (`Reach.app/Contents/MacOS/Reach` + `Info.plist` with `CFBundleIdentifier`) or it throws `NSInternalInconsistencyException: bundleProxyForCurrentProcess is nil` (forum 724249/679326), and the bundle must be **code-signed** — ad-hoc `codesign -s - Reach.app` is reported sufficient; unsigned → `UNErrorDomain code 1 "Notifications are not allowed for this application"`. `LSUIElement = true` apps are fine.
- Implement `UNUserNotificationCenterDelegate.userNotificationCenter(_:willPresent:withCompletionHandler:)` returning `[.banner, .sound]` or notifications are suppressed while Reach is the active app.
- Fallback that needs nothing: an `NSAlert`/floating `NSPanel` with the code (use `.floating` level + `canJoinAllSpaces`).

## Permissions & detection (summary)

| Grant (TCC service) | Needed for | Detect | Request |
|---|---|---|---|
| Accessibility (`kTCCServiceAccessibility`, `kTCCServicePostEvent`) | AX window focus, every `CGEvent.post` (media keys, typing, Ctrl+Cmd+Q, mouse) | `AXIsProcessTrusted()` | `AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt: true])` (prompts once; later toggles in Settings) |
| Screen Recording (`kTCCServiceScreenCapture`) | window titles/owners, SCK, screenshots | `CGPreflightScreenCaptureAccess()` | `CGRequestScreenCaptureAccess()` or first SCK call; relaunch after grant |
| Automation (`kTCCServiceAppleEvents`, per target app) | AppleScript to System Events / Spotify / Music / loginwindow | `AEDeterminePermissionToAutomateTarget(&addrDesc, typeWildCard, typeWildCard, false)` (10.14) → `errAEEventNotPermitted` (-1743) | same call with `askUserIfNeeded = true`, or just run the script; **`NSAppleEventsUsageDescription` is required in Info.plist** or the request is denied silently |
| Notifications | pairing code banners | `getNotificationSettings()` | `requestAuthorization` |
| Input Monitoring | *not needed* (only event taps need it) | — | — |

TCC keys on the bundle's signing identity: rebuilding an ad-hoc-signed app changes its cdhash and can invalidate grants — sign with a stable identity (or at least keep bundle ID + path constant) and expect to re-grant during development. `osascript`/child processes launched by Reach are attributed to Reach for TCC.

## Pitfalls (short list)

1. `icon(forFile:)` is 32 px until you render it — draw at 64 px explicitly.
2. `activate(options:)` on 14+ may return `false` with no visible effect when Reach isn't active; always follow with the AX raise and the LaunchServices/AppleScript fallbacks.
3. `kCGWindowName` missing ≠ no title: it's the Screen Recording gate. Don't use it as a permission probe (DTS: undocumented).
4. `CGEvent.post` silently drops events without Accessibility; check `AXIsProcessTrusted()` before every action batch and surface the state in the UI.
5. Unicode typing: 20 UTF-16 units per event; never start a chunk with `\n`/`\t`; some apps ignore the Unicode payload → paste fallback.
6. Media/brightness/volume keys need both key-down and key-up events, else the OSD HUD sticks.
7. `pmset displaysleepnow` ≠ lock unless the password-delay setting is "Immediately"; `CGSession -suspend` unreliable since 11.
8. System Events verbs need Automation **and** `NSAppleEventsUsageDescription`; missing key = instant `-1743` with no prompt.
9. SCK: don't re-fetch `SCShareableContent` per frame; it is an XPC call. Use one `SCStream` per display at 5 fps.
10. Sequoia re-approval prompts will interrupt unattended sessions monthly; tell the user up front.
11. Sandboxing disables `terminate()`/`forceTerminate()` and AppleEvents — ship Reach unsandboxed, Hardened Runtime is fine (add `com.apple.security.automation.apple-events` entitlement when hardened).
12. All private symbols (`SACLockScreenImmediate`, `DisplayServices*`, `_AXUIElementGetWindow`, `_SLPSSetFrontProcessWithOptions`) must be resolved with `dlsym` and guarded by nil checks; never a Mac App Store build.

## Minimal Swift sketches

```swift
// 1. Installed apps + 64px PNG icon
import AppKit
struct AppInfo { let name: String; let bundleID: String?; let path: String; let iconPNG: Data? }
func iconPNG(path: String, px: Int = 64) -> Data? {
    let img = NSWorkspace.shared.icon(forFile: path)               // 32x32 until drawn
    guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return nil }
    rep.size = NSSize(width: px, height: px)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    img.draw(in: NSRect(x: 0, y: 0, width: px, height: px), from: .zero, operation: .copy, fraction: 1)
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])
}
func installedApps() -> [AppInfo] {
    let dirs = ["/Applications", "/Applications/Utilities", "/System/Applications",
                "/System/Applications/Utilities", NSHomeDirectory() + "/Applications"]
    return dirs.flatMap { dir -> [AppInfo] in
        let urls = (try? FileManager.default.contentsOfDirectory(at: URL(fileURLWithPath: dir),
                    includingPropertiesForKeys: nil, options: [.skipsHiddenFiles])) ?? []
        return urls.filter { $0.pathExtension == "app" }.map { u in
            let b = Bundle(url: u); let info = b?.infoDictionary
            let name = (info?["CFBundleDisplayName"] as? String) ?? (info?["CFBundleName"] as? String)
                       ?? FileManager.default.displayName(atPath: u.path)
            return AppInfo(name: name, bundleID: b?.bundleIdentifier, path: u.path, iconPNG: iconPNG(path: u.path))
        }
    }
}
func runningApps() -> [NSRunningApplication] {
    NSWorkspace.shared.runningApplications.filter { $0.activationPolicy == .regular }
}
```

```swift
// 1b. Launch / focus / quit
func launch(bundleID: String) async throws -> NSRunningApplication {
    guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID) else { throw CocoaError(.fileNoSuchFile) }
    let cfg = NSWorkspace.OpenConfiguration(); cfg.activates = true
    return try await NSWorkspace.shared.openApplication(at: url, configuration: cfg)
}
func focusApp(_ app: NSRunningApplication) async {
    if app.isHidden { app.unhide() }
    if app.activate(options: [.activateAllWindows]) { return }          // 14+: a request; may be refused
    if let url = app.bundleURL {                                         // LaunchServices re-activation
        let cfg = NSWorkspace.OpenConfiguration(); cfg.activates = true
        _ = try? await NSWorkspace.shared.openApplication(at: url, configuration: cfg)
    }
}
func quit(_ app: NSRunningApplication, force: Bool = false) -> Bool { force ? app.forceTerminate() : app.terminate() }
```

```swift
// 2. Windows: list (Screen Recording for titles) + focus (Accessibility)
import ApplicationServices
struct WinInfo { let id: CGWindowID; let pid: pid_t; let owner: String; let title: String?; let bounds: CGRect }
func onScreenWindows() -> [WinInfo] {
    let arr = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    return arr.compactMap { d in
        guard (d[kCGWindowLayer as String] as? Int) == 0,
              let id = d[kCGWindowNumber as String] as? CGWindowID,
              let pid = d[kCGWindowOwnerPID as String] as? pid_t,
              let bd = d[kCGWindowBounds as String] as? NSDictionary,
              let rect = CGRect(dictionaryRepresentation: bd as CFDictionary) else { return nil }
        return WinInfo(id: id, pid: pid, owner: d[kCGWindowOwnerName as String] as? String ?? "",
                       title: d[kCGWindowName as String] as? String, bounds: rect)   // title nil without Screen Recording
    }
}
@_silgen_name("_AXUIElementGetWindow")   // private (yabai, AltTab); guard with dlsym in production
func _AXUIElementGetWindow(_ el: AXUIElement, _ wid: UnsafeMutablePointer<CGWindowID>) -> AXError
func ensureAccessibility(prompt: Bool) -> Bool {
    AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: prompt] as CFDictionary)
}
func focusWindow(pid: pid_t, windowID: CGWindowID) -> Bool {
    let app = AXUIElementCreateApplication(pid); var v: CFTypeRef?
    guard AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &v) == .success,
          let wins = v as? [AXUIElement] else { return false }
    for w in wins {
        var wid: CGWindowID = 0
        guard _AXUIElementGetWindow(w, &wid) == .success, wid == windowID else { continue }
        AXUIElementSetAttributeValue(w, kAXMinimizedAttribute as CFString, kCFBooleanFalse)
        AXUIElementSetAttributeValue(w, kAXMainAttribute as CFString, kCFBooleanTrue)
        AXUIElementPerformAction(w, kAXRaiseAction as CFString)
        NSRunningApplication(processIdentifier: pid)?.activate(options: [])
        return true
    }
    return false
}
```

```swift
// 3. Media / brightness / volume keys (needs Accessibility)
enum NXKey: Int32 { case soundUp = 0, soundDown = 1, brightnessUp = 2, brightnessDown = 3, mute = 7,
                    play = 16, next = 17, previous = 18 }
func postMediaKey(_ key: NXKey) {
    for down in [true, false] {
        let flags = NSEvent.ModifierFlags(rawValue: down ? 0xA00 : 0xB00)
        let data1 = Int((key.rawValue << 16) | ((down ? 0x0A : 0x0B) << 8))
        NSEvent.otherEvent(with: .systemDefined, location: .zero, modifierFlags: flags, timestamp: 0,
                           windowNumber: 0, context: nil, subtype: 8, data1: data1, data2: -1)?
            .cgEvent?.post(tap: .cghidEventTap)
    }
}
// 3b. Absolute volume via AppleScript (no Automation prompt: no target app) and CoreAudio
func runScript(_ src: String) -> NSAppleEventDescriptor? {
    var err: NSDictionary?; let r = NSAppleScript(source: src)?.executeAndReturnError(&err); return err == nil ? r : nil
}
func setVolumeAS(_ pct: Int) { _ = runScript("set volume output volume \(max(0, min(100, pct)))") }
func getVolumeAS() -> Int? { runScript("output volume of (get volume settings)").map { Int($0.int32Value) } }
import CoreAudio, AudioToolbox
func setVolumeCA(_ v: Float32) -> Bool {
    var a = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDefaultOutputDevice,
                                       mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
    var dev = AudioDeviceID(0); var sz = UInt32(MemoryLayout<AudioDeviceID>.size)
    guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &a, 0, nil, &sz, &dev) == noErr else { return false }
    a = AudioObjectPropertyAddress(mSelector: kAudioHardwareServiceDeviceProperty_VirtualMainVolume,
                                   mScope: kAudioDevicePropertyScopeOutput, mElement: kAudioObjectPropertyElementMain)
    var val = max(0, min(1, v))
    return AudioObjectSetPropertyData(dev, &a, 0, nil, UInt32(MemoryLayout<Float32>.size), &val) == noErr
}
// 3c. Brightness via private DisplayServices (Apple Silicon OK); fall back to NXKey.brightnessUp/Down
typealias DSSet = @convention(c) (CGDirectDisplayID, Float) -> Int32
typealias DSGet = @convention(c) (CGDirectDisplayID, UnsafeMutablePointer<Float>) -> Int32
let dsHandle = dlopen("/System/Library/PrivateFrameworks/DisplayServices.framework/DisplayServices", RTLD_LAZY)
func setBrightness(_ v: Float, display: CGDirectDisplayID = CGMainDisplayID()) -> Bool {
    guard let h = dsHandle, let s = dlsym(h, "DisplayServicesSetBrightness") else { return false }
    return unsafeBitCast(s, to: DSSet.self)(display, max(0, min(1, v))) == 0
}
func getBrightness(display: CGDirectDisplayID = CGMainDisplayID()) -> Float? {
    guard let h = dsHandle, let s = dlsym(h, "DisplayServicesGetBrightness") else { return nil }
    var b: Float = 0; return unsafeBitCast(s, to: DSGet.self)(display, &b) == 0 ? b : nil
}
```

```swift
// 4. Lock / sleep / power
func postKey(_ vk: CGKeyCode, flags: CGEventFlags = []) {
    let src = CGEventSource(stateID: .hidSystemState)
    for down in [true, false] {
        guard let e = CGEvent(keyboardEventSource: src, virtualKey: vk, keyDown: down) else { return }
        e.flags = flags; e.post(tap: .cghidEventTap)
    }
}
func lockScreen() {
    if let h = dlopen("/System/Library/PrivateFrameworks/login.framework/Versions/A/login", RTLD_LAZY),
       let s = dlsym(h, "SACLockScreenImmediate") {
        unsafeBitCast(s, to: (@convention(c) () -> Void).self)()           // private
    } else { postKey(0x0C, flags: [.maskControl, .maskCommand]) }          // Ctrl+Cmd+Q (Accessibility)
}
func pmset(_ verb: String) { let p = Process(); p.executableURL = URL(fileURLWithPath: "/usr/bin/pmset"); p.arguments = [verb]; try? p.run() }
func sleepNow() { pmset("sleepnow") }            // no root
func displaySleep() { pmset("displaysleepnow") }  // no root
func power(_ verb: String) { _ = runScript("tell application \"System Events\" to \(verb)") } // "shut down" | "restart" | "log out" | "sleep"; Automation
```

```swift
// 6. Type Unicode text (20 UTF-16 units per event; Return/Tab as real keys)
func typeChunk(_ units: [UInt16]) {
    let src = CGEventSource(stateID: .hidSystemState)
    for down in [true, false] {
        guard let e = CGEvent(keyboardEventSource: src, virtualKey: 0, keyDown: down) else { return }
        units.withUnsafeBufferPointer { e.keyboardSetUnicodeString(stringLength: $0.count, unicodeString: $0.baseAddress) }
        e.post(tap: .cghidEventTap)
    }
}
func typeText(_ text: String) {
    var buf: [UInt16] = []
    func flush() { if !buf.isEmpty { typeChunk(buf); buf.removeAll(); usleep(5_000) } }
    for ch in text {
        switch ch {
        case "\n", "\r\n", "\r": flush(); postKey(0x24)          // kVK_Return
        case "\t": flush(); postKey(0x30)                         // kVK_Tab
        default:
            let u = Array(ch.utf16)
            if buf.count + u.count > 20 { flush() }
            buf += u
        }
    }
    flush()
}
func pasteText(_ s: String) {
    let pb = NSPasteboard.general; let old = pb.string(forType: .string)
    pb.clearContents(); pb.setString(s, forType: .string); let count = pb.changeCount
    postKey(0x09, flags: [.maskCommand])                                  // Cmd+V
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
        guard pb.changeCount == count, let old else { return }
        pb.clearContents(); pb.setString(old, forType: .string)
    }
}
```

```swift
// 7. Screen preview: SCK screenshot -> JPEG q0.5, plus tap mapping
import ScreenCaptureKit, UniformTypeIdentifiers
final class Previewer {
    private var displays: [SCDisplay] = []
    func refresh() async throws { displays = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true).displays }
    func grab(displayID: CGDirectDisplayID, maxWidth: Int = 1280) async throws -> (jpeg: Data, frame: CGRect, w: Int, h: Int) {
        guard let d = displays.first(where: { $0.displayID == displayID }) else { throw SCStreamError(.noDisplayList) }
        let scale = min(1.0, Double(maxWidth) / Double(d.width))
        let cfg = SCStreamConfiguration()
        cfg.width = Int(Double(d.width) * scale); cfg.height = Int(Double(d.height) * scale)
        cfg.showsCursor = true; cfg.captureResolution = .nominal; cfg.pixelFormat = kCVPixelFormatType_32BGRA
        let img = try await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(display: d, excludingWindows: []), configuration: cfg)
        guard let jpg = jpeg(img, quality: 0.5) else { throw CocoaError(.fileWriteUnknown) }
        return (jpg, d.frame, img.width, img.height)
    }
    func jpeg(_ img: CGImage, quality: Double) -> Data? {
        let data = NSMutableData()
        guard let dst = CGImageDestinationCreateWithData(data, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(dst, img, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
        return CGImageDestinationFinalize(dst) ? data as Data : nil
    }
    /// Preview pixel -> global display point (CGDisplayBounds space, top-left origin of main display).
    static func toGlobal(px: Double, py: Double, imgW: Int, imgH: Int, frame: CGRect) -> CGPoint {
        CGPoint(x: frame.minX + px / Double(imgW) * frame.width, y: frame.minY + py / Double(imgH) * frame.height)
    }
}
// For continuous 5 fps prefer SCStream: cfg.minimumFrameInterval = CMTime(value: 1, timescale: 5);
// SCStream(filter:configuration:delegate:), addStreamOutput(_, type: .screen, sampleHandlerQueue:), startCapture().
```

```swift
// 8. Pairing-code notification (bundle + signed app; LSUIElement OK)
import UserNotifications
func notifyPairingCode(_ code: String) async {
    let c = UNUserNotificationCenter.current()
    guard (try? await c.requestAuthorization(options: [.alert, .sound])) == true else { return }
    let content = UNMutableNotificationContent(); content.title = "Reach pairing code"; content.body = code
    try? await c.add(UNNotificationRequest(identifier: "pair-\(code)", content: content, trigger: nil))
}
// Delegate: func userNotificationCenter(_:willPresent:withCompletionHandler:) { handler([.banner, .sound]) }
```

Info.plist keys for the swiftc bundle: `CFBundleIdentifier`, `CFBundleName`, `CFBundleExecutable`, `LSUIElement = true`, `NSAppleEventsUsageDescription`, `NSHumanReadableCopyright`; sign with `codesign --force --deep -s - Reach.app` at minimum.

## Unverified

- `SACLockScreenImmediate` behaviour on macOS 14/15/26 (no breakage reports found; Hammerspoon still ships it); its return type (declared `void` in timac's post).
- Whether `CGSession -suspend` still exists/works on 14/15 (one Big Sur report says it stopped working).
- Whether System Events `shut down` / `restart` / `log out` show the 60-second confirmation sheet on 14/15, and the exact semantics of `with state saving preference`.
- That synthetic `NX_SYSDEFINED` media events reach Spotify / Music / Chrome / Safari exactly like physical keys (inferred from the shared path + Hammerspoon usage; test on each).
- `NSWorkspace.openApplication(at:)` on an already-running app as a reliable focus fallback under cooperative activation.
- Runtime behaviour of `CGDisplayCreateImage` on 15+ when compiled with deployment target 14 (SDK marks it obsoleted 15.0).
- `NX_KEYTYPE_BRIGHTNESS_UP/DOWN` synthetic events driving the built-in panel on Apple Silicon.
- `set volume …` (StandardAdditions) not triggering an Automation prompt.
- Exact Sequoia 15.1+ re-approval cadence and whether `SCContentSharingPicker` users are exempt.
- `CGSSessionScreenIsLocked` key name and the `com.apple.screenIsLocked` notification name on 15.
- Persistence of TCC grants for ad-hoc-signed bundles across rebuilds.
- `AEDeterminePermissionToAutomateTarget` signature (from memory, not re-verified).

## Sources

Apple docs (via developer.apple.com JSON): NSRunningApplication `activate(options:)`, `activate(from:options:)`, `terminate()`, `forceTerminate()`, `activationPolicy`; NSApplication `activate()`, `yieldActivation(to:)`, `ActivationOptions.activateIgnoringOtherApps` (deprecated 14.0); [AppKit Release Notes for macOS 14 — App Activation](https://developer.apple.com/documentation/macos-release-notes/appkit-release-notes-for-macos-14); NSWorkspace `openApplication(at:configuration:)`, `OpenConfiguration.activates`, `urlForApplication(withBundleIdentifier:)`, `open(_:)`, `open(_:withApplicationAt:configuration:)`, `runningApplications`, `icon(forFile:)`; NSImage `cgImage(forProposedRect:context:hints:)`; FileManager `displayName(atPath:)`; `CGWindowListCopyWindowInfo`, `kCGWindowName`, `kCGWindowOwnerName`, `kCGWindowBounds`, `kCGWindowLayer`; `CGPreflightScreenCaptureAccess`, `CGRequestScreenCaptureAccess`; `kAXRaiseAction`, `kAXMainAttribute`, `kAXWindowsAttribute`, `kAXFocusedWindowAttribute`, `kAXMinimizedAttribute`, `kAXTrustedCheckOptionPrompt`; NSEvent `otherEvent(with:…)`; CGEvent `init(keyboardEventSource:virtualKey:keyDown:)`, `keyboardSetUnicodeString`, `post(tap:)`, `init(mouseEventSource:…)`; `CGDisplayBounds`, `CGGetActiveDisplayList`; ScreenCaptureKit `SCScreenshotManager`, `captureImage(contentFilter:configuration:)`, `captureImage(in:)`, `SCContentFilter(display:excludingWindows:)`, `SCStreamConfiguration` (`width/height/minimumFrameInterval/showsCursor/captureResolution/scalesToFit`), `SCDisplay.frame/displayID`, `SCShareableContent`, `SCStreamError.Code`, `SCContentFilter.contentRect/pointPixelScale`; `kCGImageDestinationLossyCompressionQuality`; `NSBitmapImageRep.representation(using:properties:)`; `UNUserNotificationCenter.current()`, `requestAuthorization(options:)`; `NSAppleEventsUsageDescription`; `kAudioHardwareServiceDeviceProperty_VirtualMainVolume`/`…MasterVolume`, `kAudioHardwarePropertyDefaultOutputDevice`, `kAudioObjectPropertyElementMain`; `NSAppleScript.executeAndReturnError`.

Headers: [ev_keymap.h](https://raw.githubusercontent.com/phracker/MacOSX-SDKs/master/MacOSX11.3.sdk/System/Library/Frameworks/IOKit.framework/Versions/A/Headers/hidsystem/ev_keymap.h), [IOLLEvent.h](https://raw.githubusercontent.com/phracker/MacOSX-SDKs/master/MacOSX11.3.sdk/System/Library/Frameworks/IOKit.framework/Versions/A/Headers/hidsystem/IOLLEvent.h), [Carbon Events.h (kVK_*)](https://raw.githubusercontent.com/phracker/MacOSX-SDKs/master/MacOSX11.3.sdk/System/Library/Frameworks/Carbon.framework/Versions/A/Frameworks/HIToolbox.framework/Versions/A/Headers/Events.h), [pmset.1 (apple-oss-distributions/PowerManagement)](https://raw.githubusercontent.com/apple-oss-distributions/PowerManagement/main/pmset/pmset.1).

Open source: Hammerspoon [libeventtap_event.m](https://raw.githubusercontent.com/Hammerspoon/hammerspoon/master/extensions/eventtap/libeventtap_event.m), [libeventtap.m](https://raw.githubusercontent.com/Hammerspoon/hammerspoon/master/extensions/eventtap/libeventtap.m), [libcaffeinate.m](https://raw.githubusercontent.com/Hammerspoon/hammerspoon/master/extensions/caffeinate/libcaffeinate.m), [libbrightness.m](https://raw.githubusercontent.com/Hammerspoon/hammerspoon/master/extensions/brightness/libbrightness.m); [nriley/brightness brightness.c](https://raw.githubusercontent.com/nriley/brightness/master/brightness.c); [MonitorControl AppleDisplay.swift](https://raw.githubusercontent.com/MonitorControl/MonitorControl/main/MonitorControl/Model/AppleDisplay.swift); [go-macos/brightness](https://pkg.go.dev/github.com/go-macos/brightness); [enigo macos_impl.rs](https://raw.githubusercontent.com/enigo-rs/enigo/main/src/macos/macos_impl.rs); yabai [extern.h](https://raw.githubusercontent.com/koekeishiya/yabai/master/src/misc/extern.h), [window_manager.c](https://raw.githubusercontent.com/koekeishiya/yabai/master/src/window_manager.c); [Rectangle AccessibilityElement.swift](https://raw.githubusercontent.com/rxhanson/Rectangle/master/Rectangle/AccessibilityElement.swift); Chromium [permissions_utils.mm](https://raw.githubusercontent.com/chromium/chromium/main/ui/base/cocoa/permissions_utils.mm), [curtain_mode_mac.cc](https://raw.githubusercontent.com/chromium/chromium/main/remoting/host/curtain_mode_mac.cc); [PanicLock](https://github.com/paniclock/paniclock/).

Forums / articles: Apple forums [739524](https://developer.apple.com/forums/thread/739524) (activate on Sonoma), [793253](https://developer.apple.com/forums/thread/793253), [682918](https://developer.apple.com/forums/thread/682918) (CGSession on Big Sur), [724249](https://developer.apple.com/forums/thread/724249) & [679326](https://developer.apple.com/forums/thread/679326) (UNUserNotificationCenter bundle requirement), [839069](https://developer.apple.com/forums/thread/839069) (kCGWindowOwnerName gating is undocumented), [748798](https://developer.apple.com/forums/thread/748798) (CGDisplayCreateImage → SCScreenshotManager), [103992](https://developer.apple.com/forums/thread/103992) (CGEventPost needs Accessibility, 10.14); [timac: programmatically lock the screen](https://blog.timac.org/2016/0605-programmatically-lock-the-screen/); [9to5mac: Sequoia monthly screen-recording prompt](https://9to5mac.com/2024/08/14/macos-sequoia-screen-recording-prompt-monthly/); [tinyapps: Sequoia nag / 15.1 replayd](https://tinyapps.org/blog/202409180700_disable_sequoia_nag.html); [MacPorts #70756 (obsoleted in macOS 15.0 message)](https://trac.macports.org/ticket/70756); [Jamf: loginwindow «event aevtrlgo»](https://community.jamf.com/t5/jamf-pro/policy-to-logout-after-a-certain-time-idle/m-p/169791); [MacScripter: aevtrrst](https://www.macscripter.net/t/what-does-aevtrrst-mean/67572); [desktop-notifier docs (signing requirement)](https://desktop-notifier.readthedocs.io/); [Chrome 73 hardware media keys](https://www.thurrott.com/?p=199249); [Qt forum: CGEventKeyboardSetUnicodeString 20-char limit](https://forum.qt.io/post/46576).
