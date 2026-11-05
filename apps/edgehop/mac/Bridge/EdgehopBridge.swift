import AppKit

/// Registers every bridge method of `docs/ARCHITECTURE.md` §4–§5 on the `NativeBridge`, with
/// the names, parameters, result shapes, error codes and events of the Go backend
/// (`apps/edgehop/win/bridge.go`), so the shared web UI cannot tell the platforms apart.
/// It also owns what the app layer adds on top of the node: the log ring the UI reads on
/// open, the pairing in progress, launch at login and the version.
@MainActor
final class EdgehopBridge {

    private static let logKeep = 300
    private static let empty: [String: Any] = [:]
    private static let openableSchemes: Set<String> = ["http", "https"]

    /// Fired after every state emission (the status menu follows).
    var onStateChanged: (() -> Void)?

    private let bridge: NativeBridge
    private let node: Node
    private let transport: Transport
    private let version: String
    private var logs: [LogEntry] = []
    private var pairing: PairingState?

    init(bridge: NativeBridge, node: Node, transport: Transport, version: String) {
        self.bridge = bridge
        self.node = node
        self.transport = transport
        self.version = version
    }

    // MARK: - Registration

    func registerAll() {
        registerApp()
        registerState()
        registerPairing()
        registerSettings()
    }

    private func registerApp() {
        bridge.register("app.info") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            return self.info()
        }

        bridge.register("app.openURL") { params in
            let raw = (params["url"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard let url = URL(string: raw), let scheme = url.scheme?.lowercased() else {
                throw BridgeError.invalidParams("url must be absolute")
            }
            guard EdgehopBridge.openableSchemes.contains(scheme) else {
                throw BridgeError.invalidParams("only http(s) URLs")
            }
            NSWorkspace.shared.open(url)
            return EdgehopBridge.empty
        }

        bridge.register("app.quit") { _ in
            Log.info("quit requested from the UI")
            // Let the reply reach the page first.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
                MainActor.assumeIsolated { NSApp.terminate(nil) }
            }
            return EdgehopBridge.empty
        }

        bridge.register("app.openSettingsPane") { params in
            EdgehopBridge.openSettingsPane(params["pane"] as? String ?? "")
            return EdgehopBridge.empty
        }
    }

    private func registerState() {
        bridge.register("edgehop.state") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            return try self.state()
        }

        // Spec §5: `{layout: {updatedAt?, machines:[{id,x,y}]}}`. A legacy `{tiles}` layout is
        // still accepted (converted per §4). Returns the new state; an invalid canvas comes
        // back as `invalid_layout` naming the machines that overlap.
        bridge.register("edgehop.layout.set") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            guard let raw = params["layout"] else { throw BridgeError.invalidParams("layout required") }
            let layout: Layout
            do {
                layout = try decodeJSON(Layout.self, from: raw)
            } catch {
                throw BridgeError.invalidParams("invalid layout: \(error)")
            }
            do {
                try self.node.setLayout(layout)
            } catch let error as BridgeError {
                throw error
            } catch let error as Layout.ValidationError {
                throw BridgeError(code: "invalid_layout", message: error.description)
            } catch {
                // `win/bridge.go` answers every `SetLayout` failure with this code.
                throw BridgeError(code: "invalid_layout", message: String(describing: error))
            }
            return try self.state()
        }

        bridge.register("edgehop.switchTo") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            var peerID = params["peerId"] as? String ?? ""
            if peerID.isEmpty {
                peerID = self.node.id
            }
            try self.node.claim(peerID)
            return EdgehopBridge.empty
        }

        bridge.register("edgehop.log.recent") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let result: [String: Any] = ["entries": try jsonObject(self.logs)]
            return result
        }
    }

    private func registerPairing() {
        bridge.register("edgehop.pair.start") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let peerID = params["peerId"] as? String ?? ""
            let address = (params["address"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !peerID.isEmpty || !address.isEmpty else {
                throw BridgeError.invalidParams("peerId or address required")
            }
            guard peerID != self.node.id else {
                throw BridgeError.invalidParams("cannot pair with this machine")
            }
            // Record the placeholder stage before the transport runs: its own
            // connecting/waiting_code events must not be overwritten by a later "connecting".
            let previous = self.pairing
            self.pairing = PairingState(stage: "connecting", peerId: peerID, address: address)
            do {
                try self.transport.pairStart(peerID: peerID, address: address)
            } catch {
                self.pairing = previous
                throw BridgeError.internalError(String(describing: error))
            }
            self.emitState()
            return EdgehopBridge.empty
        }

        bridge.register("edgehop.pair.confirm") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let peerID = params["peerId"] as? String ?? ""
            let code = (params["code"] as? String ?? "").filter { $0.isASCII && $0.isNumber }
            guard code.count == 6 else { throw BridgeError.invalidParams("the code has 6 digits") }
            do {
                try self.transport.pairConfirm(peerID: peerID, code: code)
            } catch {
                throw BridgeError.internalError(String(describing: error))
            }
            return EdgehopBridge.empty
        }

        bridge.register("edgehop.pair.cancel") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            self.transport.pairCancel()
            return EdgehopBridge.empty
        }

        bridge.register("edgehop.unpair") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            let peerID = params["peerId"] as? String ?? ""
            guard !peerID.isEmpty else { throw BridgeError.invalidParams("peerId required") }
            self.transport.unpair(peerID)
            self.node.forget(peerID)
            return EdgehopBridge.empty
        }
    }

    private func registerSettings() {
        bridge.register("edgehop.settings.get") { [weak self] _ in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            return try self.settings()
        }

        bridge.register("edgehop.settings.set") { [weak self] params in
            guard let self else { throw BridgeError.internalError("bridge is gone") }
            try self.applySettings(params)
            return try self.settings()
        }
    }

    // MARK: - Shapes

    /// `app.info`: platform "macos", the permission badges, no fake input.
    private func info() -> [String: Any] {
        [
            "name": "Edgehop",
            "version": version,
            "platform": "macos",
            "machine": node.name,
            "machineId": node.id,
            "permissions": EdgehopBridge.permissions(),
            "fakeInput": false,
            "goos": "darwin",
        ]
    }

    /// The `permissions` object of the state and of `app.info`.
    static func permissions() -> [String: Bool] {
        ["accessibility": Permissions.accessibilityGranted, "inputMonitoring": Permissions.inputMonitoringGranted]
    }

    /// `edgehop.state`: the node's state plus the pairing in progress, launch at login, the
    /// version and `fakeInput` (`uiState` in `win/bridge.go`).
    func state() throws -> [String: Any] {
        guard var object = try jsonObject(node.state()) as? [String: Any] else {
            throw BridgeError.internalError("state is not an object")
        }
        if let pairing {
            object["pairing"] = try jsonObject(pairing)
        }
        object["launchAtLogin"] = LaunchAtLogin.isEnabled
        object["version"] = version
        object["fakeInput"] = false
        return object
    }

    /// `edgehop.settings.get/set`: the node's settings plus the name and the autostart flag.
    private func settings() throws -> [String: Any] {
        guard var object = try jsonObject(node.settings) as? [String: Any] else {
            throw BridgeError.internalError("settings are not an object")
        }
        object["name"] = node.name
        object["launchAtLogin"] = LaunchAtLogin.isEnabled
        return object
    }

    /// Merges the given keys into the settings (a partial object is fine).
    private func applySettings(_ params: [String: Any]) throws {
        guard var merged = try jsonObject(node.settings) as? [String: Any] else {
            throw BridgeError.internalError("settings are not an object")
        }
        for (key, value) in params {
            merged[key] = value
        }
        let settings: EdgehopSettings
        do {
            settings = try decodeJSON(EdgehopSettings.self, from: merged)
        } catch {
            throw BridgeError.invalidParams("invalid params: \(error)")
        }
        if let name = params["name"] as? String {
            try node.setName(name)
        }
        node.setSettings(settings)
        if let wanted = params["launchAtLogin"] as? Bool, wanted != LaunchAtLogin.isEnabled {
            do {
                try LaunchAtLogin.setEnabled(wanted)
            } catch {
                throw BridgeError.internalError("launch at login: \(error)")
            }
            if LaunchAtLogin.requiresApproval {
                LaunchAtLogin.openLoginItemsSettings()
            }
        }
        emitState()
    }

    // MARK: - Events

    /// Pushes `edgehop.state` to the page and refreshes the menu.
    func emitState() {
        do {
            bridge.emit("edgehop.state", try state())
        } catch {
            Log.error("state: \(error)", category: "bridge")
        }
        onStateChanged?()
    }

    /// A log line from the node or the transport: kept for `edgehop.log.recent`, pushed as
    /// `edgehop.log`. Debug lines stay in the unified log only.
    func addLog(_ entry: LogEntry) {
        guard entry.level != "debug" else { return }
        logs.append(entry)
        if logs.count > EdgehopBridge.logKeep {
            logs.removeFirst(logs.count - EdgehopBridge.logKeep)
        }
        if let object = try? jsonObject(entry) {
            bridge.emit("edgehop.log", object)
        }
    }

    func log(_ level: String, _ message: String) {
        switch level {
        case "warn", "error": Log.error(message, category: "transport")
        case "debug": Log.debug(message, category: "transport")
        default: Log.info(message, category: "transport")
        }
        addLog(LogEntry(level: level, msg: message))
    }

    /// A transport event (`edgehop.pairing`, `edgehop.pairCode`, `edgehop.pairResult`).
    func handleTransportEvent(_ name: String, _ data: Any) {
        switch name {
        case "edgehop.pairing":
            guard let state = data as? PairingState else { return }
            pairing = state
            emitCodable(name, state)
            emitState()
        case "edgehop.pairCode":
            guard let code = data as? PairCode else { return }
            log("info", "pairing request from \(code.peer.name): code shown in the window")
            emitCodable(name, code)
            notifyPairingRequest(from: code.peer.name)
        case "edgehop.pairResult":
            guard let result = data as? PairResult else { return }
            emitCodable(name, result)
        default:
            break
        }
    }

    private func emitCodable<T: Encodable>(_ name: String, _ value: T) {
        do {
            bridge.emit(name, try jsonObject(value))
        } catch {
            Log.error("event \(name): \(error)", category: "bridge")
        }
    }

    /// A pairing request is worth a bounce even when the window is closed.
    private func notifyPairingRequest(from name: String) {
        _ = NSApp.requestUserAttention(.informationalRequest)
    }

    // MARK: - Settings panes

    /// `app.openSettingsPane {pane}`: the privacy pane a permission badge points at, or
    /// Login Items for "startup".
    static func openSettingsPane(_ pane: String) {
        switch pane {
        case "accessibility":
            Permissions.openSettingsPane(.accessibility)
        case "inputMonitoring":
            Permissions.openSettingsPane(.inputMonitoring)
        case "screenRecording":
            Permissions.openSettingsPane(.screenRecording)
        case "startup", "login", "loginitems":
            LaunchAtLogin.openLoginItemsSettings()
        default:
            if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security") {
                NSWorkspace.shared.open(url)
            }
        }
    }
}
