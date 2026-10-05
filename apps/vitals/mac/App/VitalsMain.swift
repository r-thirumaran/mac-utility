import AppKit

/// Entry point.
///
/// Built by hand rather than with `@NSApplicationMain` so the activation policy is set to
/// `.accessory` *before* the run loop starts. Together with `LSUIElement` in Info.plist that
/// guarantees no Dock icon flashes up during launch; the window the UI lives in is shown on
/// demand from the menu bar item.
@main
enum VitalsMain {

    /// `NSApplication.delegate` is a weak reference, so the delegate has to be owned by
    /// something that outlives `main()`.
    private static let delegate = AppDelegate()

    static func main() {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        app.delegate = delegate
        app.run()
    }
}
