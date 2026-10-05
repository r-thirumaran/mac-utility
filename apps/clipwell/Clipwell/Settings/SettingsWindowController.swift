import AppKit
import SwiftUI

/// Hosts `SettingsView` in a plain `NSWindow`.
///
/// SwiftUI's `Settings` scene is not usable here: it belongs to the SwiftUI `App` lifecycle,
/// and Clipwell runs an AppKit `NSApplication` so it can be `LSUIElement` and own its status
/// item directly.
final class SettingsWindowController: NSWindowController {

    convenience init(preferences: Preferences, store: ClipboardStore) {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 520, height: 440),
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered,
            defer: false)
        window.title = "Clipwell Settings"
        window.isReleasedWhenClosed = false
        window.center()
        self.init(window: window)

        window.contentView = NSHostingView(
            rootView: SettingsView(preferences: preferences, store: store))
    }

    func present() {
        // A background-only app has to activate explicitly, or the window opens behind
        // whatever the user was doing and the shortcut recorders never get key events.
        NSApp.activate()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }
}
