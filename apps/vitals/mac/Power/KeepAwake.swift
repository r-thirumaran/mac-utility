import Foundation
import IOKit.pwr_mgt

/// Root-free keep-awake through an IOKit power assertion (`power.keepAwake`).
///
/// "system" holds `PreventUserIdleSystemSleep` (the display may still sleep); "display"
/// holds `PreventUserIdleDisplaySleep`, which implies the former. Neither stops lid-close
/// sleep — only `pmset disablesleep` does — and the assertion dies with the process, so
/// quitting Vitals always releases it. Changing the mode while enabled swaps the assertion.
@MainActor
final class KeepAwake {

    static let modes = ["system", "display"]

    private(set) var enabled = false
    private(set) var mode = "system"
    private var assertionID = IOPMAssertionID(0)

    /// `{enabled, mode}` as `power.keepAwake` / `power.keepAwake.get` report it.
    var state: [String: Any] {
        ["enabled": enabled, "mode": mode]
    }

    // MARK: - Control

    func set(enabled wanted: Bool, mode wantedMode: String) throws {
        let newMode = wantedMode.isEmpty ? "system" : wantedMode
        guard KeepAwake.modes.contains(newMode) else {
            throw BridgeError.invalidParams("keep-awake mode must be system or display")
        }
        release()
        mode = newMode
        guard wanted else { return }

        let type = (newMode == "display" ? "PreventUserIdleDisplaySleep" : "PreventUserIdleSystemSleep") as CFString
        var id = IOPMAssertionID(0)
        let status = IOPMAssertionCreateWithName(type,
                                                 IOPMAssertionLevel(kIOPMAssertionLevelOn),
                                                 "Vitals keep awake" as CFString,
                                                 &id)
        guard status == kIOReturnSuccess else {
            throw BridgeError.internalError("could not create the power assertion (IOReturn \(status))")
        }
        assertionID = id
        enabled = true
    }

    func release() {
        if assertionID != 0 {
            IOPMAssertionRelease(assertionID)
            assertionID = 0
        }
        enabled = false
    }
}
