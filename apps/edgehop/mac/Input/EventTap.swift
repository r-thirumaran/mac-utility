import AppKit
import CoreGraphics
import Foundation

/// The capture half of the macOS input backend: a `CGEventTap` at the HID level, head of the
/// chain, active (it can swallow events) — mouse moves/drags with their raw deltas, buttons
/// with the OS click count, scroll wheels (discrete notches ×120, continuous pixels),
/// keys and modifiers as HID usages, and the media keys that arrive as `NX_SYSDEFINED`.
///
/// Permissions: an active tap needs Accessibility; seeing keyboard events needs Input
/// Monitoring. When the full tap cannot be created the tap degrades (mouse only, then
/// listen-only) and keeps retrying every few seconds, so granting the permissions in
/// System Settings takes effect without a relaunch when macOS allows it. Events the app
/// itself posts carry `injectedMagic` in `eventSourceUserData` and are reported with
/// `injected = true`, never swallowed.
///
/// The run-loop source lives on the main run loop, so `handle` runs on the main actor.
@MainActor
final class EventTap {

    /// "EDGH", stamped into `eventSourceUserData` of every event the injector posts.
    static let injectedMagic: Int64 = 0x4544_4748

    private static let retryInterval: TimeInterval = 3
    /// `NX_SYSDEFINED`: media keys (`NSEvent.EventType.systemDefined`), not a `CGEventType` case.
    private static let systemDefinedType: UInt32 = 14
    private static let mediaKeySubtype: Int16 = 8
    private static let capsLockKeyCode = 57
    private static let capsLockWire = 0x39

    private static let mouseTypes: [CGEventType] = [
        .mouseMoved, .leftMouseDown, .leftMouseUp, .rightMouseDown, .rightMouseUp,
        .otherMouseDown, .otherMouseUp, .leftMouseDragged, .rightMouseDragged, .otherMouseDragged,
        .scrollWheel,
    ]
    private static let keyTypes: [CGEventType] = [.keyDown, .keyUp, .flagsChanged]

    /// Receives every event; returns `true` to swallow it.
    var handler: (@MainActor (InputEvent) -> Bool)?
    /// `true` once a tap that sees keyboard events is installed.
    private(set) var capturesKeys = false
    /// `true` when the installed tap may swallow events (Accessibility granted).
    private(set) var canSwallow = false

    private let keyMap: KeyMap
    private var port: CFMachPort?
    private var source: CFRunLoopSource?
    private var retry: DispatchWorkItem?
    private var running = false
    /// Key codes of the modifier keys currently held, for sources without device bits.
    private var heldModifierKeys: Set<Int> = []

    init(keyMap: KeyMap) {
        self.keyMap = keyMap
    }

    // MARK: - Lifecycle

    func start() {
        guard !running else { return }
        running = true
        install()
    }

    func stop() {
        running = false
        retry?.cancel()
        retry = nil
        uninstall()
        capturesKeys = false
        canSwallow = false
    }

    /// Tries for the full tap again (after the user granted a permission).
    func reinstallIfNeeded() {
        guard running, !capturesKeys else { return }
        install()
    }

    private static func mask(_ types: [CGEventType]) -> CGEventMask {
        types.reduce(CGEventMask(0)) { $0 | (CGEventMask(1) << $1.rawValue) }
    }

    private func install() {
        guard running, !capturesKeys else { return }
        let fullMask = EventTap.mask(EventTap.mouseTypes + EventTap.keyTypes) | (CGEventMask(1) << EventTap.systemDefinedType)
        let mouseMask = EventTap.mask(EventTap.mouseTypes)
        if let full = create(mask: fullMask, options: .defaultTap) {
            uninstall()
            activate(full)
            capturesKeys = true
            canSwallow = true
            Log.info("EventTap: full tap installed (mouse + keyboard)", category: "input")
            return
        }
        if port == nil {
            if let mouseOnly = create(mask: mouseMask, options: .defaultTap) {
                activate(mouseOnly)
                canSwallow = true
                Log.error("EventTap: no Input Monitoring permission; mouse only until granted", category: "input")
            } else if let listen = create(mask: mouseMask, options: .listenOnly) {
                activate(listen)
                canSwallow = false
                Log.error("EventTap: no Accessibility permission; listen-only mouse tap until granted", category: "input")
            } else {
                Log.error("EventTap: cannot create an event tap at all", category: "input")
            }
        }
        scheduleRetry()
    }

    private func create(mask: CGEventMask, options: CGEventTapOptions) -> CFMachPort? {
        let userInfo = Unmanaged.passUnretained(self).toOpaque()
        return CGEvent.tapCreate(tap: .cghidEventTap,
                                 place: .headInsertEventTap,
                                 options: options,
                                 eventsOfInterest: mask,
                                 callback: edgehopEventTapCallback,
                                 userInfo: userInfo)
    }

    private func activate(_ newPort: CFMachPort) {
        guard let newSource = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, newPort, 0) else {
            CFMachPortInvalidate(newPort)
            Log.error("EventTap: cannot create the run-loop source", category: "input")
            return
        }
        CFRunLoopAddSource(CFRunLoopGetMain(), newSource, .commonModes)
        CGEvent.tapEnable(tap: newPort, enable: true)
        port = newPort
        source = newSource
    }

    private func uninstall() {
        if let source {
            CFRunLoopRemoveSource(CFRunLoopGetMain(), source, .commonModes)
        }
        if let port {
            CGEvent.tapEnable(tap: port, enable: false)
            CFMachPortInvalidate(port)
        }
        source = nil
        port = nil
    }

    private func scheduleRetry() {
        retry?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.install() }
        }
        retry = item
        DispatchQueue.main.asyncAfter(deadline: .now() + EventTap.retryInterval, execute: item)
    }

    // MARK: - Events

    /// Called from the tap callback; returns `true` to swallow the event.
    fileprivate func handle(type: CGEventType, event: CGEvent) -> Bool {
        if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
            if let port {
                CGEvent.tapEnable(tap: port, enable: true)
            }
            Log.debug("EventTap: re-enabled after the system disabled it", category: "input")
            return false
        }
        guard let handler else { return false }
        let injected = event.getIntegerValueField(.eventSourceUserData) == EventTap.injectedMagic

        switch type {
        case .mouseMoved, .leftMouseDragged, .rightMouseDragged, .otherMouseDragged:
            var ev = InputEvent(kind: .mouseMove)
            ev.injected = injected
            let location = event.location
            ev.x = Int(location.x.rounded())
            ev.y = Int(location.y.rounded())
            ev.dx = Int(event.getIntegerValueField(.mouseEventDeltaX))
            ev.dy = Int(event.getIntegerValueField(.mouseEventDeltaY))
            return handler(ev) && !injected

        case .leftMouseDown, .leftMouseUp, .rightMouseDown, .rightMouseUp, .otherMouseDown, .otherMouseUp:
            var ev = InputEvent(kind: .mouseButton)
            ev.injected = injected
            let location = event.location
            ev.x = Int(location.x.rounded())
            ev.y = Int(location.y.rounded())
            ev.button = EventTap.wireButton(type: type, number: Int(event.getIntegerValueField(.mouseEventButtonNumber)))
            ev.down = type == .leftMouseDown || type == .rightMouseDown || type == .otherMouseDown
            ev.clickCount = max(1, Int(event.getIntegerValueField(.mouseEventClickState)))
            return handler(ev) && !injected

        case .scrollWheel:
            var ev = InputEvent(kind: .scroll)
            ev.injected = injected
            if event.getIntegerValueField(.scrollWheelEventIsContinuous) != 0 {
                ev.dy = Int(event.getIntegerValueField(.scrollWheelEventPointDeltaAxis1))
                ev.dx = Int(event.getIntegerValueField(.scrollWheelEventPointDeltaAxis2))
                if event.getIntegerValueField(.scrollWheelEventMomentumPhase) != 0 {
                    ev.flags |= Wire.scrollFlagMomentum
                }
            } else {
                ev.dy = Int(event.getIntegerValueField(.scrollWheelEventDeltaAxis1)) * 120
                ev.dx = Int(event.getIntegerValueField(.scrollWheelEventDeltaAxis2)) * 120
                ev.flags = Wire.scrollFlagDiscrete
            }
            return handler(ev) && !injected

        case .keyDown, .keyUp:
            let code = Int(event.getIntegerValueField(.keyboardEventKeycode))
            let mods = HIDModifiers.byte(from: event.flags)
            if let wire = keyMap.wire(forMac: code) {
                var ev = InputEvent(kind: .key)
                ev.injected = injected
                ev.wire = wire
                ev.down = type == .keyDown
                ev.isRepeat = event.getIntegerValueField(.keyboardEventAutorepeat) != 0
                ev.mods = mods
                return handler(ev) && !injected
            }
            // A key the table cannot express: send what it would have typed.
            guard type == .keyDown else { return false }
            let text = EventTap.unicodeString(of: event)
            guard !text.isEmpty else { return false }
            var ev = InputEvent(kind: .keyText)
            ev.injected = injected
            ev.text = text
            ev.mods = mods
            return handler(ev) && !injected

        case .flagsChanged:
            return handleFlagsChanged(event, injected: injected, handler: handler)

        default:
            if type.rawValue == EventTap.systemDefinedType {
                return handleSystemDefined(event, injected: injected, handler: handler)
            }
            return false
        }
    }

    /// A modifier key went down or up (one event per change, no key-down/up pair).
    private func handleFlagsChanged(_ event: CGEvent, injected: Bool, handler: @MainActor (InputEvent) -> Bool) -> Bool {
        let code = Int(event.getIntegerValueField(.keyboardEventKeycode))
        let mods = HIDModifiers.byte(from: event.flags)
        guard let wire = keyMap.wire(forMac: code) else { return false }
        if let bit = KeyMap.modifierBit(wire) {
            // The device bits say exactly which side is down after this event; a source
            // without them only sets the family, so fall back to toggling per key code.
            var down = mods & bit != 0
            if !down, !heldModifierKeys.contains(code), event.flags.contains(HIDModifiers.family(ofWire: wire)) {
                down = true
            }
            if down {
                heldModifierKeys.insert(code)
            } else {
                heldModifierKeys.remove(code)
            }
            var ev = InputEvent(kind: .key)
            ev.injected = injected
            ev.wire = wire
            ev.down = down
            ev.mods = mods
            return handler(ev) && !injected
        }
        if code == EventTap.capsLockKeyCode {
            // Caps Lock reports a single flags change per press: send a press and a release.
            var ev = InputEvent(kind: .key)
            ev.injected = injected
            ev.wire = EventTap.capsLockWire
            ev.mods = mods
            ev.down = true
            let swallowDown = handler(ev)
            ev.down = false
            let swallowUp = handler(ev)
            return (swallowDown || swallowUp) && !injected
        }
        return false
    }

    /// Media keys: `NX_SYSDEFINED` subtype 8, key type in bits 16–31 of `data1`, 0xA/0xB in
    /// bits 8–15 for down/up.
    private func handleSystemDefined(_ event: CGEvent, injected: Bool, handler: @MainActor (InputEvent) -> Bool) -> Bool {
        guard let nsEvent = NSEvent(cgEvent: event), nsEvent.subtype.rawValue == EventTap.mediaKeySubtype else {
            return false
        }
        let data1 = nsEvent.data1
        let keyType = (data1 >> 16) & 0xFFFF
        let keyState = (data1 >> 8) & 0xFF
        guard let wire = KeyMap.wire(forMediaKeyType: keyType) else { return false }
        var ev = InputEvent(kind: .key)
        ev.injected = injected
        ev.wire = wire
        ev.down = keyState == 0xA
        ev.isRepeat = data1 & 0x1 != 0
        ev.mods = HIDModifiers.byte(from: event.flags)
        return handler(ev) && !injected
    }

    // MARK: - Helpers

    /// macOS button numbers → wire numbers (0 left, 1 right, 2 middle, 3 back, 4 forward).
    private static func wireButton(type: CGEventType, number: Int) -> Int {
        switch type {
        case .leftMouseDown, .leftMouseUp: return 0
        case .rightMouseDown, .rightMouseUp: return 1
        default: return max(number, 2)
        }
    }

    private static func unicodeString(of event: CGEvent) -> String {
        var length = 0
        var buffer = [UniChar](repeating: 0, count: 8)
        event.keyboardGetUnicodeString(maxStringLength: buffer.count, actualStringLength: &length, unicodeString: &buffer)
        guard length > 0 else { return "" }
        return String(utf16CodeUnits: buffer, count: min(length, buffer.count))
    }
}

/// The C callback: `userInfo` is the unretained `EventTap`. Runs on the main run loop.
private let edgehopEventTapCallback: CGEventTapCallBack = { _, type, event, userInfo in
    guard let userInfo else { return Unmanaged.passUnretained(event) }
    let tap = Unmanaged<EventTap>.fromOpaque(userInfo).takeUnretainedValue()
    let swallow = MainActor.assumeIsolated {
        tap.handle(type: type, event: event)
    }
    return swallow ? nil : Unmanaged.passUnretained(event)
}
