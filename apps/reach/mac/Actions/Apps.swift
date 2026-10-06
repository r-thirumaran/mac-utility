import AppKit

/// Installed and running applications (`apps.*`).
///
/// The installed list is a scan of `/Applications`, `~/Applications`, `/System/Applications`
/// and their `Utilities` folders (one vendor folder deep), named as Finder shows them and
/// de-duplicated by name; `id` is the bundle path. Icons are `NSWorkspace.icon(forFile:)`
/// rendered at 64 px, PNG, base64, cached per path and modification time. The scan may run
/// off the main actor; everything that touches `NSRunningApplication` runs on it.
final class AppsController {

    static let iconSide = 64

    static var roots: [String] {
        [
            "/Applications",
            "/System/Applications",
            NSHomeDirectory() + "/Applications",
        ]
    }

    private let lock = NSLock()
    private var iconCache: [String: String] = [:]

    // MARK: - Installed

    /// `apps.list {icons}` rows, sorted by name.
    func installed(icons: Bool) -> [[String: Any]] {
        var seen = Set<String>()
        var found: [(name: String, path: String, modified: Date)] = []
        for root in AppsController.roots {
            scan(URL(fileURLWithPath: root, isDirectory: true), depth: 0, into: &found, seen: &seen)
        }
        found.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        return found.map { entry -> [String: Any] in
            var row: [String: Any] = ["id": entry.path, "name": entry.name]
            if icons, let encoded = icon(forPath: entry.path, modified: entry.modified) {
                row["iconB64"] = encoded
            }
            return row
        }
    }

    private func scan(_ directory: URL, depth: Int,
                      into found: inout [(name: String, path: String, modified: Date)],
                      seen: inout Set<String>) {
        let keys: [URLResourceKey] = [.isDirectoryKey, .isPackageKey, .contentModificationDateKey]
        guard let entries = try? FileManager.default.contentsOfDirectory(
            at: directory, includingPropertiesForKeys: keys, options: [.skipsHiddenFiles]) else { return }
        for url in entries {
            let values = try? url.resourceValues(forKeys: Set(keys))
            if url.pathExtension == "app" {
                guard let bundle = Bundle(url: url), bundle.executableURL != nil else { continue }
                if (bundle.infoDictionary?["LSBackgroundOnly"] as? Bool) == true { continue }
                let name = AppsController.displayName(of: url, bundle: bundle)
                let key = name.lowercased()
                guard !key.isEmpty, !seen.contains(key) else { continue }
                seen.insert(key)
                found.append((name: name, path: url.path, modified: values?.contentModificationDate ?? .distantPast))
            } else if depth < 2, values?.isDirectory == true, values?.isPackage != true {
                scan(url, depth: depth + 1, into: &found, seen: &seen)
            }
        }
    }

    /// The name Finder shows (localized, without `.app`), falling back to the bundle keys.
    private static func displayName(of url: URL, bundle: Bundle) -> String {
        var name = FileManager.default.displayName(atPath: url.path)
        if name.lowercased().hasSuffix(".app") {
            name = String(name.dropLast(4))
        }
        name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty {
            let info = bundle.infoDictionary
            name = (info?["CFBundleDisplayName"] as? String)
                ?? (info?["CFBundleName"] as? String)
                ?? url.deletingPathExtension().lastPathComponent
        }
        return name
    }

    // MARK: - Icons

    private func icon(forPath path: String, modified: Date) -> String? {
        let key = path + "|" + String(modified.timeIntervalSince1970)
        lock.lock()
        let cached = iconCache[key]
        lock.unlock()
        if let cached {
            return cached.isEmpty ? nil : cached
        }
        let encoded = AppsController.iconPNG(path: path) ?? ""
        lock.lock()
        iconCache[key] = encoded
        lock.unlock()
        return encoded.isEmpty ? nil : encoded
    }

    /// The file's icon drawn into a 64×64 RGBA bitmap, as base64 PNG. `icon(forFile:)` is
    /// thread-safe; the 32 px default is avoided by asking `cgImage(forProposedRect:)` for the
    /// 64 px representation and drawing with Core Graphics.
    static func iconPNG(path: String) -> String? {
        let image = NSWorkspace.shared.icon(forFile: path)
        let side = iconSide
        var proposed = CGRect(x: 0, y: 0, width: side, height: side)
        guard let source = image.cgImage(forProposedRect: &proposed, context: nil, hints: nil) else { return nil }
        guard let context = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0,
                                      space: CGColorSpaceCreateDeviceRGB(),
                                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        let target = CGRect(x: 0, y: 0, width: side, height: side)
        context.interpolationQuality = .high
        context.clear(target)
        context.draw(source, in: target)
        guard let rendered = context.makeImage() else { return nil }
        return NSBitmapImageRep(cgImage: rendered).representation(using: .png, properties: [:])?.base64EncodedString()
    }

    // MARK: - Running

    /// `apps.running` rows: regular (Dock) apps other than Reach, sorted by name.
    @MainActor
    func running() -> [[String: Any]] {
        let own = ProcessInfo.processInfo.processIdentifier
        let apps = NSWorkspace.shared.runningApplications
            .filter { $0.activationPolicy == .regular && $0.processIdentifier > 0 && $0.processIdentifier != own }
            .sorted { AppsController.name(of: $0).localizedCaseInsensitiveCompare(AppsController.name(of: $1)) == .orderedAscending }
        return apps.map { app -> [String: Any] in
            [
                "id": app.bundleURL?.path ?? "pid:\(app.processIdentifier)",
                "name": AppsController.name(of: app),
                "pid": Int(app.processIdentifier),
                "active": app.isActive,
            ]
        }
    }

    private static func name(of app: NSRunningApplication) -> String {
        app.localizedName ?? app.bundleIdentifier ?? "App"
    }

    // MARK: - Launch, focus, quit

    /// `apps.launch {id}`; `completion` may run on any queue.
    @MainActor
    func launch(id: String, completion: @escaping (Error?) -> Void) {
        let url = URL(fileURLWithPath: id)
        guard url.pathExtension == "app" else {
            completion(ReachError.invalidParams("id must be an app bundle from apps.list"))
            return
        }
        guard FileManager.default.fileExists(atPath: url.path) else {
            completion(ReachError.notFound("app not found"))
            return
        }
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, error in
            if let error {
                completion(ReachError.internalError("launch failed: \(error.localizedDescription)"))
            } else {
                completion(nil)
            }
        }
    }

    /// `apps.focus {id?, pid?}`: the pid wins, then the bundle path.
    @MainActor
    func focus(id: String, pid: Int) throws {
        let apps = NSWorkspace.shared.runningApplications
        var target: NSRunningApplication?
        if pid > 0 {
            target = apps.first { Int($0.processIdentifier) == pid }
        }
        if target == nil, !id.isEmpty {
            target = apps.first { $0.bundleURL?.path.caseInsensitiveCompare(id) == .orderedSame }
        }
        guard let app = target else {
            throw ReachError.notFound("no running app for \"\(id)\" / pid \(pid)")
        }
        AppsController.activate(app)
    }

    /// Brings `app` to the front. On macOS 14 activation is a request a background agent may
    /// be refused, so the LaunchServices route (re-opening the running app) is the fallback.
    @MainActor
    static func activate(_ app: NSRunningApplication) {
        if app.isHidden {
            app.unhide()
        }
        if app.activate(options: [.activateAllWindows]) {
            return
        }
        guard let url = app.bundleURL else { return }
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, _ in }
    }

    /// `apps.quit {pid, force?}`: the Quit Apple event, or SIGKILL-style termination.
    @MainActor
    func quit(pid: Int, force: Bool) throws {
        guard let app = NSRunningApplication(processIdentifier: pid_t(pid)) else {
            throw ReachError.notFound("no process \(pid)")
        }
        let accepted = force ? app.forceTerminate() : app.terminate()
        guard accepted else {
            throw ReachError.internalError("process \(pid) did not accept the quit request")
        }
    }
}
