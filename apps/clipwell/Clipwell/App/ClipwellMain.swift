import AppKit

/// Entry point.
///
/// Built by hand rather than with `@NSApplicationMain` / the SwiftUI `App` lifecycle so the
/// activation policy is set to `.accessory` *before* the run loop starts. Combined with
/// `LSUIElement` in Info.plist, that guarantees no Dock icon and no menu bar ever flashes up
/// during launch.
@main
enum ClipwellMain {

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
