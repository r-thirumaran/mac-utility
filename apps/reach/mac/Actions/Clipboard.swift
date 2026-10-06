import AppKit

/// `clipboard.get` / `clipboard.set`: the plain-text flavour of the general pasteboard.
enum ClipboardAccess {

    /// Same cap as the Go agent.
    static let maxLength = 4 << 20

    @MainActor
    static func text() -> String {
        NSPasteboard.general.string(forType: .string) ?? ""
    }

    @MainActor
    static func setText(_ text: String) throws {
        guard text.utf8.count <= maxLength else {
            throw ReachError.invalidParams("text too large")
        }
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
    }
}
