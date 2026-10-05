import AppKit

/// The menu bar item and its dropdown.
///
/// The menu is rebuilt in `menuNeedsUpdate` rather than kept in sync as history changes:
/// it is only ever read at the instant it opens, so rebuilding a dozen items on demand is
/// far simpler than maintaining incremental updates against a list that churns constantly.
final class StatusItemController: NSObject, NSMenuDelegate {

    /// How many recents the dropdown shows before deferring to the full panel.
    private let recentCount = 10

    private let statusItem: NSStatusItem
    private let store: ClipboardStore
    private let preferences: Preferences

    var onOpenHistory: (() -> Void)?
    var onOpenSettings: (() -> Void)?
    var onPaste: ((ClipItem) -> Void)?
    var onClearHistory: (() -> Void)?

    init(store: ClipboardStore, preferences: Preferences = .shared) {
        self.store = store
        self.preferences = preferences
        self.statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        super.init()

        if let button = statusItem.button {
            // A template image tracks light/dark menu bars automatically.
            let image = NSImage(systemSymbolName: "rectangle.split.2x1",
                                accessibilityDescription: "Clipwell")
            image?.isTemplate = true
            button.image = image
            button.toolTip = "Clipwell — window snapping & clipboard history"
        }

        let menu = NSMenu()
        menu.delegate = self
        statusItem.menu = menu
    }

    // MARK: - Menu construction

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()

        let recents = Array(store.items.prefix(recentCount))
        if recents.isEmpty {
            let empty = NSMenuItem(title: "No clipboard history", action: nil, keyEquivalent: "")
            empty.isEnabled = false
            menu.addItem(empty)
        } else {
            let header = NSMenuItem(title: "Recent", action: nil, keyEquivalent: "")
            header.isEnabled = false
            menu.addItem(header)

            for item in recents {
                let menuItem = NSMenuItem(title: item.menuTitle(),
                                          action: #selector(pasteRecent(_:)),
                                          keyEquivalent: "")
                menuItem.target = self
                menuItem.representedObject = item.id
                if item.isPinned {
                    menuItem.image = NSImage(systemSymbolName: "pin.fill",
                                             accessibilityDescription: "Pinned")
                } else if let icon = store.icon(forBundleID: item.sourceAppBundleID) {
                    icon.size = NSSize(width: 16, height: 16)
                    menuItem.image = icon
                } else {
                    menuItem.image = NSImage(systemSymbolName: item.kind.symbolName,
                                             accessibilityDescription: nil)
                }
                menu.addItem(menuItem)
            }
        }

        menu.addItem(.separator())

        let open = NSMenuItem(title: "Open Clipboard History",
                              action: #selector(openHistory),
                              keyEquivalent: preferences.showClipboard.menuKeyEquivalent)
        open.keyEquivalentModifierMask = preferences.showClipboard.cocoaModifiers
        open.target = self
        menu.addItem(open)

        let clear = NSMenuItem(title: "Clear History (Keeps Pinned)",
                               action: #selector(clearHistory),
                               keyEquivalent: "")
        clear.target = self
        clear.isEnabled = store.items.contains { !$0.isPinned }
        menu.addItem(clear)

        menu.addItem(.separator())

        let settings = NSMenuItem(title: "Settings…",
                                  action: #selector(openSettings),
                                  keyEquivalent: ",")
        settings.target = self
        menu.addItem(settings)

        let quit = NSMenuItem(title: "Quit Clipwell",
                              action: #selector(quit),
                              keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)
    }

    // MARK: - Actions

    @objc private func pasteRecent(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? UUID,
              let item = store.items.first(where: { $0.id == id }) else { return }
        onPaste?(item)
    }

    @objc private func openHistory() { onOpenHistory?() }
    @objc private func openSettings() { onOpenSettings?() }
    @objc private func clearHistory() { onClearHistory?() }
    @objc private func quit() { NSApp.terminate(nil) }
}
