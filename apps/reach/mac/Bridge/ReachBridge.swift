import AppKit

/// The agent's own settings page talks to the app through the `NativeBridge`
/// (`window.webkit.messageHandlers.native`). Registers the common `app.*` methods of
/// `docs/ARCHITECTURE.md` §4 and forwards every Reach method — the remote-control ones, which
/// the page uses too (the PWA doubles as the settings UI), and the local `reach.*` ones — to
/// `ReachMethods` with `local: true`. Events (`reach.state`, `reach.pairCode`,
/// `reach.paired`, `info`, `apps.running`) reach the page through `AppDelegate`'s wiring of
/// `ReachAPI.onLocalEvent` to `NativeBridge.emit`.
@MainActor
final class ReachBridge {

    private static let empty: [String: Any] = [:]

    private let bridge: NativeBridge
    private unowned let api: ReachAPI

    init(bridge: NativeBridge, api: ReachAPI) {
        self.bridge = bridge
        self.api = api
    }

    // MARK: - Registration

    func registerAll() {
        registerApp()
        registerMethods()
    }

    private func registerApp() {
        bridge.register("app.info") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let info: [String: Any] = [
                "name": "Reach",
                "version": self.api.version,
                "platform": ReachAPI.platform,
                "machine": ReachAPI.hostname,
                "permissions": self.api.permissions,
            ]
            return info
        }

        bridge.register("app.openURL") { params in
            do {
                try URLOpener.open(params["url"] as? String ?? "")
            } catch {
                throw ReachError.wrap(error).asBridgeError
            }
            return ReachBridge.empty
        }

        bridge.register("app.quit") { _ in
            Log.info("quit requested from the UI", category: "app")
            // Let the reply reach the page first.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
                MainActor.assumeIsolated { NSApp.terminate(nil) }
            }
            return ReachBridge.empty
        }

        bridge.register("app.openSettingsPane") { params in
            ReachBridge.openSettingsPane(params["pane"] as? String ?? "")
            return ReachBridge.empty
        }
    }

    private func registerMethods() {
        for method in ReachMethods.allMethods {
            bridge.registerAsync(method) { [weak self] params, done in
                guard let self else {
                    done(.failure(BridgeError.internalError("bridge is gone")))
                    return
                }
                self.api.methods.call(method, params: params, session: nil, local: true) { result in
                    switch result {
                    case .success(let value):
                        done(.success(value))
                    case .failure(let error):
                        done(.failure(ReachError.wrap(error).asBridgeError))
                    }
                }
            }
        }
    }

    // MARK: - Settings panes

    /// System Settings deep links for the permission notices and the login item.
    private static func openSettingsPane(_ pane: String) {
        switch pane.lowercased() {
        case "screenrecording", "screen_recording", "screen":
            Permissions.openSettingsPane(.screenRecording)
        case "inputmonitoring", "input_monitoring":
            Permissions.openSettingsPane(.inputMonitoring)
        case "localnetwork", "local_network":
            Permissions.openSettingsPane(.localNetwork)
        case "automation":
            open("x-apple.systempreferences:com.apple.preference.security?Privacy_Automation")
        case "notifications":
            open("x-apple.systempreferences:com.apple.preference.notifications")
        case "startup", "login", "loginitems", "launchatlogin":
            LaunchAtLogin.openLoginItemsSettings()
        default:
            Permissions.openSettingsPane(.accessibility)
        }
    }

    private static func open(_ link: String) {
        guard let url = URL(string: link) else { return }
        NSWorkspace.shared.open(url)
    }
}
