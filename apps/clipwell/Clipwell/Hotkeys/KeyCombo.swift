import AppKit
import Carbon.HIToolbox

/// A global hot key: a virtual key code plus a Carbon modifier mask.
///
/// We persist the *Carbon* mask (`cmdKey`, `optionKey`, `controlKey`, `shiftKey`) rather than
/// `NSEvent.ModifierFlags`, because that is the form `RegisterEventHotKey` wants. Converting
/// at the edges (recorder in, display out) keeps the stored value stable and means the hot key
/// registration path never has to think about AppKit types.
struct KeyCombo: Codable, Equatable, Hashable {

    var keyCode: UInt32
    var carbonModifiers: UInt32

    init(keyCode: UInt32, carbonModifiers: UInt32) {
        self.keyCode = keyCode
        self.carbonModifiers = carbonModifiers
    }

    /// Builds a combo from a recorded `NSEvent`.
    init(keyCode: UInt16, flags: NSEvent.ModifierFlags) {
        self.keyCode = UInt32(keyCode)
        self.carbonModifiers = KeyCombo.carbonModifiers(from: flags)
    }

    // MARK: - Modifier conversion

    static func carbonModifiers(from flags: NSEvent.ModifierFlags) -> UInt32 {
        var mask: UInt32 = 0
        if flags.contains(.control) { mask |= UInt32(controlKey) }
        if flags.contains(.option)  { mask |= UInt32(optionKey) }
        if flags.contains(.shift)   { mask |= UInt32(shiftKey) }
        if flags.contains(.command) { mask |= UInt32(cmdKey) }
        return mask
    }

    var cocoaModifiers: NSEvent.ModifierFlags {
        var flags: NSEvent.ModifierFlags = []
        if carbonModifiers & UInt32(controlKey) != 0 { flags.insert(.control) }
        if carbonModifiers & UInt32(optionKey)  != 0 { flags.insert(.option) }
        if carbonModifiers & UInt32(shiftKey)   != 0 { flags.insert(.shift) }
        if carbonModifiers & UInt32(cmdKey)     != 0 { flags.insert(.command) }
        return flags
    }

    /// A combo with no modifiers would swallow a bare keystroke system-wide. Never allow it.
    var isValid: Bool { carbonModifiers != 0 }

    // MARK: - Display

    /// e.g. `⌃⌘←`. Modifier order follows Apple's HIG: Control, Option, Shift, Command.
    var displayString: String {
        var s = ""
        if carbonModifiers & UInt32(controlKey) != 0 { s += "⌃" }
        if carbonModifiers & UInt32(optionKey)  != 0 { s += "⌥" }
        if carbonModifiers & UInt32(shiftKey)   != 0 { s += "⇧" }
        if carbonModifiers & UInt32(cmdKey)     != 0 { s += "⌘" }
        s += KeyCombo.name(forKeyCode: keyCode)
        return s
    }

    /// The `NSMenuItem.keyEquivalent` string for this combo (lowercased character, or the
    /// magic unichar for arrows). Used only to render shortcut hints in the status menu —
    /// the menu items themselves are driven by the Carbon hot keys, not by AppKit.
    var menuKeyEquivalent: String {
        switch Int(keyCode) {
        case kVK_LeftArrow:  return String(UnicodeScalar(NSLeftArrowFunctionKey)!)
        case kVK_RightArrow: return String(UnicodeScalar(NSRightArrowFunctionKey)!)
        case kVK_UpArrow:    return String(UnicodeScalar(NSUpArrowFunctionKey)!)
        case kVK_DownArrow:  return String(UnicodeScalar(NSDownArrowFunctionKey)!)
        default:             return KeyCombo.name(forKeyCode: keyCode).lowercased()
        }
    }

    // MARK: - Key code names

    private static let specialNames: [Int: String] = [
        kVK_Return: "↩", kVK_Tab: "⇥", kVK_Space: "Space", kVK_Delete: "⌫",
        kVK_Escape: "⎋", kVK_ForwardDelete: "⌦", kVK_Home: "↖", kVK_End: "↘",
        kVK_PageUp: "⇞", kVK_PageDown: "⇟", kVK_LeftArrow: "←", kVK_RightArrow: "→",
        kVK_DownArrow: "↓", kVK_UpArrow: "↑", kVK_ANSI_KeypadEnter: "⌤",
        kVK_F1: "F1", kVK_F2: "F2", kVK_F3: "F3", kVK_F4: "F4", kVK_F5: "F5",
        kVK_F6: "F6", kVK_F7: "F7", kVK_F8: "F8", kVK_F9: "F9", kVK_F10: "F10",
        kVK_F11: "F11", kVK_F12: "F12",
        kVK_ANSI_A: "A", kVK_ANSI_B: "B", kVK_ANSI_C: "C", kVK_ANSI_D: "D",
        kVK_ANSI_E: "E", kVK_ANSI_F: "F", kVK_ANSI_G: "G", kVK_ANSI_H: "H",
        kVK_ANSI_I: "I", kVK_ANSI_J: "J", kVK_ANSI_K: "K", kVK_ANSI_L: "L",
        kVK_ANSI_M: "M", kVK_ANSI_N: "N", kVK_ANSI_O: "O", kVK_ANSI_P: "P",
        kVK_ANSI_Q: "Q", kVK_ANSI_R: "R", kVK_ANSI_S: "S", kVK_ANSI_T: "T",
        kVK_ANSI_U: "U", kVK_ANSI_V: "V", kVK_ANSI_W: "W", kVK_ANSI_X: "X",
        kVK_ANSI_Y: "Y", kVK_ANSI_Z: "Z",
        kVK_ANSI_0: "0", kVK_ANSI_1: "1", kVK_ANSI_2: "2", kVK_ANSI_3: "3",
        kVK_ANSI_4: "4", kVK_ANSI_5: "5", kVK_ANSI_6: "6", kVK_ANSI_7: "7",
        kVK_ANSI_8: "8", kVK_ANSI_9: "9",
        kVK_ANSI_Minus: "-", kVK_ANSI_Equal: "=", kVK_ANSI_LeftBracket: "[",
        kVK_ANSI_RightBracket: "]", kVK_ANSI_Backslash: "\\", kVK_ANSI_Semicolon: ";",
        kVK_ANSI_Quote: "'", kVK_ANSI_Comma: ",", kVK_ANSI_Period: ".",
        kVK_ANSI_Slash: "/", kVK_ANSI_Grave: "`",
    ]

    static func name(forKeyCode code: UInt32) -> String {
        specialNames[Int(code)] ?? "Key \(code)"
    }

    // MARK: - Defaults

    /// Windows-style snapping defaults. `⌃⌘` was chosen because it is one of the few
    /// arrow-key combinations macOS leaves largely unclaimed — but see the README: recent
    /// macOS releases ship their own tiling shortcuts, so this is user-configurable.
    static let defaultSnapLeft  = KeyCombo(keyCode: UInt32(kVK_LeftArrow),
                                           carbonModifiers: UInt32(controlKey | cmdKey))
    static let defaultSnapRight = KeyCombo(keyCode: UInt32(kVK_RightArrow),
                                           carbonModifiers: UInt32(controlKey | cmdKey))
    static let defaultMaximize  = KeyCombo(keyCode: UInt32(kVK_UpArrow),
                                           carbonModifiers: UInt32(controlKey | cmdKey))
    static let defaultRestoreOrMinimize = KeyCombo(keyCode: UInt32(kVK_DownArrow),
                                           carbonModifiers: UInt32(controlKey | cmdKey))
    /// The Win+V analogue.
    static let defaultShowClipboard = KeyCombo(keyCode: UInt32(kVK_ANSI_V),
                                           carbonModifiers: UInt32(cmdKey | shiftKey))

    // Plain ⌘← / ⌘→ for the opt-in "Cmd+Arrow mode".
    static let cmdArrowLeft  = KeyCombo(keyCode: UInt32(kVK_LeftArrow),
                                        carbonModifiers: UInt32(cmdKey))
    static let cmdArrowRight = KeyCombo(keyCode: UInt32(kVK_RightArrow),
                                        carbonModifiers: UInt32(cmdKey))
}
