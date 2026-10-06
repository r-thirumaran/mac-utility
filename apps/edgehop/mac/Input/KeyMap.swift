import Foundation

/// The canonical key table `shared/keymap/hid-keymap.json` (bundled as
/// `Resources/hid-keymap.json`): wire value (HID usage; page 0x0C media keys are
/// `0x1000 + usage`) ⇄ macOS virtual key code. Media keys have no key code on macOS; they
/// travel as `NX_SYSDEFINED` events instead (`mediaKeyType(forWire:)`).
final class KeyMap {

    /// One row of the table (the Windows/evdev columns are ignored here).
    struct Entry: Codable {
        var usage: Int
        var page: Int
        var wire: Int
        var mac: Int
        var dom: String?
    }

    private struct TableFile: Codable {
        var keys: [Entry]
    }

    /// Modifier usages 0xE0…0xE7 occupy bit0…bit7 of the wire modifier byte.
    static let firstModifier = 0xE0
    static let lastModifier = 0xE7

    /// Wire value → `NX_KEYTYPE_*` for the media keys macOS delivers as system-defined events.
    private static let mediaKeyTypes: [Int: Int] = [
        0x10CD: 16, // play/pause      NX_KEYTYPE_PLAY
        0x10B5: 17, // next track      NX_KEYTYPE_NEXT
        0x10B6: 18, // previous track  NX_KEYTYPE_PREVIOUS
        0x10E9: 0,  // volume up       NX_KEYTYPE_SOUND_UP
        0x10EA: 1,  // volume down     NX_KEYTYPE_SOUND_DOWN
        0x10E2: 7,  // mute            NX_KEYTYPE_MUTE
        0x106F: 2,  // brightness up   NX_KEYTYPE_BRIGHTNESS_UP
        0x1070: 3,  // brightness down NX_KEYTYPE_BRIGHTNESS_DOWN
    ]

    private static let wiresByMediaKeyType: [Int: Int] = {
        var out: [Int: Int] = [:]
        for (wire, type) in mediaKeyTypes {
            out[type] = wire
        }
        return out
    }()

    private(set) var byWire: [Int: Entry] = [:]
    private(set) var byMac: [Int: Entry] = [:]

    /// An empty table (every lookup fails).
    init() {}

    init(data: Data) throws {
        let file = try JSONDecoder().decode(TableFile.self, from: data)
        for key in file.keys {
            if byWire[key.wire] == nil {
                byWire[key.wire] = key
            }
            if key.mac >= 0, byMac[key.mac] == nil {
                byMac[key.mac] = key
            }
        }
    }

    /// The table shipped in the bundle, or an empty one (logged) when it cannot be read.
    static func bundled() -> KeyMap {
        guard let url = Bundle.main.resourceURL?.appendingPathComponent("hid-keymap.json") else {
            Log.error("KeyMap: no resource folder", category: "input")
            return KeyMap()
        }
        do {
            let map = try KeyMap(data: try Data(contentsOf: url))
            Log.info("KeyMap: \(map.byWire.count) keys", category: "input")
            return map
        } catch {
            Log.error("KeyMap: cannot load hid-keymap.json: \(error)", category: "input")
            return KeyMap()
        }
    }

    // MARK: - Lookups

    func wire(forMac code: Int) -> Int? {
        byMac[code]?.wire
    }

    /// The virtual key code for a wire value; `nil` for media keys and unknown usages.
    func mac(forWire wire: Int) -> Int? {
        guard let entry = byWire[wire], entry.mac >= 0 else { return nil }
        return entry.mac
    }

    static func isModifier(_ wire: Int) -> Bool {
        wire >= firstModifier && wire <= lastModifier
    }

    /// The bit a modifier usage occupies in the wire modifier byte (bit0 LCtrl … bit7 RGUI).
    static func modifierBit(_ wire: Int) -> UInt8? {
        guard isModifier(wire) else { return nil }
        return UInt8(1) << UInt8(wire - firstModifier)
    }

    static func mediaKeyType(forWire wire: Int) -> Int? {
        mediaKeyTypes[wire]
    }

    static func wire(forMediaKeyType type: Int) -> Int? {
        wiresByMediaKeyType[type]
    }
}
