import AppKit
import Darwin

/// Immediate power actions (`power.action`): sleep, display off, lock, restart, shut down.
///
/// Sleep and display-off go through `pmset sleepnow` / `pmset displaysleepnow`, which the
/// console user may run without root (sleep falls back to System Events if pmset refuses).
/// Lock tries the route shipping apps use on macOS 14/15 — `SACLockScreenImmediate` in the
/// private login framework, resolved with `dlsym` and skipped when absent — then a synthetic
/// ⌃⌘Q key press (needs Accessibility), then the legacy `CGSession -suspend`. Restart and
/// shut down ask System Events, the same Apple events the  menu sends, so open apps may
/// still refuse; the first use triggers the Automation consent prompt and a denial surfaces
/// as `needs_permission`.
///
/// `perform` must be called on the main actor (AppleScript and CGEvent); the completion runs
/// on an arbitrary queue.
enum PowerActions {

    static let all = PowerSettings.actions

    // MARK: - Dispatch

    @MainActor
    static func perform(_ action: String, completion: @escaping (Result<Void, Error>) -> Void) {
        switch action {
        case "sleep":
            DispatchQueue.global(qos: .userInitiated).async {
                do {
                    try runPmset(["sleepnow"], what: "sleep")
                    completion(.success(()))
                } catch {
                    // "You must run this as root" on some configurations: ask loginwindow instead.
                    DispatchQueue.main.async {
                        MainActor.assumeIsolated {
                            completion(Result { try tellSystemEvents("sleep") })
                        }
                    }
                }
            }
        case "displayOff":
            DispatchQueue.global(qos: .userInitiated).async {
                completion(Result { try runPmset(["displaysleepnow"], what: "display off") })
            }
        case "lock":
            completion(Result { try lock() })
        case "restart":
            completion(Result { try tellSystemEvents("restart") })
        case "shutdown":
            completion(Result { try tellSystemEvents("shut down") })
        default:
            completion(.failure(BridgeError.invalidParams("action must be one of \(all.joined(separator: ", "))")))
        }
    }

    // MARK: - pmset

    private static func runPmset(_ arguments: [String], what: String) throws {
        let result = try Shell.run("/usr/bin/pmset", arguments)
        guard result.status == 0 else {
            let detail = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            throw BridgeError.internalError("\(what) failed: \(detail.isEmpty ? "pmset exited \(result.status)" : detail)")
        }
    }

    // MARK: - Lock

    @MainActor
    private static func lock() throws {
        if lockViaLoginFramework() { return }
        if Permissions.accessibilityGranted, postControlCommandQ() { return }
        if lockViaCGSession() { return }
        throw BridgeError.needsPermission("Locking the screen needs Accessibility access for Vitals (System Settings ▸ Privacy & Security ▸ Accessibility).")
    }

    /// `SACLockScreenImmediate()` from login.framework — private, so resolved at run time and
    /// skipped when it is not there.
    private static func lockViaLoginFramework() -> Bool {
        guard let handle = dlopen("/System/Library/PrivateFrameworks/login.framework/login", RTLD_LAZY),
              let symbol = dlsym(handle, "SACLockScreenImmediate") else { return false }
        typealias LockFunction = @convention(c) () -> Int32
        let lockScreen = unsafeBitCast(symbol, to: LockFunction.self)
        _ = lockScreen()
        return true
    }

    /// ⌃⌘Q (key code 12) through the HID event tap; needs Accessibility.
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

    private static func lockViaCGSession() -> Bool {
        let path = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession"
        guard FileManager.default.isExecutableFile(atPath: path),
              let result = try? Shell.run(path, ["-suspend"]) else { return false }
        return result.status == 0
    }

    // MARK: - System Events

    /// `tell application "System Events" to <verb>` — restart, shut down, sleep.
    @MainActor
    private static func tellSystemEvents(_ verb: String) throws {
        guard let script = NSAppleScript(source: "tell application \"System Events\" to \(verb)") else {
            throw BridgeError.internalError("could not build the System Events script")
        }
        var errorInfo: NSDictionary?
        _ = script.executeAndReturnError(&errorInfo)
        guard let errorInfo else { return }
        let number = errorInfo[NSAppleScript.errorNumber] as? Int ?? 0
        let message = errorInfo[NSAppleScript.errorMessage] as? String ?? "AppleScript error \(number)"
        switch number {
        case -1743, -1744:
            throw BridgeError.needsPermission("Allow Vitals to control System Events in System Settings ▸ Privacy & Security ▸ Automation, then try again.")
        case -128:
            throw BridgeError.notAuthorized("\(verb) was cancelled")
        default:
            throw BridgeError.internalError(message)
        }
    }
}
