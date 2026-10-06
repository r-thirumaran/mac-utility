import AppKit
import CoreGraphics

/// Mouse, keyboard and text injection through `CGEvent` on the HID event tap, the same
/// route Clipwell's `Paster` uses. Every post needs the Accessibility grant: without it
/// macOS drops the events silently, so each entry point checks first and reports
/// `needs_permission {permission: "accessibility"}`.
///
/// Coordinates are the global, top-left-origin space of `CGDisplayBounds` — the one the
/// screen frames are mapped back into. Relative moves accumulate sub-pixel remainders and
/// clamp to the union of the displays; a held button turns moves into drags.
@MainActor
final class InputInjector {

    /// `CGEventKeyboardSetUnicodeString` truncates longer strings.
    static let unicodeChunk = 20
    private static let returnKey: CGKeyCode = 36
    private static let tabKey: CGKeyCode = 48

    private let keys: KeyTable
    private let source = CGEventSource(stateID: .hidSystemState)
    private var remainderX = 0.0
    private var remainderY = 0.0
    private var scrollRemainderX = 0.0
    private var scrollRemainderY = 0.0
    private var heldButtons: Set<String> = []

    init(keys: KeyTable) {
        self.keys = keys
    }

    // MARK: - Permission

    static func ensureAccessibility() throws {
        guard Permissions.accessibilityGranted else {
            throw ReachError.needsPermission("accessibility",
                "Accessibility is not allowed for Reach: open System Settings ▸ Privacy & Security ▸ Accessibility and switch Reach on.")
        }
    }

    // MARK: - Mouse

    /// `mouse.move {dx, dy}` in pixels.
    func move(dx: Double, dy: Double) throws {
        try InputInjector.ensureAccessibility()
        remainderX += dx
        remainderY += dy
        let stepX = remainderX.rounded(.towardZero)
        let stepY = remainderY.rounded(.towardZero)
        remainderX -= stepX
        remainderY -= stepY
        guard stepX != 0 || stepY != 0 else { return }
        let current = currentLocation()
        let target = clamp(CGPoint(x: current.x + stepX, y: current.y + stepY))
        postMove(to: target, deltaX: stepX, deltaY: stepY)
    }

    /// `mouse.moveTo {display, x, y}` with fractions of that display.
    func moveTo(display: Int, x: Double, y: Double) throws {
        try InputInjector.ensureAccessibility()
        let target = try ScreenCapture.display(at: display)
        let fx = min(max(x, 0), 1)
        let fy = min(max(y, 0), 1)
        let point = CGPoint(x: target.bounds.minX + fx * max(0, target.bounds.width - 1),
                            y: target.bounds.minY + fy * max(0, target.bounds.height - 1))
        postMove(to: clamp(point), deltaX: 0, deltaY: 0)
    }

    /// `mouse.click {button, count}` at the current position. A double click is two
    /// down/up pairs with click states 1 and 2, which is what AppKit expects.
    func click(button name: String, count: Int) throws {
        try InputInjector.ensureAccessibility()
        let button = try InputInjector.mouseButton(name)
        let location = currentLocation()
        for state in 1...max(1, count) {
            post(type: button.down, at: location, button: button.button, clickState: state)
            post(type: button.up, at: location, button: button.button, clickState: state)
        }
    }

    /// `mouse.down` / `mouse.up` (drag support).
    func button(_ name: String, down: Bool) throws {
        try InputInjector.ensureAccessibility()
        let button = try InputInjector.mouseButton(name)
        if down {
            heldButtons.insert(name)
        } else {
            heldButtons.remove(name)
        }
        post(type: down ? button.down : button.up, at: currentLocation(), button: button.button, clickState: 1)
    }

    /// `mouse.scroll {dx, dy}` in pixels. Positive `dy` moves the content up (natural, as
    /// the phone's two-finger drag sends it); Quartz counts a positive wheel delta as a
    /// scroll towards the top, so both axes are negated. Sub-pixel remainders accumulate.
    func scroll(dx: Double, dy: Double) throws {
        try InputInjector.ensureAccessibility()
        scrollRemainderX += dx
        scrollRemainderY += dy
        let stepX = scrollRemainderX.rounded(.towardZero)
        let stepY = scrollRemainderY.rounded(.towardZero)
        scrollRemainderX -= stepX
        scrollRemainderY -= stepY
        guard stepX != 0 || stepY != 0 else { return }
        let wheelY = Int32(min(max(-stepY, -32_000), 32_000))
        let wheelX = Int32(min(max(-stepX, -32_000), 32_000))
        guard let event = CGEvent(scrollWheelEvent2Source: source, units: .pixel, wheelCount: 2,
                                  wheel1: wheelY, wheel2: wheelX, wheel3: 0) else {
            throw ReachError.internalError("could not create the scroll event")
        }
        event.post(tap: .cghidEventTap)
    }

    // MARK: - Keys

    /// `key.press` (`down && up`, with `mods` held as flags), `key.down`, `key.up`. Media
    /// and audio names go through the system-defined event route of `MediaControl`.
    func key(name: String, mods: [String], down: Bool, up: Bool) throws {
        guard let modifierFlags = KeyTable.flags(for: mods) else {
            throw ReachError.invalidParams("unknown modifier in \(mods)")
        }
        try InputInjector.ensureAccessibility()
        if let systemKey = MediaControl.systemKey(forDOM: name) {
            if down { MediaControl.postSystemKey(systemKey, down: true) }
            if up { MediaControl.postSystemKey(systemKey, down: false) }
            return
        }
        guard let resolved = keys.resolve(name) else {
            throw ReachError.invalidParams("unknown key \"\(name)\"")
        }
        let ownFlag = InputInjector.modifierFlag(ofDOM: resolved.dom)
        if down {
            try postKey(resolved.code, down: true, flags: modifierFlags.union(ownFlag))
        }
        if up {
            try postKey(resolved.code, down: false, flags: modifierFlags)
        }
    }

    /// `key.text`: Unicode typing in chunks of at most 20 UTF-16 units, split at character
    /// boundaries; `\n` presses Return and `\t` Tab so they reach every app.
    func typeText(_ text: String) throws {
        try InputInjector.ensureAccessibility()
        var buffer: [UInt16] = []
        var chunks = 0
        for character in text {
            switch character {
            case "\n", "\r\n":
                try flushUnicode(&buffer, chunks: &chunks)
                try pressKey(InputInjector.returnKey)
            case "\r":
                continue
            case "\t":
                try flushUnicode(&buffer, chunks: &chunks)
                try pressKey(InputInjector.tabKey)
            default:
                let units = Array(character.utf16)
                if buffer.count + units.count > InputInjector.unicodeChunk {
                    try flushUnicode(&buffer, chunks: &chunks)
                }
                buffer.append(contentsOf: units)
            }
        }
        try flushUnicode(&buffer, chunks: &chunks)
    }

    private func flushUnicode(_ buffer: inout [UInt16], chunks: inout Int) throws {
        guard !buffer.isEmpty else { return }
        if chunks > 0 {
            // A short pause between chunks keeps slow apps from dropping characters.
            usleep(2_000)
        }
        try postUnicode(buffer)
        buffer.removeAll(keepingCapacity: true)
        chunks += 1
    }

    private func pressKey(_ code: CGKeyCode) throws {
        try postKey(code, down: true, flags: [])
        try postKey(code, down: false, flags: [])
    }

    private func postKey(_ code: CGKeyCode, down: Bool, flags: CGEventFlags) throws {
        guard let event = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: down) else {
            throw ReachError.internalError("could not create the key event")
        }
        event.flags = flags
        event.post(tap: .cghidEventTap)
    }

    private func postUnicode(_ units: [UInt16]) throws {
        guard let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true),
              let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false) else {
            throw ReachError.internalError("could not create the text event")
        }
        units.withUnsafeBufferPointer { pointer in
            down.keyboardSetUnicodeString(stringLength: pointer.count, unicodeString: pointer.baseAddress)
            up.keyboardSetUnicodeString(stringLength: pointer.count, unicodeString: pointer.baseAddress)
        }
        down.flags = []
        up.flags = []
        down.post(tap: .cghidEventTap)
        up.post(tap: .cghidEventTap)
    }

    /// The flag a modifier key carries on its own key-down, so `key.down MetaLeft` reads as
    /// ⌘ held.
    private static func modifierFlag(ofDOM dom: String) -> CGEventFlags {
        switch dom {
        case "ControlLeft", "ControlRight": return .maskControl
        case "ShiftLeft", "ShiftRight": return .maskShift
        case "AltLeft", "AltRight": return .maskAlternate
        case "MetaLeft", "MetaRight": return .maskCommand
        case "CapsLock": return .maskAlphaShift
        default: return []
        }
    }

    // MARK: - Mouse plumbing

    /// The pointer's current global position (top-left origin).
    private func currentLocation() -> CGPoint {
        CGEvent(source: nil)?.location ?? .zero
    }

    private func clamp(_ point: CGPoint) -> CGPoint {
        let bounds = ScreenCapture.desktopBounds()
        guard !bounds.isNull, bounds.width > 0, bounds.height > 0 else { return point }
        return CGPoint(x: min(max(point.x, bounds.minX), bounds.maxX - 1),
                       y: min(max(point.y, bounds.minY), bounds.maxY - 1))
    }

    private func postMove(to point: CGPoint, deltaX: Double, deltaY: Double) {
        let moving = dragType()
        guard let event = CGEvent(mouseEventSource: source, mouseType: moving.type,
                                  mouseCursorPosition: point, mouseButton: moving.button) else { return }
        event.setIntegerValueField(.mouseEventDeltaX, value: Int64(deltaX))
        event.setIntegerValueField(.mouseEventDeltaY, value: Int64(deltaY))
        event.post(tap: .cghidEventTap)
    }

    private func post(type: CGEventType, at point: CGPoint, button: CGMouseButton, clickState: Int) {
        guard let event = CGEvent(mouseEventSource: source, mouseType: type,
                                  mouseCursorPosition: point, mouseButton: button) else { return }
        event.setIntegerValueField(.mouseEventClickState, value: Int64(clickState))
        event.post(tap: .cghidEventTap)
    }

    /// A move while a button is held is a drag of that button.
    private func dragType() -> (type: CGEventType, button: CGMouseButton) {
        if heldButtons.contains("left") { return (.leftMouseDragged, .left) }
        if heldButtons.contains("right") { return (.rightMouseDragged, .right) }
        if heldButtons.contains("middle") { return (.otherMouseDragged, .center) }
        return (.mouseMoved, .left)
    }

    /// The Quartz button and event types for a contract button name (`""` = left).
    private static func mouseButton(_ name: String) throws -> (button: CGMouseButton, down: CGEventType, up: CGEventType) {
        switch name {
        case "", "left": return (.left, .leftMouseDown, .leftMouseUp)
        case "right": return (.right, .rightMouseDown, .rightMouseUp)
        case "middle": return (.center, .otherMouseDown, .otherMouseUp)
        default: throw ReachError.invalidParams("unknown button \"\(name)\"")
        }
    }
}
