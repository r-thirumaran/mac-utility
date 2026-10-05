import Foundation
import ServiceManagement

/// Thin wrapper over `SMAppService.mainApp` — the modern (macOS 13+) replacement for
/// `SMLoginItemSetEnabled` and login-item helper bundles.
///
/// Note for future-you: registration is keyed off the app's *bundle identifier and location*.
/// Registering while the app runs from DerivedData and then moving it to /Applications leaves
/// a stale entry behind, so `make install` should be run before enabling this.
enum LaunchAtLogin {

    static var isEnabled: Bool {
        SMAppService.mainApp.status == .enabled
    }

    /// `true` if macOS is holding the registration behind the user's approval in
    /// System Settings ▸ General ▸ Login Items.
    static var requiresApproval: Bool {
        SMAppService.mainApp.status == .requiresApproval
    }

    @discardableResult
    static func set(_ enabled: Bool) -> Bool {
        do {
            if enabled {
                // Registering when already registered throws; harmless, but avoid the noise.
                guard SMAppService.mainApp.status != .enabled else { return true }
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            return true
        } catch {
            NSLog("Clipwell: launch-at-login \(enabled ? "register" : "unregister") failed: \(error)")
            return false
        }
    }

    static func openLoginItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}
