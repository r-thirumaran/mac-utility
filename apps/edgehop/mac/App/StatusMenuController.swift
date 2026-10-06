import AppKit

/// The menu bar item: "Open Edgehop", a "Switch to ▸" submenu with every paired machine and
/// "This Mac", "Launch at Login" and "Quit". While control is on another machine the title
/// shows "⌨ <its name>". The menu is rebuilt each time it opens, so it always reflects the
/// node's state.
@MainActor
final class StatusMenuController: NSObject, NSMenuDelegate {

    var onOpen: (() -> Void)?

    private let node: Node
    private let statusItem: StatusItemHost
    private var targets: [MenuActionTarget] = []

    init(node: Node) {
        self.node = node
        statusItem = StatusItemHost(
            image: NSImage(systemSymbolName: "rectangle.on.rectangle.angled", accessibilityDescription: "Edgehop"),
            title: nil)
        super.init()
        statusItem.menu.delegate = self
        rebuild()
        refresh()
    }

    // MARK: - State

    /// Updates the title and tooltip from the node's state.
    func refresh() {
        let state = node.state()
        if state.selfMachine.active {
            statusItem.setTitle(nil)
            statusItem.setToolTip("Edgehop — \(state.selfMachine.name)")
        } else {
            let name = state.peers.first(where: { $0.active })?.name ?? String(state.active.prefix(8))
            statusItem.setTitle("⌨ " + name)
            statusItem.setToolTip("Edgehop — controlling \(name)")
        }
    }

    /// `NSMenuDelegate`: the peer list and the login-item state change behind our back.
    func menuNeedsUpdate(_ menu: NSMenu) {
        guard menu === statusItem.menu else { return }
        rebuild()
    }

    // MARK: - Menu

    private func rebuild() {
        statusItem.removeAllItems()
        targets.removeAll()

        statusItem.addItem(title: "Open Edgehop", key: "o") { [weak self] in
            self?.onOpen?()
        }
        statusItem.addSeparator()

        let switchItem = statusItem.addItem(title: "Switch to") {}
        switchItem.submenu = switchMenu()
        statusItem.addSeparator()

        let loginItem = statusItem.addItem(title: "Launch at Login") { [weak self] in
            self?.toggleLaunchAtLogin()
        }
        loginItem.state = LaunchAtLogin.isEnabled ? .on : .off
        statusItem.addSeparator()

        statusItem.addItem(title: "Quit Edgehop", key: "q") {
            NSApp.terminate(nil)
        }
    }

    /// "This Mac" (claim) and one entry per paired machine, disabled while offline or active.
    private func switchMenu() -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        let state = node.state()
        let selfID = node.id
        add(to: menu, title: "This Mac", enabled: !state.selfMachine.active, checked: state.selfMachine.active) { [weak self] in
            self?.claim(selfID)
        }
        let peers = state.peers.filter { $0.paired }.sorted { $0.name < $1.name }
        if !peers.isEmpty {
            menu.addItem(.separator())
        }
        for peer in peers {
            let id = peer.id
            let title = peer.status == "online" || peer.active ? peer.name : peer.name + " (offline)"
            add(to: menu, title: title, enabled: peer.status == "online" && !peer.active, checked: peer.active) { [weak self] in
                self?.claim(id)
            }
        }
        return menu
    }

    private func add(to menu: NSMenu, title: String, enabled: Bool, checked: Bool, action: @escaping () -> Void) {
        let target = MenuActionTarget(action: action)
        targets.append(target)
        let item = NSMenuItem(title: title, action: #selector(MenuActionTarget.fire(_:)), keyEquivalent: "")
        item.target = target
        item.isEnabled = enabled
        item.state = checked ? .on : .off
        menu.addItem(item)
    }

    private func claim(_ id: String) {
        do {
            try node.claim(id)
        } catch {
            Log.error("switch: \(error)")
        }
    }

    private func toggleLaunchAtLogin() {
        do {
            try LaunchAtLogin.setEnabled(!LaunchAtLogin.isEnabled)
        } catch {
            Log.error("launch at login: \(error)")
        }
        if LaunchAtLogin.requiresApproval {
            LaunchAtLogin.openLoginItemsSettings()
        }
    }
}
