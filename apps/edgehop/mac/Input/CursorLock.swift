import CoreGraphics
import Foundation

/// Parks the hardware cursor while another machine has control (§6.1 rule 5): mouse motion is
/// detached from the cursor (`CGAssociateMouseAndMouseCursorPosition`), so the tap still sees
/// the deltas but nothing moves on screen; the cursor is warped to the centre of the primary
/// display and hidden.
///
/// Hiding: `CGDisplayHideCursor` only takes effect for the frontmost application, and
/// Edgehop (a menu-bar app) is almost never frontmost. So before hiding, the window-server
/// connection property `SetsCursorInBackground` is set (the same private call Barrier and
/// Synergy use), looked up with dlsym so a missing symbol only means "may stay visible".
/// Detaching: macOS can re-attach the cursor to the mouse when another app activates, so
/// `reparkIfDrifted` re-detaches and re-centres whenever a parked cursor has moved.
@MainActor
final class CursorLock {

    private(set) var isLocked = false
    private var hidden = false

    // MARK: - Control

    func lock(at centre: Geometry.Point) {
        _ = CGAssociateMouseAndMouseCursorPosition(0)
        _ = CGWarpMouseCursorPosition(CGPoint(x: centre.x, y: centre.y))
        CursorLock.allowHidingInBackground()
        if !hidden {
            _ = CGDisplayHideCursor(CGMainDisplayID())
            hidden = true
        }
        isLocked = true
    }

    /// Re-detaches and re-centres a parked cursor that drifted more than 8 points from the
    /// centre; returns the warp vector (centre − p) or nil when nothing was done.
    func reparkIfDrifted(at p: Geometry.Point, centre: Geometry.Point) -> (dx: Int, dy: Int)? {
        guard isLocked, abs(p.x - centre.x) > 8 || abs(p.y - centre.y) > 8 else { return nil }
        _ = CGAssociateMouseAndMouseCursorPosition(0)
        _ = CGWarpMouseCursorPosition(CGPoint(x: centre.x, y: centre.y))
        return (centre.x - p.x, centre.y - p.y)
    }

    private static var backgroundHidingSet = false

    /// `CGSSetConnectionProperty(conn, conn, "SetsCursorInBackground", true)`, once.
    private static func allowHidingInBackground() {
        guard !backgroundHidingSet else { return }
        backgroundHidingSet = true
        typealias MainConnection = @convention(c) () -> Int32
        typealias SetProperty = @convention(c) (Int32, Int32, CFString, CFTypeRef) -> Int32
        guard let handle = dlopen(nil, RTLD_NOW),
              let mainSym = dlsym(handle, "_CGSDefaultConnection") ?? dlsym(handle, "CGSMainConnectionID"),
              let setSym = dlsym(handle, "CGSSetConnectionProperty") else {
            Log.debug("CursorLock: background cursor hiding unavailable", category: "input")
            return
        }
        let connection = unsafeBitCast(mainSym, to: MainConnection.self)()
        let set = unsafeBitCast(setSym, to: SetProperty.self)
        let status = set(connection, connection, "SetsCursorInBackground" as CFString, kCFBooleanTrue)
        Log.debug("CursorLock: SetsCursorInBackground → \(status)", category: "input")
    }

    func unlock() {
        guard isLocked || hidden else { return }
        _ = CGAssociateMouseAndMouseCursorPosition(1)
        if hidden {
            _ = CGDisplayShowCursor(CGMainDisplayID())
            hidden = false
        }
        isLocked = false
    }
}
