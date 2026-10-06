import AppKit
import CoreGraphics
import Foundation

/// The injection half of the macOS input backend: posts synthetic mouse, scroll, key and text
/// events at the HID tap, stamped with `EventTap.injectedMagic` so the tap reports them as
/// injected.
///
/// Relative moves drive a virtual cursor (re-synced from the real one after a second of
/// quiet or an absolute move) clamped to the display union; while a button is held they are
/// posted as drags. Keys carry explicit flags from the modifiers the remote side holds (its
/// modifier key events are tracked here) merged with the modifier byte it sent. Text goes
/// through `keyboardSetUnicodeString` in chunks of 20 UTF-16 units. Media keys are
/// `NX_SYSDEFINED` events built with `NSEvent.otherEvent`.
@MainActor
final class Injector {

    /// Longest run of UTF-16 units one keyboard event may carry.
    private static let textChunk = 20
    /// One wheel notch (120 units) is three lines.
    private static let unitsPerLine = 40
    private static let resyncAfter: TimeInterval = 1

    /// Reports whether secure input is on; keystrokes are skipped while it is.
    var secureInputBlocked: () -> Bool = { false }

    private let keyMap: KeyMap
    private let displays: () -> [Display]
    private let source: CGEventSource?
    private var cursor = CGPoint.zero
    private var lastMove: TimeInterval = 0
    /// Bit = wire button number.
    private var heldButtons: UInt8 = 0
    /// HID modifier byte of the modifier keys injected and not yet released.
    private var heldMods: UInt8 = 0

    init(keyMap: KeyMap, displays: @escaping () -> [Display]) {
        self.keyMap = keyMap
        self.displays = displays
        source = CGEventSource(stateID: .hidSystemState)
        // Reviewer note: tag the source as well as each posted event, so every event created
        // from it carries `injectedMagic` into the tap; and drop the default 250 ms during
        // which macOS suppresses local hardware input after each posted event, or a mouse or
        // keyboard attached to this machine would stall while a remote one drives it.
        source?.userData = EventTap.injectedMagic
        source?.localEventsSuppressionInterval = 0
    }

    // MARK: - Cursor

    /// The real cursor position (global display coordinates, origin top-left).
    func cursorPosition() -> Geometry.Point {
        if let event = CGEvent(source: nil) {
            let location = event.location
            return Geometry.Point(x: Int(location.x.rounded()), y: Int(location.y.rounded()))
        }
        return Geometry.Point(x: Int(cursor.x), y: Int(cursor.y))
    }

    /// Warps to an absolute position (snapped onto a display).
    func moveTo(x: Int, y: Int) {
        let screens = displays()
        let target = Geometry.snapToDisplays(Geometry.clamp(Geometry.Point(x: x, y: y), to: screens), displays: screens)
        let from = cursorPosition()
        postMove(to: target, dx: target.x - from.x, dy: target.y - from.y)
    }

    /// Moves by the remote sender's delta from the virtual cursor.
    func moveRelative(dx: Int, dy: Int) {
        let now = ProcessInfo.processInfo.systemUptime
        if now - lastMove > Injector.resyncAfter {
            let real = cursorPosition()
            cursor = CGPoint(x: real.x, y: real.y)
        }
        let target = Geometry.clamp(Geometry.Point(x: Int(cursor.x) + dx, y: Int(cursor.y) + dy), to: displays())
        postMove(to: target, dx: dx, dy: dy)
    }

    private func postMove(to point: Geometry.Point, dx: Int, dy: Int) {
        let (type, button) = dragType()
        let position = CGPoint(x: point.x, y: point.y)
        guard let event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: position, mouseButton: button) else {
            return
        }
        event.setIntegerValueField(.mouseEventDeltaX, value: Int64(dx))
        event.setIntegerValueField(.mouseEventDeltaY, value: Int64(dy))
        if type == .otherMouseDragged {
            event.setIntegerValueField(.mouseEventButtonNumber, value: Int64(button.rawValue))
        }
        post(event)
        cursor = position
        lastMove = ProcessInfo.processInfo.systemUptime
    }

    /// The event type a move takes while a button is held (the lowest held button wins).
    private func dragType() -> (CGEventType, CGMouseButton) {
        if heldButtons & 0x01 != 0 { return (.leftMouseDragged, .left) }
        if heldButtons & 0x02 != 0 { return (.rightMouseDragged, .right) }
        for number in 2..<8 where heldButtons & (UInt8(1) << UInt8(number)) != 0 {
            return (.otherMouseDragged, CGMouseButton(rawValue: UInt32(number)) ?? .center)
        }
        return (.mouseMoved, .left)
    }

    // MARK: - Buttons and wheels

    func button(_ button: Int, down: Bool, clicks: Int) {
        guard button >= 0, button <= 7 else { return }
        let type: CGEventType
        let cgButton: CGMouseButton
        switch button {
        case 0:
            type = down ? .leftMouseDown : .leftMouseUp
            cgButton = .left
        case 1:
            type = down ? .rightMouseDown : .rightMouseUp
            cgButton = .right
        default:
            type = down ? .otherMouseDown : .otherMouseUp
            cgButton = CGMouseButton(rawValue: UInt32(button)) ?? .center
        }
        let here = cursorPosition()
        let position = CGPoint(x: here.x, y: here.y)
        guard let event = CGEvent(mouseEventSource: source, mouseType: type, mouseCursorPosition: position, mouseButton: cgButton) else {
            return
        }
        event.setIntegerValueField(.mouseEventClickState, value: Int64(max(clicks, 1)))
        event.setIntegerValueField(.mouseEventButtonNumber, value: Int64(button))
        post(event)
        let bit = UInt8(1) << UInt8(button)
        if down {
            heldButtons |= bit
        } else {
            heldButtons &= ~bit
        }
        cursor = position
    }

    /// Positive `dy` = wheel forward (away from the user), positive `dx` = right; 120 per
    /// notch when discrete, pixels otherwise.
    func scroll(dx: Int, dy: Int, discrete: Bool) {
        let vertical = discrete ? Injector.lines(dy) : dy
        let horizontal = discrete ? Injector.lines(dx) : dx
        guard let event = CGEvent(scrollWheelEvent2Source: source,
                                  units: discrete ? .line : .pixel,
                                  wheelCount: 2,
                                  wheel1: Int32(clamping: vertical),
                                  wheel2: Int32(clamping: horizontal),
                                  wheel3: 0) else { return }
        post(event)
    }

    private static func lines(_ units: Int) -> Int {
        guard units != 0 else { return 0 }
        let lines = units / unitsPerLine
        if lines != 0 { return lines }
        return units > 0 ? 1 : -1
    }

    // MARK: - Keys

    func key(wire: Int, down: Bool, mods: UInt8) {
        if let bit = KeyMap.modifierBit(wire) {
            if down {
                heldMods |= bit
            } else {
                heldMods &= ~bit
            }
        }
        if let keyType = KeyMap.mediaKeyType(forWire: wire) {
            postMediaKey(keyType, down: down)
            return
        }
        guard let code = keyMap.mac(forWire: wire) else { return }
        if secureInputBlocked() { return }
        guard let event = CGEvent(keyboardEventSource: source, virtualKey: CGKeyCode(code), keyDown: down) else { return }
        let effective = KeyMap.isModifier(wire) ? heldMods : (heldMods | mods)
        event.flags = HIDModifiers.flags(from: effective)
        post(event)
    }

    func text(_ text: String) {
        if secureInputBlocked() { return }
        let units = Array(text.utf16)
        var index = 0
        while index < units.count {
            let end = min(index + Injector.textChunk, units.count)
            let chunk = Array(units[index..<end])
            index = end
            for down in [true, false] {
                guard let event = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: down) else { continue }
                chunk.withUnsafeBufferPointer { buffer in
                    if let base = buffer.baseAddress {
                        event.keyboardSetUnicodeString(stringLength: chunk.count, unicodeString: base)
                    }
                }
                event.flags = []
                post(event)
            }
        }
    }

    private func postMediaKey(_ keyType: Int, down: Bool) {
        let data1 = (keyType << 16) | ((down ? 0xA : 0xB) << 8)
        guard let nsEvent = NSEvent.otherEvent(with: .systemDefined,
                                               location: .zero,
                                               modifierFlags: [],
                                               timestamp: ProcessInfo.processInfo.systemUptime,
                                               windowNumber: 0,
                                               context: nil,
                                               subtype: 8,
                                               data1: data1,
                                               data2: -1),
              let event = nsEvent.cgEvent else { return }
        post(event)
    }

    // MARK: - Posting

    private func post(_ event: CGEvent) {
        event.setIntegerValueField(.eventSourceUserData, value: EventTap.injectedMagic)
        event.post(tap: .cghidEventTap)
    }
}
