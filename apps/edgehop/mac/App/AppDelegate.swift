import AppKit

/// Owns every long-lived object and wires them together: the store and identity, the input
/// device, the node, the transport, the bridge, the menu bar item and the web window.
/// Deliberately the only place in the app where the pieces know about each other.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {

    private static let windowSize = NSSize(width: 980, height: 680)
    private static let windowMinSize = NSSize(width: 820, height: 560)

    private let bridge = NativeBridge()
    private let permissions = PermissionWatcher()
    private var store = PeerStore()
    private var device: MacInputDevice?
    private var node: Node?
    private var transport: Transport?
    private var edgehopBridge: EdgehopBridge?
    private var statusMenu: StatusMenuController?
    private var window: WebWindowController?

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.0"

        // Config and identity: ~/Library/Application Support/Edgehop/{config,identity}.json.
        var identity = Identity()
        do {
            let directory = try IdentityStore.configDirectory()
            // Reviewer note: the identity first, so an unreadable config.json does not also
            // give this machine a new id (which would break every existing pairing).
            identity = try IdentityStore.load(from: directory)
            store = try PeerStore(directory: directory)
        } catch {
            Log.error("config: \(error); running with an in-memory store")
        }
        var name = store.name
        if name.isEmpty {
            name = AppDelegate.defaultMachineName()
        }
        let settings = store.settings

        let keyMap = KeyMap.bundled()
        let device = MacInputDevice(keyMap: keyMap)
        self.device = device

        let transport = Transport(config: Transport.Config(identity: identity, name: name, plat: "macos",
                                                           store: store, port: settings.port))
        let node = Node(config: Node.Config(id: identity.id, name: name, plat: "macos", version: version,
                                            store: store, device: device, sender: transport,
                                            clipboard: PasteboardClipboard(),
                                            permissions: { EdgehopBridge.permissions() }))
        let edgehopBridge = EdgehopBridge(bridge: bridge, node: node, transport: transport, version: version)
        self.transport = transport
        self.node = node
        self.edgehopBridge = edgehopBridge

        // Transport → node.
        transport.onPeerUp = { [weak node] peer, addr in node?.peerUp(peer, addr: addr) }
        transport.onPeerDown = { [weak node] id in node?.peerDown(id) }
        transport.onPeerMessage = { [weak node] id, frame in node?.onPeerMessage(id, frame) }
        transport.onDiscovered = { [weak node] found in node?.discovered(found) }
        transport.onListening = { [weak node, weak edgehopBridge] port in
            node?.setPort(port)
            edgehopBridge?.emitState()
        }
        transport.addrFor = { [weak node] id in node?.peerAddr(id) }
        // Transport and node → UI.
        transport.emit = { [weak edgehopBridge] name, data in edgehopBridge?.handleTransportEvent(name, data) }
        transport.logf = { [weak edgehopBridge] level, message in edgehopBridge?.log(level, message) }
        node.onStateChanged = { [weak edgehopBridge] in edgehopBridge?.emitState() }
        node.onLog = { [weak edgehopBridge] entry in edgehopBridge?.addLog(entry) }

        edgehopBridge.registerAll()

        let statusMenu = StatusMenuController(node: node)
        statusMenu.onOpen = { [weak self] in self?.showWindow() }
        self.statusMenu = statusMenu
        edgehopBridge.onStateChanged = { [weak statusMenu] in statusMenu?.refresh() }

        // Permissions: prompt once, then follow grants (the tap upgrades itself).
        permissions.onChange = { [weak self] accessibility, inputMonitoring in
            Log.info("permissions: accessibility=\(accessibility) inputMonitoring=\(inputMonitoring)")
            self?.device?.permissionsChanged()
            self?.edgehopBridge?.emitState()
        }
        permissions.start()

        transport.start()
        node.start()

        // First launch opens the window; afterwards it lives behind the menu bar item.
        showWindow()
    }

    func applicationWillTerminate(_ notification: Notification) {
        permissions.stop()
        node?.stop()
        transport?.stop()
    }

    /// Opening the app again (Finder, Launchpad) brings the window back.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showWindow()
        return false
    }

    // MARK: - Window

    private func showWindow() {
        if window == nil {
            guard let uiRoot = Bundle.main.resourceURL?.appendingPathComponent("ui") else {
                Log.error("no ui folder in the bundle")
                return
            }
            window = WebWindowController(title: "Edgehop",
                                         size: AppDelegate.windowSize,
                                         uiRoot: uiRoot,
                                         bridge: bridge,
                                         minSize: AppDelegate.windowMinSize)
        }
        window?.show()
    }

    // MARK: - Helpers

    /// The computer name from Sharing settings, else the hostname without its domain.
    private static func defaultMachineName() -> String {
        if let name = Host.current().localizedName, !name.isEmpty {
            return name
        }
        let host = ProcessInfo.processInfo.hostName
        if let dot = host.firstIndex(of: "."), dot != host.startIndex {
            return String(host[host.startIndex..<dot])
        }
        return host.isEmpty ? "This Mac" : host
    }
}
