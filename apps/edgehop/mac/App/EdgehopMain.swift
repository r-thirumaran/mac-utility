import AppKit

/// Entry point.
///
/// Built by hand rather than with `@NSApplicationMain` so the activation policy is set to
/// `.accessory` *before* the run loop starts. Together with `LSUIElement` in Info.plist that
/// guarantees no Dock icon flashes up during launch; the window the UI lives in is shown on
/// demand from the menu bar item.
@main
enum EdgehopMain {

    /// `NSApplication.delegate` is a weak reference, so the delegate has to be owned by
    /// something that outlives `main()`.
    @MainActor private static var delegate: AppDelegate?

    static func main() {
        // `main` runs on the main thread; the delegate and NSApplication are main-actor types.
        MainActor.assumeIsolated {
            let app = NSApplication.shared
            let delegate = AppDelegate()
            EdgehopMain.delegate = delegate
            app.setActivationPolicy(.accessory)
            app.delegate = delegate
            app.run()
        }
    }
}
