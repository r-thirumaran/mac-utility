#if canImport(AppKit)
import AppKit
import ApplicationServices

/// TCC permission checks, prompts and System Settings deep links.
///
/// macOS shows each system prompt at most once per app version; after that the only way in
/// is the Settings pane, which is why every check has an `openSettingsPane` companion. There
/// is no notification for a grant, so UIs poll while they are visible.
enum Permissions {

    /// Privacy & Security panes reachable by URL.
    enum Pane: String {
        case accessibility = "Privacy_Accessibility"
        case inputMonitoring = "Privacy_ListenEvent"
        case screenRecording = "Privacy_ScreenCapture"
        case localNetwork = "Privacy_LocalNetwork"
    }

    // MARK: - Accessibility

    /// Non-prompting check.
    static var accessibilityGranted: Bool {
        AXIsProcessTrusted()
    }

    /// Shows the system "would like to control this computer" alert (once per version).
    @discardableResult
    static func requestAccessibility() -> Bool {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true]
        return AXIsProcessTrustedWithOptions(options as CFDictionary)
    }

    // MARK: - Input Monitoring

    static var inputMonitoringGranted: Bool {
        CGPreflightListenEventAccess()
    }

    @discardableResult
    static func requestInputMonitoring() -> Bool {
        CGRequestListenEventAccess()
    }

    // MARK: - Screen Recording

    static var screenRecordingGranted: Bool {
        CGPreflightScreenCaptureAccess()
    }

    @discardableResult
    static func requestScreenRecording() -> Bool {
        CGRequestScreenCaptureAccess()
    }

    // MARK: - Settings

    /// Deep-links to a pane of System Settings ▸ Privacy & Security.
    static func openSettingsPane(_ pane: Pane) {
        guard let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?" + pane.rawValue) else {
            return
        }
        NSWorkspace.shared.open(url)
    }

    /// The `permissions` object of `app.info`.
    static var summary: [String: Bool] {
        [
            "accessibility": accessibilityGranted,
            "inputMonitoring": inputMonitoringGranted,
            "screenRecording": screenRecordingGranted,
        ]
    }
}
#endif
