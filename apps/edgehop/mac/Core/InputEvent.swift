import Foundation

/// One captured (or injected) input event — `input.Event` on the Go side. Coordinates are
/// desktop-union pixels, origin top-left, y down.
struct InputEvent {

    enum Kind {
        /// `x, y` absolute after the move; `dx, dy` the raw delta.
        case mouseMove
        /// `button, down, clickCount`.
        case mouseButton
        /// `dx, dy` (120 = one wheel notch, or pixels when continuous), `flags`.
        case scroll
        /// `wire` (HID usage), `down`, `isRepeat`, `mods`.
        case key
        /// `text` typed verbatim.
        case keyText
    }

    var kind: Kind
    /// `true` when this process's own injector (or another synthetic source) produced the
    /// event, never hardware.
    var injected = false
    var x = 0
    var y = 0
    var dx = 0
    var dy = 0
    /// Wire button number: 0 left, 1 right, 2 middle, 3 back, 4 forward.
    var button = 0
    var down = false
    var clickCount = 0
    /// An auto-repeat key down (wire state 2).
    var isRepeat = false
    /// HID usage of a key event (`shared/keymap` wire value).
    var wire = 0
    /// HID modifier byte at the time of the event (bit0 LCtrl … bit7 RGUI).
    var mods: UInt8 = 0
    /// Scroll flags (bit0 discrete, bit1 momentum).
    var flags: UInt8 = 0
    var text = ""

    init(kind: Kind) {
        self.kind = kind
    }
}
