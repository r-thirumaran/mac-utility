import CoreGraphics
import Foundation

/// Parks the hardware cursor while another machine has control (§6.1 rule 5): mouse motion is
/// detached from the cursor (`CGAssociateMouseAndMouseCursorPosition`), so the tap still sees
/// the deltas but nothing moves on screen; the cursor is warped to the centre of the primary
/// display and hidden.
///
/// Hiding is best effort: `CGDisplayHideCursor` only takes effect for the frontmost
/// application unless a private connection property is set, so the parked cursor may stay
/// visible on some systems. The README says so.
@MainActor
final class CursorLock {

    private(set) var isLocked = false
    private var hidden = false

    // MARK: - Control

    func lock(at centre: Geometry.Point) {
        _ = CGAssociateMouseAndMouseCursorPosition(0)
        _ = CGWarpMouseCursorPosition(CGPoint(x: centre.x, y: centre.y))
        if !hidden {
            _ = CGDisplayHideCursor(CGMainDisplayID())
            hidden = true
        }
        isLocked = true
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
