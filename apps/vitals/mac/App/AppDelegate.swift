import AppKit

/// Owns every long-lived object and wires them together: the sampler, the bridge, the menu
/// bar item and the web window. Deliberately the only place in the app where the pieces know
/// about each other.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate, NSWindowDelegate {

    private static let windowSize = NSSize(width: 1040, height: 720)
    private static let windowMinSize = NSSize(width: 820, height: 560)
    /// The menu bar title is refreshed at this cadence whatever the sampling interval.
    private static let titleInterval: TimeInterval = 2

    private let bridge = NativeBridge()
    private let sampler = SystemSampler()
    private lazy var vitalsBridge = VitalsBridge(bridge: bridge, sampler: sampler)
    private lazy var statusItem = StatusItemHost(
        image: NSImage(systemSymbolName: "gauge", accessibilityDescription: "Vitals"),
        title: nil)

    private var window: WebWindowController?
    private var launchAtLoginItem: NSMenuItem?
    private var lastTitleUpdate: TimeInterval = 0

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        vitalsBridge.registerAll()
        configureStatusItem()

        sampler.onSample = { [weak self] sample, live in
            guard let self else { return }
            if live {
                self.bridge.emit("vitals.sample", sample)
            }
            self.updateTitle(from: sample)
        }
        sampler.start()

        // First launch opens the window; afterwards it lives behind the menu bar item.
        showWindow()
    }

    func applicationWillTerminate(_ notification: Notification) {
        sampler.stop()
    }

    /// Opening the app again (Finder, Launchpad) brings the window back.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showWindow()
        return false
    }

    // MARK: - Status item

    private func configureStatusItem() {
        statusItem.setToolTip("Vitals — CPU, memory, GPU, processes and power")
        statusItem.addItem(title: "Open Vitals", key: "o") { [weak self] in
            self?.showWindow()
        }
        statusItem.addSeparator()
        launchAtLoginItem = statusItem.addItem(title: "Launch at Login") { [weak self] in
            self?.toggleLaunchAtLogin()
        }
        statusItem.addSeparator()
        statusItem.addItem(title: "Quit Vitals", key: "q") {
            NSApp.terminate(nil)
        }
        statusItem.menu.delegate = self
        refreshLaunchAtLoginItem()
    }

    /// `NSMenuDelegate`: the login-item state can change behind our back in System Settings.
    func menuNeedsUpdate(_ menu: NSMenu) {
        refreshLaunchAtLoginItem()
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
        refreshLaunchAtLoginItem()
    }

    private func refreshLaunchAtLoginItem() {
        launchAtLoginItem?.state = LaunchAtLogin.isEnabled ? .on : .off
    }

    /// "CPU 27% · 9.6 GB", at most every two seconds.
    private func updateTitle(from sample: SystemSampler.Sample) {
        let now = ProcessInfo.processInfo.systemUptime
        guard now - lastTitleUpdate >= AppDelegate.titleInterval - 0.1 else { return }
        lastTitleUpdate = now
        let cpu = (sample["cpu"] as? [String: Any])?["total"] as? Double ?? 0
        let used = (sample["mem"] as? [String: Any])?["used"] as? UInt64 ?? 0
        let gigabytes = Double(used) / 1_073_741_824
        statusItem.setTitle(String(format: "CPU %d%% · %.1f GB", Int(cpu.rounded()), gigabytes))
    }

    // MARK: - Window

    private func showWindow() {
        if window == nil {
            guard let uiRoot = Bundle.main.resourceURL?.appendingPathComponent("ui") else {
                Log.error("no ui folder in the bundle")
                return
            }
            let controller = WebWindowController(title: "Vitals",
                                                 size: AppDelegate.windowSize,
                                                 uiRoot: uiRoot,
                                                 bridge: bridge,
                                                 minSize: AppDelegate.windowMinSize)
            controller.window?.delegate = self
            window = controller
        }
        window?.show()
        sampler.setPageVisible(true)
    }

    /// `NSWindowDelegate`: a closed window keeps its page (and its subscription) but samples
    /// stop flowing to it until it is shown again.
    func windowWillClose(_ notification: Notification) {
        sampler.setPageVisible(false)
    }
}
