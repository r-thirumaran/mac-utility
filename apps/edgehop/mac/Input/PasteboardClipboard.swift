import AppKit
import Foundation

/// The general pasteboard as a text clipboard. Text longer than 512 KiB of UTF-16 is cut, as
/// the Windows backend does, so a `clipboard` message always fits a 1 MiB frame.
@MainActor
final class PasteboardClipboard: TextClipboard {

    private static let maxUnits = 262_144

    func text() -> String? {
        guard let text = NSPasteboard.general.string(forType: .string) else { return nil }
        if text.utf16.count > PasteboardClipboard.maxUnits {
            return String(decoding: Array(text.utf16.prefix(PasteboardClipboard.maxUnits)), as: UTF16.self)
        }
        return text
    }

    func setText(_ text: String) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
    }
}
