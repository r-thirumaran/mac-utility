#if canImport(WebKit)
import AppKit
import WebKit

/// A resizable window hosting the app's web UI.
///
/// The `WKWebView` loads `app://ui/index.html` through `AppSchemeHandler`, has the
/// `NativeBridge` installed, follows the system appearance, hides the right-click menu (the
/// UI is an app, not a web page) and remembers its frame across launches.
@MainActor
final class WebWindowController: NSWindowController {

    let bridge: NativeBridge
    let webView: WKWebView
    private let schemeHandler: AppSchemeHandler

    private static let contextMenuScript = """
        document.addEventListener('contextmenu', function (e) { e.preventDefault(); }, true);
        """

    init(title: String,
         size: NSSize,
         uiRoot: URL,
         bridge: NativeBridge,
         minSize: NSSize = NSSize(width: 720, height: 480)) {
        let schemeHandler = AppSchemeHandler(root: uiRoot)

        let configuration = WKWebViewConfiguration()
        configuration.setURLSchemeHandler(schemeHandler, forURLScheme: AppSchemeHandler.scheme)
        configuration.userContentController.addUserScript(WKUserScript(
            source: WebWindowController.contextMenuScript,
            injectionTime: .atDocumentStart,
            forMainFrameOnly: false))
        bridge.attach(to: configuration)

        let webView = WKWebView(frame: NSRect(origin: .zero, size: size), configuration: configuration)
        webView.autoresizingMask = [.width, .height]
        // Match the window chrome in both appearances while the page is loading.
        webView.underPageBackgroundColor = NSColor.windowBackgroundColor
        if #available(macOS 13.3, *) {
            webView.isInspectable = true
        }

        let window = NSWindow(
            contentRect: NSRect(origin: .zero, size: size),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false)
        window.title = title
        window.minSize = minSize
        window.isReleasedWhenClosed = false
        window.contentView = webView
        window.center()

        self.bridge = bridge
        self.webView = webView
        self.schemeHandler = schemeHandler
        super.init(window: window)

        // Restores the last frame if one was saved; the centred frame above is the default.
        _ = window.setFrameAutosaveName("WebWindow." + title)
        bridge.webView = webView
        load()
    }

    required init?(coder: NSCoder) {
        fatalError("WebWindowController does not support NSCoding")
    }

    // MARK: - Presentation

    /// Brings the window (and the app) to the front.
    func show() {
        NSApp.activate(ignoringOtherApps: true)
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }

    func reload() {
        load()
    }

    private func load() {
        guard let url = AppSchemeHandler.url(forPath: "/index.html") else { return }
        webView.load(URLRequest(url: url))
    }
}
#endif
