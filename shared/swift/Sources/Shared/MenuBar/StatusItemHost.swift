#if canImport(AppKit)
import AppKit

/// An `NSStatusItem` with a closure-driven menu and an optional live title (Vitals shows CPU
/// load there).
///
/// `NSMenuItem` wants a target/selector; each closure is wrapped in a small `NSObject` target
/// that the host retains (the menu item's `target` is weak).
@MainActor
final class StatusItemHost {

    /// The dropdown. Replace it wholesale or build it with `addItem` / `addSeparator`.
    var menu: NSMenu {
        didSet { statusItem.menu = menu }
    }

    private let statusItem: NSStatusItem
    private var targets: [MenuItemTarget] = []

    init(image: NSImage?, title: String?) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        menu = NSMenu()
        statusItem.menu = menu
        setImage(image)
        setTitle(title)
    }

    // MARK: - Appearance

    /// Text shown next to the icon; `nil` or empty hides it.
    func setTitle(_ title: String?) {
        guard let button = statusItem.button else { return }
        button.title = title ?? ""
        button.imagePosition = button.image == nil ? .noImage : (button.title.isEmpty ? .imageOnly : .imageLeading)
    }

    /// A template image tracks light/dark menu bars automatically.
    func setImage(_ image: NSImage?) {
        guard let button = statusItem.button else { return }
        button.image = image
        button.imagePosition = image == nil ? .noImage : (button.title.isEmpty ? .imageOnly : .imageLeading)
    }

    func setToolTip(_ toolTip: String?) {
        statusItem.button?.toolTip = toolTip
    }

    // MARK: - Menu construction

    /// Appends an item that runs `action` when chosen. `key` is the key equivalent (`"q"`,
    /// `","`), shown with ⌘.
    @discardableResult
    func addItem(title: String, key: String = "", action: @escaping () -> Void) -> NSMenuItem {
        let target = MenuItemTarget(action: action)
        targets.append(target)
        let item = NSMenuItem(title: title, action: #selector(MenuItemTarget.fire(_:)), keyEquivalent: key)
        item.target = target
        menu.addItem(item)
        return item
    }

    func addSeparator() {
        menu.addItem(.separator())
    }

    func removeAllItems() {
        menu.removeAllItems()
        targets.removeAll()
    }

    /// Takes the item out of the menu bar.
    func remove() {
        NSStatusBar.system.removeStatusItem(statusItem)
    }
}

/// Holds one menu closure behind an Objective-C selector.
@MainActor
private final class MenuItemTarget: NSObject {

    private let action: () -> Void

    init(action: @escaping () -> Void) {
        self.action = action
        super.init()
    }

    @objc func fire(_ sender: Any?) {
        action()
    }
}
#endif
