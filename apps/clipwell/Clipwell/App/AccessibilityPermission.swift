import AppKit
import ApplicationServices

/// Accessibility (AX) permission gate.
///
/// Clipwell needs `kTCCServiceAccessibility` for two distinct things:
///   1. reading and writing window position/size on other apps' windows, and
///   2. posting the synthetic ⌘V that performs the paste.
///
/// Both are the *same* permission, so one grant covers the whole app.
enum AccessibilityPermission {

    /// Non-prompting check. Safe to call as often as you like.
    static var isTrusted: Bool {
        AXIsProcessTrusted()
    }

    /// Prompting check — shows the system "…would like to control this computer" alert with
    /// a button through to System Settings. macOS only shows it once per app version, so we
    /// pair it with our own helper window for every subsequent nudge.
    @discardableResult
    static func requestTrust() -> Bool {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true]
        return AXIsProcessTrustedWithOptions(options as CFDictionary)
    }

    /// Deep-links straight to the Accessibility pane of Privacy & Security.
    static func openSystemSettings() {
        let url = URL(string:
            "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")!
        NSWorkspace.shared.open(url)
    }

    // Note: there is no notification for a TCC grant and macOS does not relaunch the app when
    // the user flips the switch, so anything that needs to react to the grant has to poll.
    // `PermissionWindowController` and `SettingsView` each do so while they are on screen.
}
