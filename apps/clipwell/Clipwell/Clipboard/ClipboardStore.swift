import AppKit
import Combine

/// The clipboard history: the in-memory list, its JSON mirror on disk, and the eviction rules.
///
/// Everything here runs on the main thread — the capture timer, the SwiftUI panel and the
/// status menu all live there, and the workload (a few hundred small structs) does not justify
/// a queue and the synchronisation bugs that come with one. Disk writes are debounced so a
/// burst of copies costs one save rather than ten.
final class ClipboardStore: ObservableObject {

    @Published private(set) var items: [ClipItem] = []

    private let preferences: Preferences
    private var saveWorkItem: DispatchWorkItem?

    /// Thumbnails and app icons are re-read constantly while scrolling the panel; caching them
    /// keeps that off the disk. Cleared wholesale rather than pruned — it is only a cache.
    private var imageCache: [UUID: NSImage] = [:]
    private var iconCache: [String: NSImage] = [:]

    init(preferences: Preferences = .shared) {
        self.preferences = preferences
    }

    // MARK: - Loading

    func load() {
        guard let data = Storage.read(Storage.historyFile) else { return }
        do {
            items = try Storage.decoder.decode([ClipItem].self, from: data)
        } catch {
            NSLog("Clipwell: history.json unreadable (\(error)); starting empty")
            items = []
            return
        }
        // Retention and cap settings may have tightened while the app was not running.
        enforceLimits()
        Storage.pruneOrphanedImages(keeping: Set(items.map(\.id)))
    }

    // MARK: - Mutation

    /// Adds a freshly captured item, collapsing it onto an existing entry with the same
    /// content.
    ///
    /// A re-copy keeps the original `id` — and therefore the original image blob and pin
    /// state — but takes the new timestamp and moves to the top, which is what "move
    /// re-copied item to top instead of duplicating" means in practice.
    func insert(_ item: ClipItem) {
        if let index = items.firstIndex(where: { $0.fingerprint == item.fingerprint }) {
            var existing = items.remove(at: index)
            existing.timestamp = item.timestamp
            if let source = item.sourceAppBundleID { existing.sourceAppBundleID = source }
            items.insert(existing, at: 0)

            // The capture wrote a blob for what turned out to be a duplicate; drop it so the
            // Images folder does not accumulate copies of the same screenshot.
            if item.hasImage && item.id != existing.id {
                Storage.delete(Storage.imageURL(for: item.id))
            }
        } else {
            items.insert(item, at: 0)
        }
        enforceLimits()
        scheduleSave()
    }

    func togglePin(_ item: ClipItem) {
        guard let index = items.firstIndex(where: { $0.id == item.id }) else { return }
        items[index].isPinned.toggle()
        scheduleSave()
    }

    func remove(_ item: ClipItem) {
        guard let index = items.firstIndex(where: { $0.id == item.id }) else { return }
        discardBlob(items[index])
        items.remove(at: index)
        scheduleSave()
    }

    /// "Clear history" — pinned items survive, as advertised in the UI.
    func clearUnpinned() {
        for item in items where !item.isPinned { discardBlob(item) }
        items.removeAll { !$0.isPinned }
        scheduleSave()
    }

    // MARK: - Eviction

    /// Applies the retention window and the hard cap. Pinned items are exempt from both.
    func enforceLimits() {
        if let maxAge = preferences.retention.maxAge {
            let cutoff = Date().addingTimeInterval(-maxAge)
            for item in items where !item.isPinned && item.timestamp < cutoff {
                discardBlob(item)
            }
            items.removeAll { !$0.isPinned && $0.timestamp < cutoff }
        }

        let limit = max(1, preferences.historyLimit)
        while items.count > limit {
            // `items` is newest-first, so the *last* unpinned entry is the oldest one.
            guard let index = items.lastIndex(where: { !$0.isPinned }) else { break }
            discardBlob(items[index])
            items.remove(at: index)
        }
    }

    private func discardBlob(_ item: ClipItem) {
        imageCache.removeValue(forKey: item.id)
        if item.hasImage { Storage.delete(Storage.imageURL(for: item.id)) }
    }

    // MARK: - Persistence

    private func scheduleSave() {
        saveWorkItem?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.flush() }
        saveWorkItem = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5, execute: work)
    }

    /// Writes immediately. Called on quit so nothing is lost to the debounce window.
    func flush() {
        saveWorkItem?.cancel()
        saveWorkItem = nil
        guard let data = try? Storage.encoder.encode(items) else { return }
        Storage.write(data, to: Storage.historyFile)
    }

    // MARK: - Presentation helpers

    func image(for item: ClipItem) -> NSImage? {
        if let cached = imageCache[item.id] { return cached }
        guard let data = item.imagePNGData, let image = NSImage(data: data) else { return nil }
        imageCache[item.id] = image
        return image
    }

    func icon(forBundleID bundleID: String?) -> NSImage? {
        guard let bundleID else { return nil }
        if let cached = iconCache[bundleID] { return cached }
        guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
        else { return nil }
        let icon = NSWorkspace.shared.icon(forFile: url.path)
        iconCache[bundleID] = icon
        return icon
    }
}
