import AppKit
import CryptoKit

enum ClipKind: String, Codable {
    case text
    case rtf
    case image
    case fileURLs

    var symbolName: String {
        switch self {
        case .text:     return "text.alignleft"
        case .rtf:      return "doc.richtext"
        case .image:    return "photo"
        case .fileURLs: return "folder"
        }
    }
}

/// One entry in the clipboard history.
///
/// **Image storage.** Unlike the other payloads, image bytes are *not* held in this struct or
/// in `history.json`; they live at `Storage.imageURL(for: id)` and are read on demand through
/// `imagePNGData`. Five hundred screenshots inlined as base64 would make every single copy
/// re-encode a several-hundred-megabyte JSON file. `hasImage` records that the blob exists.
struct ClipItem: Identifiable, Codable, Equatable {

    let id: UUID
    var kind: ClipKind

    /// Always populated when the pasteboard offered any text at all, even for `.rtf`,
    /// `.image` and `.fileURLs` items — it powers search and "paste as plain text".
    var plainText: String?
    var rtfData: Data?

    var hasImage: Bool
    var imageWidth: Int?
    var imageHeight: Int?

    var fileURLs: [URL]?

    var sourceAppBundleID: String?
    var timestamp: Date
    var isPinned: Bool

    /// Content hash used to collapse a re-copy onto the existing entry instead of
    /// duplicating it.
    var fingerprint: String

    init(id: UUID = UUID(),
         kind: ClipKind,
         plainText: String? = nil,
         rtfData: Data? = nil,
         hasImage: Bool = false,
         imageWidth: Int? = nil,
         imageHeight: Int? = nil,
         fileURLs: [URL]? = nil,
         sourceAppBundleID: String? = nil,
         timestamp: Date = Date(),
         isPinned: Bool = false,
         fingerprint: String) {
        self.id = id
        self.kind = kind
        self.plainText = plainText
        self.rtfData = rtfData
        self.hasImage = hasImage
        self.imageWidth = imageWidth
        self.imageHeight = imageHeight
        self.fileURLs = fileURLs
        self.sourceAppBundleID = sourceAppBundleID
        self.timestamp = timestamp
        self.isPinned = isPinned
        self.fingerprint = fingerprint
    }

    // MARK: - Derived content

    var imagePNGData: Data? {
        hasImage ? Storage.read(Storage.imageURL(for: id)) : nil
    }

    /// Multi-line preview shown in the history panel.
    var previewText: String {
        switch kind {
        case .image:
            if let w = imageWidth, let h = imageHeight { return "Image · \(w) × \(h)" }
            return "Image"
        case .fileURLs:
            let names = (fileURLs ?? []).map { $0.lastPathComponent }
            return names.isEmpty ? "Files" : names.joined(separator: "\n")
        case .text, .rtf:
            return plainText ?? ""
        }
    }

    /// Single-line form for the status-bar menu, with whitespace collapsed so a multi-line
    /// snippet does not blow the menu apart.
    func menuTitle(maxLength: Int = 48) -> String {
        let collapsed = previewText
            .components(separatedBy: .whitespacesAndNewlines)
            .filter { !$0.isEmpty }
            .joined(separator: " ")
        let text = collapsed.isEmpty ? kind.rawValue.capitalized : collapsed
        guard text.count > maxLength else { return text }
        return text.prefix(maxLength).trimmingCharacters(in: .whitespaces) + "…"
    }

    var searchHaystack: String {
        var parts = [previewText]
        if let plainText { parts.append(plainText) }
        if let bundleID = sourceAppBundleID { parts.append(bundleID) }
        return parts.joined(separator: " ").lowercased()
    }

    // MARK: - Fingerprinting

    static func fingerprint(for data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    static func fingerprint(for string: String) -> String {
        fingerprint(for: Data(string.utf8))
    }
}
