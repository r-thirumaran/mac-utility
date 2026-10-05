import Foundation

/// On-disk layout for everything Clipwell persists:
///
///     ~/Library/Application Support/Clipwell/
///         history.json          the ClipItem index (metadata + inline text/RTF)
///         Images/<uuid>.png     image blobs, one file per item, keyed by ClipItem.id
///
/// Image bytes deliberately live outside `history.json`: a few hundred screenshots would
/// turn the index into a multi-hundred-megabyte file that has to be re-encoded on every
/// single copy. Keeping them as loose files means a capture only rewrites the small index.
enum Storage {

    /// The app's former name, kept only for the one-time data migration below.
    private static let legacyDirectoryName = "SnapBoard"

    /// Application Support container, created on first access.
    static let directory: URL = {
        let fileManager = FileManager.default
        let base = fileManager.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("Clipwell", isDirectory: true)

        // The app was renamed, which changes where it looks for its data. Without this move
        // an existing user's entire clipboard history would appear to have been wiped — the
        // old folder would just sit there, orphaned, while the app started from empty.
        //
        // Only ever runs when the new folder does not exist yet, so it cannot clobber real
        // data, and a failed move leaves the old folder untouched rather than losing it.
        let legacy = base.appendingPathComponent(legacyDirectoryName, isDirectory: true)
        if !fileManager.fileExists(atPath: dir.path),
           fileManager.fileExists(atPath: legacy.path) {
            do {
                try fileManager.moveItem(at: legacy, to: dir)
                NSLog("Clipwell: migrated history from %@", legacy.path)
            } catch {
                NSLog("Clipwell: could not migrate from %@: %@", legacy.path, "\(error)")
            }
        }

        try? fileManager.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }()

    static let imagesDirectory: URL = {
        let dir = directory.appendingPathComponent("Images", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }()

    static var historyFile: URL {
        directory.appendingPathComponent("history.json")
    }

    static func imageURL(for id: UUID) -> URL {
        imagesDirectory.appendingPathComponent("\(id.uuidString).png")
    }

    // MARK: - JSON

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        return e
    }()

    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    // MARK: - Atomic helpers

    /// Writes atomically so a crash mid-save can never leave a truncated `history.json`
    /// behind — the app would come back with an empty history, which is exactly the
    /// failure mode we are trying to beat.
    static func write(_ data: Data, to url: URL) {
        do {
            try data.write(to: url, options: .atomic)
        } catch {
            NSLog("Clipwell: failed writing \(url.lastPathComponent): \(error)")
        }
    }

    static func read(_ url: URL) -> Data? {
        try? Data(contentsOf: url)
    }

    static func delete(_ url: URL) {
        try? FileManager.default.removeItem(at: url)
    }

    /// Removes image blobs that no live `ClipItem` references any more. Cheap enough to
    /// run at launch; keeps the Images folder from growing without bound after evictions.
    static func pruneOrphanedImages(keeping liveIDs: Set<UUID>) {
        let fm = FileManager.default
        guard let files = try? fm.contentsOfDirectory(at: imagesDirectory,
                                                      includingPropertiesForKeys: nil) else { return }
        for file in files where file.pathExtension == "png" {
            let stem = file.deletingPathExtension().lastPathComponent
            guard let id = UUID(uuidString: stem) else { continue }
            if !liveIDs.contains(id) { delete(file) }
        }
    }
}
