#if canImport(ServiceManagement)
import Foundation
import ServiceManagement

/// Thin wrapper over `SMAppService.mainApp` (macOS 13+), the replacement for login-item
/// helper bundles. Registration is keyed off the bundle id *and* location, so enable it from
/// the installed copy in /Applications, not from a build directory.
enum LaunchAtLogin {

    static var isEnabled: Bool {
        SMAppService.mainApp.status == .enabled
    }

    /// `true` while macOS holds the registration behind the user's approval in
    /// System Settings ▸ General ▸ Login Items.
    static var requiresApproval: Bool {
        SMAppService.mainApp.status == .requiresApproval
    }

    static func setEnabled(_ enabled: Bool) throws {
        if enabled {
            // Registering twice throws; harmless, but avoid the noise.
            guard SMAppService.mainApp.status != .enabled else { return }
            try SMAppService.mainApp.register()
        } else {
            guard SMAppService.mainApp.status != .notRegistered else { return }
            try SMAppService.mainApp.unregister()
        }
    }

    static func openLoginItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}
#endif
