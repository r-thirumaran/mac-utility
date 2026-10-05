import AppKit
import Carbon.HIToolbox

/// Puts an item back on the pasteboard and synthesizes ⌘V into whichever app was frontmost
/// before the panel opened.
///
/// macOS has no "paste into that app over there" API, so this is the standard approach:
/// re-activate the target, then post a hardware-level key event. It needs the same
/// Accessibility grant as window snapping — posting to `.cghidEventTap` is exactly the
/// capability that permission gates.
enum Paster {

    /// Writes `item` to the general pasteboard.
    ///
    /// - Parameter plainTextOnly: drops RTF/image/file flavours and writes only the plain
    ///   string, which is what ⇧↩ in the panel does.
    /// - Returns: the resulting `changeCount`, so `ClipboardMonitor` can ignore our own write.
    @discardableResult
    static func writeToPasteboard(_ item: ClipItem, plainTextOnly: Bool) -> Int {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()

        if plainTextOnly {
            pasteboard.setString(item.plainText ?? item.previewText, forType: .string)
            return pasteboard.changeCount
        }

        switch item.kind {
        case .text:
            pasteboard.setString(item.plainText ?? "", forType: .string)

        case .rtf:
            // Write RTF *and* the plain string: an app that cannot take rich text falls back
            // to the string flavour rather than pasting nothing.
            if let rtf = item.rtfData { pasteboard.setData(rtf, forType: .rtf) }
            if let text = item.plainText { pasteboard.setString(text, forType: .string) }

        case .image:
            if let png = item.imagePNGData { pasteboard.setData(png, forType: .png) }
            if let text = item.plainText, !text.isEmpty {
                pasteboard.setString(text, forType: .string)
            }

        case .fileURLs:
            if let urls = item.fileURLs, !urls.isEmpty {
                pasteboard.writeObjects(urls as [NSURL])
            }
        }

        return pasteboard.changeCount
    }

    /// Posts ⌘V as a synthetic key-down/key-up pair.
    ///
    /// `.cghidEventTap` injects at the lowest point in the event stream, before any
    /// application-level taps, which is what makes it work across sandboxed apps like Safari.
    static func synthesizeCommandV() {
        guard let source = CGEventSource(stateID: .combinedSessionState) else {
            NSLog("Clipwell: could not create event source for paste")
            return
        }
        // While the synthetic event is in flight, keep the user's own physically-held keys
        // from merging into it — otherwise a still-depressed ⇧ from ⇧↩ turns ⌘V into ⇧⌘V
        // (paste-and-match-style, or worse in some apps).
        // Note the omission of `.permitLocalKeyboardEvents`: local *keyboard* events are
        // suppressed for the interval, which is exactly the point.
        source.setLocalEventsFilterDuringSuppressionState(
            [.permitLocalMouseEvents, .permitSystemDefinedEvents],
            state: .eventSuppressionStateSuppressionInterval)

        let vKey = CGKeyCode(kVK_ANSI_V)   // 9
        guard let keyDown = CGEvent(keyboardEventSource: source, virtualKey: vKey, keyDown: true),
              let keyUp = CGEvent(keyboardEventSource: source, virtualKey: vKey, keyDown: false)
        else { return }

        keyDown.flags = .maskCommand
        keyUp.flags = .maskCommand
        keyDown.post(tap: .cghidEventTap)
        keyUp.post(tap: .cghidEventTap)
    }
}
