import AppKit
import Combine

/// How long clipboard items are kept before they are swept.
enum Retention: String, Codable, CaseIterable, Identifiable {
    case forever
    case days30
    case days7

    var id: String { rawValue }

    var title: String {
        switch self {
        case .forever: return "Forever"
        case .days30:  return "30 days"
        case .days7:   return "7 days"
        }
    }

    /// `nil` means "never expire".
    var maxAge: TimeInterval? {
        switch self {
        case .forever: return nil
        case .days30:  return 30 * 24 * 60 * 60
        case .days7:   return 7 * 24 * 60 * 60
        }
    }
}

/// All user-facing settings, backed by `UserDefaults`.
///
/// `AppDelegate` is the single listener for both notifications and re-applies the world from
/// scratch, which keeps the "who reacts to what" story in one place.
///
/// The two notifications are kept separate on purpose: re-registering hot keys can raise a
/// "that shortcut is already taken" alert, and firing that because someone nudged the history
/// limit would be baffling.
final class Preferences: ObservableObject {

    static let shared = Preferences()

    /// Bindings changed — re-register with `HotKeyManager`.
    static let hotKeysDidChangeNotification = Notification.Name("ClipwellHotKeysDidChange")
    /// Everything else (history cap, retention).
    static let didChangeNotification = Notification.Name("ClipwellPreferencesDidChange")
    /// Screenshot watching was turned on or off — the folder watcher must restart.
    static let screenshotSettingsDidChange = Notification.Name("ClipwellScreenshotSettingsDidChange")

    private let defaults = UserDefaults.standard

    // MARK: - Hot keys

    @Published var snapLeft: KeyCombo {
        didSet { store(snapLeft, "hk.snapLeft"); announceHotKeys() }
    }
    @Published var snapRight: KeyCombo {
        didSet { store(snapRight, "hk.snapRight"); announceHotKeys() }
    }
    @Published var maximize: KeyCombo {
        didSet { store(maximize, "hk.maximize"); announceHotKeys() }
    }
    @Published var restoreOrMinimize: KeyCombo {
        didSet { store(restoreOrMinimize, "hk.restoreOrMinimize"); announceHotKeys() }
    }
    @Published var showClipboard: KeyCombo {
        didSet { store(showClipboard, "hk.showClipboard"); announceHotKeys() }
    }

    /// When on, plain ⌘← / ⌘→ *additionally* snap. The primary bindings keep working.
    /// This steals text-navigation (line start/end) and browser back/forward — the Settings
    /// UI says so in as many words.
    @Published var cmdArrowMode: Bool {
        didSet { defaults.set(cmdArrowMode, forKey: "cmdArrowMode"); announceHotKeys() }
    }

    // MARK: - Window management

    /// Whether display cycling wraps around at the outermost screen. Windows does not wrap;
    /// off by default to match.
    @Published var wrapDisplays: Bool {
        didSet { defaults.set(wrapDisplays, forKey: "wrapDisplays") }
    }

    // MARK: - Clipboard

    @Published var historyLimit: Int {
        didSet { defaults.set(historyLimit, forKey: "historyLimit"); announce() }
    }
    @Published var retention: Retention {
        didSet { defaults.set(retention.rawValue, forKey: "retention"); announce() }
    }
    /// Pull screenshots into history even when they were saved as files rather than copied.
    /// Without this, only `⌃⌘⇧4`-style captures (which go to the pasteboard) are ever seen.
    @Published var captureScreenshots: Bool {
        didSet {
            defaults.set(captureScreenshots, forKey: "captureScreenshots")
            NotificationCenter.default.post(name: Preferences.screenshotSettingsDidChange,
                                            object: self)
        }
    }

    /// Also place each captured screenshot on the system pasteboard, so ⌘V pastes it straight
    /// away. Off means it lands in Clipwell's history only, leaving your clipboard untouched.
    @Published var screenshotsToClipboard: Bool {
        didSet { defaults.set(screenshotsToClipboard, forKey: "screenshotsToClipboard") }
    }

    /// Milliseconds to wait after re-activating the target app before synthesizing ⌘V.
    /// Too short and the keystroke lands in the wrong process; 120 ms is comfortable on
    /// Apple silicon, but slow-launching apps may want more.
    @Published var pasteDelayMs: Double {
        didSet { defaults.set(pasteDelayMs, forKey: "pasteDelayMs") }
    }

    // MARK: - Login item

    /// Mirrors `SMAppService.mainApp.status`; the setter is driven by `LaunchAtLogin`.
    @Published var launchAtLogin: Bool {
        didSet {
            guard launchAtLogin != LaunchAtLogin.isEnabled else { return }
            LaunchAtLogin.set(launchAtLogin)
        }
    }

    // MARK: - Init

    /// Bundle identifier the app shipped under before it was renamed.
    private static let legacyBundleID = "com.thiru.snapboard"

    /// One-time import of settings saved under the former bundle identifier.
    ///
    /// `UserDefaults.standard` is keyed by bundle id, so renaming the app silently resets every
    /// preference — custom shortcuts, history limit, retention. Reading the old domain directly
    /// and copying anything we recognise avoids that.
    private static func migrateLegacyDefaults() {
        let defaults = UserDefaults.standard
        let migrationFlag = "migratedFromLegacyBundleID"
        guard !defaults.bool(forKey: migrationFlag) else { return }
        defaults.set(true, forKey: migrationFlag)

        guard let legacy = UserDefaults(suiteName: legacyBundleID) else { return }

        // Explicit list rather than copying wholesale: the old domain also holds system-managed
        // junk (window frames, AppKit state) that has no business being imported.
        let keys = ["hk.snapLeft", "hk.snapRight", "hk.maximize", "hk.restoreOrMinimize",
                    "hk.showClipboard", "cmdArrowMode", "wrapDisplays", "historyLimit",
                    "retention", "pasteDelayMs", "captureScreenshots", "screenshotsToClipboard"]

        var imported = 0
        for key in keys where defaults.object(forKey: key) == nil {
            guard let value = legacy.object(forKey: key) else { continue }
            defaults.set(value, forKey: key)
            imported += 1
        }
        if imported > 0 {
            NSLog("Clipwell: imported %d settings from the previous app name", imported)
        }
    }

    private init() {
        Preferences.migrateLegacyDefaults()

        snapLeft          = Preferences.load("hk.snapLeft") ?? .defaultSnapLeft
        snapRight         = Preferences.load("hk.snapRight") ?? .defaultSnapRight
        maximize          = Preferences.load("hk.maximize") ?? .defaultMaximize
        restoreOrMinimize = Preferences.load("hk.restoreOrMinimize") ?? .defaultRestoreOrMinimize
        showClipboard     = Preferences.load("hk.showClipboard") ?? .defaultShowClipboard

        let d = UserDefaults.standard
        cmdArrowMode = d.bool(forKey: "cmdArrowMode")
        wrapDisplays = d.bool(forKey: "wrapDisplays")
        historyLimit = d.object(forKey: "historyLimit") as? Int ?? 500
        retention    = Retention(rawValue: d.string(forKey: "retention") ?? "") ?? .forever
        pasteDelayMs = d.object(forKey: "pasteDelayMs") as? Double ?? 120
        // Off by default: enabling it makes macOS ask for Files-and-Folders access to the
        // Desktop, and that is not a prompt a clipboard tool should spring on someone
        // unasked. The permission-free route is remapping the screenshot shortcuts — see
        // the README.
        captureScreenshots     = d.object(forKey: "captureScreenshots") as? Bool ?? false
        screenshotsToClipboard = d.object(forKey: "screenshotsToClipboard") as? Bool ?? true
        launchAtLogin = LaunchAtLogin.isEnabled
    }

    // MARK: - Bindings snapshot

    /// The hot key set to hand to `HotKeyManager`, honouring `cmdArrowMode`.
    var hotKeyBindings: [HotKeyAction: KeyCombo] {
        var map: [HotKeyAction: KeyCombo] = [
            .snapLeft: snapLeft,
            .snapRight: snapRight,
            .maximize: maximize,
            .restoreOrMinimize: restoreOrMinimize,
            .showClipboard: showClipboard,
        ]
        if cmdArrowMode {
            map[.cmdArrowLeft] = .cmdArrowLeft
            map[.cmdArrowRight] = .cmdArrowRight
        }
        return map
    }

    func resetHotKeysToDefaults() {
        snapLeft = .defaultSnapLeft
        snapRight = .defaultSnapRight
        maximize = .defaultMaximize
        restoreOrMinimize = .defaultRestoreOrMinimize
        showClipboard = .defaultShowClipboard
    }

    // MARK: - Storage plumbing

    private static func load(_ key: String) -> KeyCombo? {
        guard let data = UserDefaults.standard.data(forKey: key) else { return nil }
        return try? JSONDecoder().decode(KeyCombo.self, from: data)
    }

    private func store(_ combo: KeyCombo, _ key: String) {
        guard let data = try? JSONEncoder().encode(combo) else { return }
        defaults.set(data, forKey: key)
    }

    private func announce() {
        NotificationCenter.default.post(name: Preferences.didChangeNotification, object: self)
    }

    private func announceHotKeys() {
        NotificationCenter.default.post(name: Preferences.hotKeysDidChangeNotification,
                                        object: self)
    }
}
