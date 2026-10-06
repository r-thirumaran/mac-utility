import AppKit

/// Holds one menu closure behind an Objective-C selector, for the items of the "Switch to"
/// submenu (the shared `StatusItemHost` only wraps the items of its own top-level menu).
@MainActor
final class MenuActionTarget: NSObject {

    private let action: () -> Void

    init(action: @escaping () -> Void) {
        self.action = action
        super.init()
    }

    @objc func fire(_ sender: Any?) {
        action()
    }
}
