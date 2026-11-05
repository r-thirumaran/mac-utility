import AppKit
import Carbon.HIToolbox
import Foundation

/// The `InputDevice` of the macOS app: `EventTap` for capture, `Injector` for injection,
/// `CursorLock` for parking and `Displays` for the monitor list.
@MainActor
final class MacInputDevice: InputDevice {

    private let tap: EventTap
    private let injector: Injector
    private let cursorLock = CursorLock()
    private let monitor = Displays()

    init(keyMap: KeyMap) {
        tap = EventTap(keyMap: keyMap)
        injector = Injector(keyMap: keyMap, displays: { Displays.current() })
        injector.secureInputBlocked = { MacInputDevice.secureInput }
    }

    /// `true` once the tap sees keyboard events (Input Monitoring granted).
    var capturesKeys: Bool {
        tap.capturesKeys
    }

    /// `true` when the tap can swallow events (Accessibility granted).
    var canSwallow: Bool {
        tap.canSwallow
    }

    /// Tries for the full tap again after a permission was granted.
    func permissionsChanged() {
        tap.reinstallIfNeeded()
    }

    /// The macOS secure-input flag (a password field owns the keyboard).
    static var secureInput: Bool {
        IsSecureEventInputEnabled() == true
    }

    // MARK: - Capture

    func start(_ handler: @escaping @MainActor (InputEvent) -> Bool) {
        tap.handler = handler
        tap.start()
    }

    func stop() {
        tap.stop()
        cursorLock.unlock()
    }

    func lockCursor(_ lock: Bool, centre: Geometry.Point) {
        if lock {
            cursorLock.lock(at: centre)
        } else {
            cursorLock.unlock()
        }
    }

    func reparkIfDrifted(at p: Geometry.Point, centre: Geometry.Point) -> (dx: Int, dy: Int)? {
        cursorLock.reparkIfDrifted(at: p, centre: centre)
    }

    func displays() -> [Display] {
        Displays.current()
    }

    func onDisplaysChanged(_ handler: @escaping @MainActor () -> Void) {
        monitor.onChange = { handler() }
    }

    var secureInputEnabled: Bool {
        MacInputDevice.secureInput
    }

    // MARK: - Injection

    func moveRelative(dx: Int, dy: Int) {
        injector.moveRelative(dx: dx, dy: dy)
    }

    func moveTo(x: Int, y: Int) {
        injector.moveTo(x: x, y: y)
    }

    func button(_ button: Int, down: Bool, clicks: Int) {
        injector.button(button, down: down, clicks: clicks)
    }

    func scroll(dx: Int, dy: Int, discrete: Bool) {
        injector.scroll(dx: dx, dy: dy, discrete: discrete)
    }

    func key(wire: Int, down: Bool, mods: UInt8) {
        injector.key(wire: wire, down: down, mods: mods)
    }

    func text(_ text: String) {
        injector.text(text)
    }

    func cursorPosition() -> Geometry.Point {
        injector.cursorPosition()
    }
}
