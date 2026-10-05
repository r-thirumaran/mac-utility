import AppKit

/// Owns every long-lived object and wires them together. Deliberately the only place in the
/// app where the pieces know about each other — everything else takes its collaborators
/// through `init`.
final class AppDelegate: NSObject, NSApplicationDelegate {

    private let preferences = Preferences.shared
    private let store = ClipboardStore()
    private let tracker = FrontmostAppTracker()
    private let windowManager = WindowManager()

    private lazy var monitor = ClipboardMonitor(store: store)
    private lazy var screenshotWatcher = ScreenshotWatcher(store: store)
    private lazy var panelController = ClipboardPanelController(store: store, tracker: tracker)
    private lazy var statusItemController = StatusItemController(store: store)

    private var settingsWindow: SettingsWindowController?
    private var permissionWindow: PermissionWindowController?

    /// Sweeps expired items for a Mac that stays logged in for weeks — otherwise a 7-day
    /// retention would only ever be applied at launch.
    private var retentionTimer: DispatchSourceTimer?

    private var observers: [NSObjectProtocol] = []

    // MARK: - Lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        store.load()

        panelController.monitor = monitor
        monitor.start()

        // Covers ⌘⇧3/4/5, which save a file and never touch the pasteboard.
        screenshotWatcher.monitor = monitor
        screenshotWatcher.start()

        configureStatusItem()
        registerHotKeys()
        observeNotifications()
        startRetentionSweep()

        requestAccessibilityIfNeeded()
    }

    func applicationWillTerminate(_ notification: Notification) {
        // Beat the 0.5 s save debounce so a quit never loses the last copy.
        store.flush()
        monitor.stop()
        screenshotWatcher.stop()
        retentionTimer?.cancel()
        HotKeyManager.shared.unregisterAll()
        observers.forEach { NotificationCenter.default.removeObserver($0) }
    }

    // MARK: - Wiring

    private func configureStatusItem() {
        statusItemController.onOpenHistory = { [weak self] in
            self?.panelController.show()
        }
        statusItemController.onOpenSettings = { [weak self] in
            self?.showSettings()
        }
        statusItemController.onPaste = { [weak self] item in
            self?.panelController.performPaste(item, plainTextOnly: false)
        }
        statusItemController.onClearHistory = { [weak self] in
            self?.store.clearUnpinned()
        }
    }

    private func observeNotifications() {
        observers.append(NotificationCenter.default.addObserver(
            forName: Preferences.hotKeysDidChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            // The user is in Settings looking at the shortcut they just set, so a conflict
            // alert is useful here rather than noise.
            self?.registerHotKeys(reportFailures: true)
        })

        observers.append(NotificationCenter.default.addObserver(
            forName: Preferences.didChangeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            guard let self else { return }
            // History cap / retention may have tightened.
            self.store.enforceLimits()
            self.store.flush()
        })

        observers.append(NotificationCenter.default.addObserver(
            forName: Preferences.screenshotSettingsDidChange,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            self?.screenshotWatcher.restart()
        })

        observers.append(NotificationCenter.default.addObserver(
            forName: .clipWellNeedsAccessibility,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            self?.showPermissionWindow()
        })
    }

    // MARK: - Hot keys

    private func registerHotKeys(reportFailures: Bool = false) {
        let failures = HotKeyManager.shared.register(preferences.hotKeyBindings) { [weak self] action in
            self?.handle(action)
        }
        guard !failures.isEmpty else { return }

        let names = failures.map(\.title).joined(separator: ", ")
        NSLog("Clipwell: hot keys already taken by another app: \(names)")

        // Only interrupt when the user just changed a shortcut and is looking at Settings —
        // an alert at launch would be noise they cannot act on in context.
        guard reportFailures else { return }
        let alert = NSAlert()
        alert.messageText = "Some shortcuts could not be registered"
        alert.informativeText = """
            \(names)

            Another app — or macOS itself — already owns that key combination. \
            Recent macOS versions bind several ⌃⌘ and fn-based arrow shortcuts for their own \
            window tiling; check System Settings ▸ Keyboard ▸ Keyboard Shortcuts, or pick a \
            different combination here.
            """
        alert.alertStyle = .warning
        alert.runModal()
    }

    private func handle(_ action: HotKeyAction) {
        switch action {
        case .snapLeft, .cmdArrowLeft:
            windowManager.perform(.left)
        case .snapRight, .cmdArrowRight:
            windowManager.perform(.right)
        case .maximize:
            windowManager.perform(.maximize)
        case .restoreOrMinimize:
            windowManager.perform(.restoreOrMinimize)
        case .showClipboard:
            panelController.toggle()
        }
    }

    // MARK: - Retention

    private func startRetentionSweep() {
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 3600, repeating: 3600, leeway: .seconds(60))
        timer.setEventHandler { [weak self] in
            guard let self else { return }
            self.store.enforceLimits()
            self.store.flush()
        }
        timer.resume()
        retentionTimer = timer
    }

    // MARK: - Windows

    private func showSettings() {
        if settingsWindow == nil {
            settingsWindow = SettingsWindowController(preferences: preferences, store: store)
        }
        settingsWindow?.present()
    }

    private func showPermissionWindow() {
        guard !AccessibilityPermission.isTrusted else { return }
        if permissionWindow == nil {
            permissionWindow = PermissionWindowController()
        }
        permissionWindow?.present()
    }

    /// First-launch permission flow: fire the system prompt (which macOS shows at most once
    /// per app version) *and* open our own helper window, which can be reopened forever after.
    private func requestAccessibilityIfNeeded() {
        guard !AccessibilityPermission.isTrusted else { return }
        AccessibilityPermission.requestTrust()
        showPermissionWindow()
    }
}
