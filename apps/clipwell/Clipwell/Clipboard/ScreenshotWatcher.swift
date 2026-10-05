import AppKit
import CoreServices

/// Optional: watches the folder macOS saves screenshots into and pulls new ones into history.
///
/// ## Why this is off by default
///
/// macOS only puts a screenshot on the pasteboard when you add Control to the shortcut
/// (`⌃⌘⇧4`). The shortcuts most people use — `⌘⇧3`, `⌘⇧4`, `⌘⇧5` — write a *file* and never
/// touch the pasteboard, so `ClipboardMonitor` cannot see them: there is nothing to see.
///
/// Watching the save folder fixes that, but `~/Desktop` is gated by TCC ("Files and Folders"),
/// a permission entirely separate from Accessibility. A clipboard utility asking to read your
/// Desktop is a reasonable thing to be suspicious of, so this is opt-in. The permission-free
/// alternative — remapping the system screenshot shortcuts so the plain keys copy to the
/// clipboard — is documented in the README and needs none of this code.
///
/// ## Threading: every file operation stays off the main thread
///
/// The first `open()` on the Desktop raises a modal consent prompt, and **the call blocks
/// until the user answers**. Clipwell is `LSUIElement`, so at launch there is no window for
/// that prompt to attach to and it can sit unseen behind other windows — potentially forever.
///
/// Doing that on the main thread deadlocks the entire app before `AppDelegate` even creates the
/// status item: a running process with no menu bar icon and no clipboard capture. That bug was
/// real. The `open()` therefore happens on a throwaway global-queue task, so a stuck consent
/// prompt cannot wedge the serial queue either; only once a file descriptor exists does work
/// move onto `queue`.
final class ScreenshotWatcher {

    /// Serial queue owning the source, `seen`, and all directory scanning. Never used for the
    /// initial `open()` — see the note above.
    private let queue = DispatchQueue(label: "com.thiru.clipwell.screenshot-watcher")

    private let store: ClipboardStore
    private let preferences: Preferences

    /// Set by `AppDelegate` so copying a shot to the pasteboard does not come back through
    /// `ClipboardMonitor` as a second, duplicate entry.
    weak var monitor: ClipboardMonitor?

    private var source: DispatchSourceFileSystemObject?
    private var directoryFD: CInt = -1
    private var watchedDirectory: URL?
    private var seen: Set<String> = []

    /// Guards against stacking up `open()` attempts while one is blocked on a consent prompt.
    private var isStarting = false

    /// Observable status for Settings, so a stuck or denied permission is visible rather than
    /// the feature silently doing nothing.
    enum Status: Equatable {
        case off
        case waitingForPermission
        case watching(String)
        case failed(String)
    }
    private(set) var status: Status = .off

    static let statusDidChange = Notification.Name("ClipwellScreenshotWatcherStatusDidChange")

    init(store: ClipboardStore, preferences: Preferences = .shared) {
        self.store = store
        self.preferences = preferences
    }

    // MARK: - Lifecycle

    /// Returns immediately. Safe to call at launch: nothing here can stall the main thread.
    func start() {
        stop()
        guard preferences.captureScreenshots else {
            setStatus(.off)
            return
        }
        guard !isStarting else { return }
        isStarting = true
        setStatus(.waitingForPermission)

        // Deliberately NOT `queue`: if the consent prompt is never answered this task blocks
        // forever, and it must not take the serial queue down with it.
        DispatchQueue.global(qos: .utility).async { [weak self] in
            guard let self else { return }
            let directory = Self.screenshotDirectory()

            let fd = open(directory.path, O_EVTONLY)   // may block on a TCC prompt
            guard fd >= 0 else {
                let denied = errno == EPERM || errno == EACCES
                let message = denied
                    ? "Clipwell was denied access to \(directory.lastPathComponent). Allow it "
                      + "under Privacy & Security ▸ Files and Folders."
                    : "Cannot watch \(directory.path): \(String(cString: strerror(errno)))"
                NSLog("Clipwell: %@", message)
                DispatchQueue.main.async {
                    self.isStarting = false
                    self.setStatus(.failed(message))
                }
                return
            }

            DispatchQueue.main.async { self.isStarting = false }

            self.queue.async {
                self.directoryFD = fd
                self.watchedDirectory = directory
                // Everything already in the folder is history, not news — otherwise enabling
                // this would import a Desktop full of old screenshots.
                self.seen = Set(Self.imageFiles(in: directory).map(\.lastPathComponent))

                let source = DispatchSource.makeFileSystemObjectSource(
                    fileDescriptor: fd, eventMask: [.write], queue: self.queue)
                source.setEventHandler { [weak self] in self?.directoryChanged() }
                source.setCancelHandler { [weak self] in
                    guard let self, self.directoryFD >= 0 else { return }
                    close(self.directoryFD)
                    self.directoryFD = -1
                }
                source.resume()
                self.source = source
                self.setStatus(.watching(directory.path))
            }
        }
    }

    func stop() {
        queue.async { [weak self] in
            guard let self else { return }
            self.source?.cancel()
            self.source = nil
            self.seen.removeAll()
            self.watchedDirectory = nil
        }
    }

    /// Re-reads the save location and restarts; called when the preference changes.
    func restart() { start() }

    private func setStatus(_ new: Status) {
        DispatchQueue.main.async {
            guard self.status != new else { return }
            self.status = new
            NotificationCenter.default.post(name: ScreenshotWatcher.statusDidChange, object: self)
        }
    }

    // MARK: - Detection

    /// Runs on `queue`.
    private func directoryChanged() {
        guard let directory = watchedDirectory else { return }

        // macOS writes the PNG incrementally, and with the floating thumbnail the file only
        // lands once the preview times out. A settle delay avoids reading a half-written file.
        queue.asyncAfter(deadline: .now() + 0.8) { [weak self] in
            guard let self, self.preferences.captureScreenshots else { return }
            for url in Self.imageFiles(in: directory) {
                let name = url.lastPathComponent
                guard !self.seen.contains(name) else { continue }
                self.seen.insert(name)
                guard self.isScreenshot(url) else { continue }
                self.ingest(url)
            }
        }
    }

    /// Whether this is a screenshot rather than something else that landed on the Desktop.
    ///
    /// Spotlight tags real screen captures with `kMDItemIsScreenCapture`, which is
    /// authoritative and immune to localisation — but it is only set once the file has been
    /// indexed, which lags the write. When it is absent we fall back to the filename prefix
    /// macOS uses (read from its own preferences, since the user can change it) plus a recency
    /// check, so an unrelated old image never gets swept up.
    private func isScreenshot(_ url: URL) -> Bool {
        if let item = MDItemCreate(nil, url.path as CFString),
           let flag = MDItemCopyAttribute(item, "kMDItemIsScreenCapture" as CFString) as? Bool {
            return flag
        }

        let prefix = UserDefaults(suiteName: "com.apple.screencapture")?
            .string(forKey: "name") ?? "Screen"
        let name = url.lastPathComponent
        guard name.hasPrefix(prefix)
                || name.hasPrefix("Screenshot")
                || name.hasPrefix("Screen Shot") else { return false }

        guard let created = try? url.resourceValues(forKeys: [.creationDateKey]).creationDate
        else { return false }
        return Date().timeIntervalSince(created) < 30
    }

    // MARK: - Import

    /// Runs on `queue`; hands the finished item to main.
    private func ingest(_ url: URL) {
        guard let data = try? Data(contentsOf: url),
              let image = NSImage(data: data) else { return }

        // Normalise to PNG so the stored blob matches what the rest of the app expects and the
        // fingerprint is stable regardless of the capture format the user configured.
        let png: Data
        if url.pathExtension.lowercased() == "png" {
            png = data
        } else if let tiff = image.tiffRepresentation,
                  let rep = NSBitmapImageRep(data: tiff),
                  let converted = rep.representation(using: .png, properties: [:]) {
            png = converted
        } else {
            return
        }

        let id = UUID()
        Storage.write(png, to: Storage.imageURL(for: id))
        let item = ClipItem(id: id,
                            kind: .image,
                            plainText: url.lastPathComponent,
                            hasImage: true,
                            imageWidth: Int(image.size.width),
                            imageHeight: Int(image.size.height),
                            sourceAppBundleID: "com.apple.screencapture",
                            fingerprint: ClipItem.fingerprint(for: png))

        // Pasteboard and store are both main-thread-only.
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            if self.preferences.screenshotsToClipboard {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setData(png, forType: .png)
                self.monitor?.suppressedChangeCount = pasteboard.changeCount
            }
            self.store.insert(item)
        }
    }

    // MARK: - Location

    /// Where macOS is currently saving screenshots. `com.apple.screencapture location` is unset
    /// by default, meaning Desktop; the value may contain a tilde or point somewhere deleted.
    static func screenshotDirectory() -> URL {
        let fallback = FileManager.default.urls(for: .desktopDirectory, in: .userDomainMask)[0]
        guard let raw = UserDefaults(suiteName: "com.apple.screencapture")?
                .string(forKey: "location"), !raw.isEmpty else { return fallback }

        let expanded = (raw as NSString).expandingTildeInPath
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: expanded, isDirectory: &isDirectory),
              isDirectory.boolValue else { return fallback }
        return URL(fileURLWithPath: expanded, isDirectory: true)
    }

    private static let imageExtensions: Set<String> = ["png", "jpg", "jpeg", "tiff", "gif", "pdf"]

    private static func imageFiles(in directory: URL) -> [URL] {
        let contents = try? FileManager.default.contentsOfDirectory(
            at: directory,
            includingPropertiesForKeys: [.creationDateKey],
            options: [.skipsHiddenFiles, .skipsSubdirectoryDescendants])
        return (contents ?? []).filter { imageExtensions.contains($0.pathExtension.lowercased()) }
    }
}
