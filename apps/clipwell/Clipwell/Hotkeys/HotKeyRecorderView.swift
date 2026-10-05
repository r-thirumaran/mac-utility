import AppKit
import Carbon.HIToolbox
import SwiftUI

/// Click-to-record shortcut field.
///
/// Two things make this fiddlier than it looks:
///
///  1. **⌘-combinations never reach `keyDown`.** AppKit routes them through the key-equivalent
///     chain first, so `performKeyEquivalent(with:)` has to be overridden too — without it,
///     every shortcut containing ⌘ is unrecordable.
///  2. **Our own global hot keys eat the keystroke.** `RegisterEventHotKey` consumes matching
///     events system-wide, so recording is bracketed by `HotKeyManager.suspend()/resume()`.
///     Otherwise pressing ⌘⇧V to re-record it would just open the history panel.
final class HotKeyRecorderNSView: NSView {

    var combo: KeyCombo? { didSet { needsDisplay = true } }
    var onChange: ((KeyCombo) -> Void)?

    private var isRecording = false {
        didSet {
            needsDisplay = true
            // Bracket the whole recording session, not each keystroke.
            isRecording ? HotKeyManager.shared.suspend() : HotKeyManager.shared.resume()
        }
    }

    override var acceptsFirstResponder: Bool { true }
    override var isFlipped: Bool { false }

    override func becomeFirstResponder() -> Bool {
        isRecording = true
        return true
    }

    override func resignFirstResponder() -> Bool {
        isRecording = false
        return true
    }

    override func mouseDown(with event: NSEvent) {
        window?.makeFirstResponder(self)
    }

    // MARK: - Capture

    override func keyDown(with event: NSEvent) {
        guard isRecording else {
            super.keyDown(with: event)
            return
        }
        if !capture(event) { super.keyDown(with: event) }
    }

    /// Catches ⌘-bearing combinations, which never arrive via `keyDown`.
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard isRecording else { return super.performKeyEquivalent(with: event) }
        return capture(event)
    }

    /// - Returns: `true` if the event was consumed.
    private func capture(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)

        switch Int(event.keyCode) {
        case kVK_Escape:
            // Cancel without changing anything.
            window?.makeFirstResponder(nil)
            return true
        case kVK_Delete where flags.isEmpty, kVK_ForwardDelete where flags.isEmpty:
            // Bare ⌫ backs out of recording; with modifiers it is a legitimate shortcut.
            window?.makeFirstResponder(nil)
            return true
        default:
            break
        }

        let candidate = KeyCombo(keyCode: event.keyCode, flags: flags)
        // A bare key would hijack that keystroke in every app on the system.
        guard candidate.isValid else {
            NSSound.beep()
            return true
        }

        combo = candidate
        onChange?(candidate)
        window?.makeFirstResponder(nil)
        return true
    }

    // MARK: - Drawing

    override func draw(_ dirtyRect: NSRect) {
        let path = NSBezierPath(roundedRect: bounds.insetBy(dx: 1, dy: 1),
                                xRadius: 5, yRadius: 5)

        (isRecording ? NSColor.controlAccentColor.withAlphaComponent(0.12)
                     : NSColor.controlBackgroundColor).setFill()
        path.fill()

        (isRecording ? NSColor.controlAccentColor : NSColor.separatorColor).setStroke()
        path.lineWidth = isRecording ? 2 : 1
        path.stroke()

        let text = isRecording ? "Press keys…" : (combo?.displayString ?? "Not set")
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 12, weight: .medium),
            .foregroundColor: isRecording ? NSColor.secondaryLabelColor : NSColor.labelColor,
        ]
        let size = (text as NSString).size(withAttributes: attributes)
        (text as NSString).draw(at: NSPoint(x: bounds.midX - size.width / 2,
                                            y: bounds.midY - size.height / 2),
                                withAttributes: attributes)
    }
}

/// SwiftUI wrapper for `HotKeyRecorderNSView`.
struct HotKeyRecorder: NSViewRepresentable {

    @Binding var combo: KeyCombo

    func makeNSView(context: Context) -> HotKeyRecorderNSView {
        let view = HotKeyRecorderNSView()
        view.combo = combo
        view.onChange = { combo = $0 }
        return view
    }

    func updateNSView(_ nsView: HotKeyRecorderNSView, context: Context) {
        // Skip while the user is mid-recording so a re-render cannot clobber the field.
        if nsView.combo != combo { nsView.combo = combo }
        nsView.onChange = { combo = $0 }
    }
}
