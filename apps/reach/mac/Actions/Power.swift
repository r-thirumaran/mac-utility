import AppKit
import Darwin

/// `power {action}`: lock, sleep, displayOff, restart, shutdown — every one as the logged-in
/// user, nothing needs root.
///
/// Lock tries `SACLockScreenImmediate` from the private login framework (resolved with
/// `dlsym`, skipped when absent; what the Keychain menu's "Lock Screen" used), then
/// `CGSession -suspend`, then a synthetic ⌃⌘Q (needs Accessibility). Sleep and display-off
/// are `pmset sleepnow` / `pmset displaysleepnow`, which the console user may run. Restart
/// and shut down ask System Events, the same Apple events the  menu sends; the first use
/// shows the Automation consent prompt and a denial is reported as `needs_permission`.
enum PowerControl {

    static let actions = ["lock", "sleep", "displayOff", "restart", "shutdown"]

    // MARK: - Dispatch

    /// Runs `action`; `completion` may be called on any queue.
    @MainActor
    static func perform(_ action: String, completion: @escaping (Error?) -> Void) {
        switch action {
        case "lock":
            completion(lock())
        case "sleep":
            DispatchQueue.global(qos: .userInitiated).async {
                completion(runPmset(["sleepnow"], what: "sleep"))
            }
        case "displayOff":
            DispatchQueue.global(qos: .userInitiated).async {
                completion(runPmset(["displaysleepnow"], what: "display off"))
            }
        case "restart":
            completion(tellSystemEvents("restart"))
        case "shutdown":
            completion(tellSystemEvents("shut down"))
        default:
            completion(ReachError.invalidParams("unknown power action \"\(action)\""))
        }
    }

    // MARK: - pmset

    private static func runPmset(_ arguments: [String], what: String) -> Error? {
        do {
            let result = try Shell.run("/usr/bin/pmset", arguments)
            guard result.status == 0 else {
                let detail = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
                return ReachError.internalError("\(what) failed: \(detail.isEmpty ? "pmset exited \(result.status)" : detail)")
            }
            return nil
        } catch {
            return ReachError.internalError("\(what) failed: \(error)")
        }
    }

    // MARK: - Lock

    @MainActor
    private static func lock() -> Error? {
        if lockViaLoginFramework() { return nil }
        if lockViaCGSession() { return nil }
        if Permissions.accessibilityGranted, postControlCommandQ() { return nil }
        return ReachError.needsPermission("accessibility",
            "Locking the screen needs Accessibility access for Reach (System Settings ▸ Privacy & Security ▸ Accessibility).")
    }

    /// `SACLockScreenImmediate()` — private, so resolved at run time and skipped when missing.
    private static func lockViaLoginFramework() -> Bool {
        guard let handle = dlopen("/System/Library/PrivateFrameworks/login.framework/login", RTLD_LAZY),
              let symbol = dlsym(handle, "SACLockScreenImmediate") else { return false }
        typealias LockFunction = @convention(c) () -> Int32
        let lockScreen = unsafeBitCast(symbol, to: LockFunction.self)
        _ = lockScreen()
        return true
    }

    private static func lockViaCGSession() -> Bool {
        let path = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession"
        guard FileManager.default.isExecutableFile(atPath: path),
              let result = try? Shell.run(path, ["-suspend"]) else { return false }
        return result.status == 0
    }

    /// ⌃⌘Q (key code 12) through the HID event tap.
    private static func postControlCommandQ() -> Bool {
        guard let source = CGEventSource(stateID: .hidSystemState),
              let down = CGEvent(keyboardEventSource: source, virtualKey: 12, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 12, keyDown: false) else { return false }
        down.flags = [.maskControl, .maskCommand]
        up.flags = [.maskControl, .maskCommand]
        down.post(tap: .cghidEventTap)
        up.post(tap: .cghidEventTap)
        return true
    }

    // MARK: - System Events

    /// `tell application "System Events" to <verb>`.
    @MainActor
    private static func tellSystemEvents(_ verb: String) -> Error? {
        guard let script = NSAppleScript(source: "tell application \"System Events\" to \(verb)") else {
            return ReachError.internalError("could not build the System Events script")
        }
        var errorInfo: NSDictionary?
        _ = script.executeAndReturnError(&errorInfo)
        guard let errorInfo else { return nil }
        let number = errorInfo[NSAppleScript.errorNumber] as? Int ?? 0
        let message = errorInfo[NSAppleScript.errorMessage] as? String ?? "AppleScript error \(number)"
        switch number {
        case -1743, -1744:
            return ReachError.needsPermission("automation",
                "Allow Reach to control System Events in System Settings ▸ Privacy & Security ▸ Automation, then try again.")
        case -128:
            return ReachError.notAuthorized("\(verb) was cancelled")
        default:
            return ReachError.internalError(message)
        }
    }
}
