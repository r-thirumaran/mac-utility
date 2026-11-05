import Foundation

/// What the node needs from the platform input layer: capture of the physical mouse and
/// keyboard, the display list, cursor parking, and injection of remote input —
/// `input.Capturer` + `input.Injector` on the Go side.
@MainActor
protocol InputDevice: AnyObject {

    // MARK: - Capture

    /// Installs the capture and calls `handler` for every event, physical or injected. The
    /// handler returns `true` to swallow the event (the OS never sees it). It must be quick.
    func start(_ handler: @escaping @MainActor (InputEvent) -> Bool)
    func stop()
    /// Parks the cursor at `centre` and hides it (`true`) or releases it (`false`). Best effort.
    func lockCursor(_ lock: Bool, centre: Geometry.Point)
    /// While parked: if the OS let the pointer drift (it can re-attach it to the mouse when
    /// another app becomes active), park it again and return the warp vector (centre − p),
    /// so the caller can take that warp back out of the next reported delta. nil = no drift.
    func reparkIfDrifted(at p: Geometry.Point, centre: Geometry.Point) -> (dx: Int, dy: Int)?
    func displays() -> [Display]
    func onDisplaysChanged(_ handler: @escaping @MainActor () -> Void)
    /// macOS secure input (a password field has the keyboard).
    var secureInputEnabled: Bool { get }

    // MARK: - Injection

    func moveRelative(dx: Int, dy: Int)
    func moveTo(x: Int, y: Int)
    func button(_ button: Int, down: Bool, clicks: Int)
    func scroll(dx: Int, dy: Int, discrete: Bool)
    /// Presses or releases the key with the given HID wire usage; `mods` is the sender's
    /// modifier byte, merged into the event flags.
    func key(wire: Int, down: Bool, mods: UInt8)
    func text(_ text: String)
    func cursorPosition() -> Geometry.Point
}

extension InputDevice {
    func reparkIfDrifted(at p: Geometry.Point, centre: Geometry.Point) -> (dx: Int, dy: Int)? { nil }
}
