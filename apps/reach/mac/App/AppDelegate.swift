import AppKit
import WebKit

/// Owns every long-lived object and wires them together: the config store, the agent
/// (`ReachAPI`), the WKWebView bridge, the notifier, the menu bar item and the settings
/// window. Deliberately the only place in the app where the pieces know about each other.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate, NSWindowDelegate {

    private static let windowSize = NSSize(width: 1000, height: 700)
    private static let windowMinSize = NSSize(width: 720, height: 520)
    private static let pageProbeInterval: TimeInterval = 0.1
    private static let pageProbeLimit = 100

    private let bridge: NativeBridge
    private let notifier = Notifier()
    private let pairings: Pairings
    private let api: ReachAPI
    private lazy var reachBridge = ReachBridge(bridge: bridge, api: api)
    private lazy var statusItem = StatusItemHost(
        image: NSImage(systemSymbolName: "iphone.radiowaves.left.and.right", accessibilityDescription: "Reach"),
        title: nil)

    private var window: WebWindowController?
    private var launchAtLoginItem: NSMenuItem?
    private var pageProbe: DispatchSourceTimer?
    private var pageProbeAttempts = 0

    override init() {
        let version = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.0"
        bridge = NativeBridge()
        pairings = Pairings(path: Pairings.defaultPath(), defaultName: Host.current().localizedName ?? "Mac")
        api = ReachAPI(pairings: pairings, version: version)
        super.init()
    }

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        api.onLocalEvent = { [weak self] event, data in
            self?.bridge.emit(event, data)
        }
        api.onNotify = { [weak self] title, body in
            self?.notifier.post(title: title, body: body)
        }
        api.onLaunchAtLoginChanged = { [weak self] in
            self?.refreshLaunchAtLoginItem()
        }
        reachBridge.registerAll()
        configureStatusItem()
        syncLaunchAtLogin()
        notifier.requestAuthorization()
        api.start()

        // First-launch onboarding: the system prompts (shown once per app version) plus the
        // settings window, which lists the switches and the addresses phones can use.
        let needsPermissions = !Permissions.accessibilityGranted || !Permissions.screenRecordingGranted
        if needsPermissions {
            requestPermissions()
        }
        if needsPermissions || pairings.snapshot.pairings.isEmpty {
            showWindow()
        }
        Log.info("Reach \(api.version) started on port \(api.port)", category: "app")
    }

    func applicationWillTerminate(_ notification: Notification) {
        pageProbe?.cancel()
        api.stop()
    }

    /// Opening the app again (Finder, Launchpad) brings the window back.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showWindow()
        return false
    }

    // MARK: - Permissions

    private func requestPermissions() {
        if !Permissions.accessibilityGranted {
            Permissions.requestAccessibility()
        }
        if !Permissions.screenRecordingGranted {
            Permissions.requestScreenRecording()
        }
    }

    // MARK: - Status item

    private func configureStatusItem() {
        statusItem.setToolTip("Reach — control this Mac from your phone")
        statusItem.addItem(title: "Open Reach", key: "o") { [weak self] in
            self?.showWindow()
        }
        statusItem.addItem(title: "Show pairing code") { [weak self] in
            self?.showPairingCode()
        }
        statusItem.addSeparator()
        launchAtLoginItem = statusItem.addItem(title: "Launch at Login") { [weak self] in
            self?.toggleLaunchAtLogin()
        }
        statusItem.addSeparator()
        statusItem.addItem(title: "Quit Reach", key: "q") {
            NSApp.terminate(nil)
        }
        statusItem.menu.delegate = self
        refreshLaunchAtLoginItem()
    }

    /// `NSMenuDelegate`: the login-item state can change behind our back in System Settings.
    func menuNeedsUpdate(_ menu: NSMenu) {
        refreshLaunchAtLoginItem()
    }

    /// Generates (or reuses) the code, notifies, pushes `reach.pairCode` to the settings
    /// page and opens it so the code is visible even with notifications off.
    private func showPairingCode() {
        do {
            _ = try api.showCodeLocally()
        } catch {
            notifier.post(title: "Reach", body: ReachError.wrap(error).message)
        }
        showWindow()
    }

    private func toggleLaunchAtLogin() {
        do {
            try api.setLaunchAtLogin(!LaunchAtLogin.isEnabled)
        } catch {
            Log.error("launch at login: \(error)", category: "app")
        }
        refreshLaunchAtLoginItem()
    }

    private func refreshLaunchAtLoginItem() {
        launchAtLoginItem?.state = LaunchAtLogin.isEnabled ? .on : .off
    }

    /// The system is the source of truth for the login item; the config mirrors it.
    private func syncLaunchAtLogin() {
        let enabled = LaunchAtLogin.isEnabled
        if pairings.snapshot.launchAtLogin != enabled {
            pairings.update { $0.launchAtLogin = enabled }
        }
    }

    // MARK: - Window

    private func showWindow() {
        if window == nil {
            guard let uiRoot = Bundle.main.resourceURL?.appendingPathComponent("ui") else {
                Log.error("no ui folder in the bundle", category: "app")
                return
            }
            let controller = WebWindowController(title: "Reach",
                                                 size: AppDelegate.windowSize,
                                                 uiRoot: uiRoot,
                                                 bridge: bridge,
                                                 minSize: AppDelegate.windowMinSize)
            controller.window?.delegate = self
            window = controller
        }
        window?.show()
        api.localListening = true
        probePage()
    }

    /// `NSWindowDelegate`: a closed window keeps its page, but pushes stop until it is shown again.
    func windowWillClose(_ notification: Notification) {
        api.localListening = false
        pageProbe?.cancel()
        pageProbe = nil
    }

    /// The PWA flips its status to online on the socket transport's `native.connected`
    /// event; the WKWebView transport has no socket, so once app.js has installed its
    /// listeners (it defines `window.__reach` last) the app pushes that event itself, then
    /// the `info` the page expects first and any outstanding pairing code.
    private func probePage() {
        pageProbe?.cancel()
        pageProbeAttempts = 0
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + AppDelegate.pageProbeInterval,
                       repeating: AppDelegate.pageProbeInterval,
                       leeway: .milliseconds(20))
        timer.setEventHandler { [weak self] in
            MainActor.assumeIsolated {
                self?.probePageOnce()
            }
        }
        timer.resume()
        pageProbe = timer
    }

    private func probePageOnce() {
        pageProbeAttempts += 1
        guard pageProbeAttempts <= AppDelegate.pageProbeLimit, let webView = window?.webView else {
            pageProbe?.cancel()
            pageProbe = nil
            return
        }
        webView.evaluateJavaScript("typeof window.__reach") { [weak self] value, _ in
            guard (value as? String) == "object" else { return }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.pageDidBecomeReady()
                }
            }
        }
    }

    private func pageDidBecomeReady() {
        guard pageProbe != nil else { return }
        pageProbe?.cancel()
        pageProbe = nil
        bridge.emit("native.connected", ["url": "wkwebview"])
        bridge.emit("info", api.infoFull())
        api.resendOutstandingCode()
    }
}
