import CoreGraphics
import Foundation

/// Conversions between the wire modifier byte (`docs/edgehop-protocol.md` §5: bit0 LCtrl,
/// bit1 LShift, bit2 LAlt, bit3 LGUI, bit4 RCtrl, bit5 RShift, bit6 RAlt, bit7 RGUI) and
/// `CGEventFlags`.
///
/// Besides the family masks (`.maskControl` …) a hardware event's flags carry the
/// `NX_DEVICE*KEYMASK` bits that say which side was pressed; those are what tell left from
/// right. Events without them (some synthetic sources) fall back to the family masks.
enum HIDModifiers {

    static let leftControl: UInt8 = 0x01
    static let leftShift: UInt8 = 0x02
    static let leftAlt: UInt8 = 0x04
    static let leftGUI: UInt8 = 0x08
    static let rightControl: UInt8 = 0x10
    static let rightShift: UInt8 = 0x20
    static let rightAlt: UInt8 = 0x40
    static let rightGUI: UInt8 = 0x80

    /// `NX_DEVICE*KEYMASK` (IOKit `IOLLEvent.h`), as they appear in `CGEventFlags.rawValue`.
    private static let deviceLeftControl: UInt64 = 0x0000_0001
    private static let deviceLeftShift: UInt64 = 0x0000_0002
    private static let deviceRightShift: UInt64 = 0x0000_0004
    private static let deviceLeftCommand: UInt64 = 0x0000_0008
    private static let deviceRightCommand: UInt64 = 0x0000_0010
    private static let deviceLeftAlternate: UInt64 = 0x0000_0020
    private static let deviceRightAlternate: UInt64 = 0x0000_0040
    private static let deviceRightControl: UInt64 = 0x0000_2000

    // MARK: - Flags → byte

    static func byte(from flags: CGEventFlags) -> UInt8 {
        let raw = flags.rawValue
        var byte: UInt8 = 0
        if raw & deviceLeftControl != 0 { byte |= leftControl }
        if raw & deviceLeftShift != 0 { byte |= leftShift }
        if raw & deviceRightShift != 0 { byte |= rightShift }
        if raw & deviceLeftCommand != 0 { byte |= leftGUI }
        if raw & deviceRightCommand != 0 { byte |= rightGUI }
        if raw & deviceLeftAlternate != 0 { byte |= leftAlt }
        if raw & deviceRightAlternate != 0 { byte |= rightAlt }
        if raw & deviceRightControl != 0 { byte |= rightControl }
        if byte == 0 {
            // No device bits: a synthetic source set only the families; call them left.
            if flags.contains(.maskControl) { byte |= leftControl }
            if flags.contains(.maskShift) { byte |= leftShift }
            if flags.contains(.maskAlternate) { byte |= leftAlt }
            if flags.contains(.maskCommand) { byte |= leftGUI }
        }
        return byte
    }

    // MARK: - Byte → flags

    static func flags(from byte: UInt8) -> CGEventFlags {
        var raw: UInt64 = 0
        if byte & (leftControl | rightControl) != 0 { raw |= CGEventFlags.maskControl.rawValue }
        if byte & (leftShift | rightShift) != 0 { raw |= CGEventFlags.maskShift.rawValue }
        if byte & (leftAlt | rightAlt) != 0 { raw |= CGEventFlags.maskAlternate.rawValue }
        if byte & (leftGUI | rightGUI) != 0 { raw |= CGEventFlags.maskCommand.rawValue }
        if byte & leftControl != 0 { raw |= deviceLeftControl }
        if byte & leftShift != 0 { raw |= deviceLeftShift }
        if byte & rightShift != 0 { raw |= deviceRightShift }
        if byte & leftGUI != 0 { raw |= deviceLeftCommand }
        if byte & rightGUI != 0 { raw |= deviceRightCommand }
        if byte & leftAlt != 0 { raw |= deviceLeftAlternate }
        if byte & rightAlt != 0 { raw |= deviceRightAlternate }
        if byte & rightControl != 0 { raw |= deviceRightControl }
        return CGEventFlags(rawValue: raw)
    }

    /// The family mask (`.maskControl` …) a modifier usage belongs to.
    static func family(ofWire wire: Int) -> CGEventFlags {
        switch wire {
        case 0xE0, 0xE4: return .maskControl
        case 0xE1, 0xE5: return .maskShift
        case 0xE2, 0xE6: return .maskAlternate
        case 0xE3, 0xE7: return .maskCommand
        default: return []
        }
    }
}
