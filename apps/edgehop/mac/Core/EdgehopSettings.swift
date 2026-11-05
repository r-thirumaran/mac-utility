import Foundation

/// The user preferences (`core.Settings` in `core/peers.go`; `docs/ARCHITECTURE.md` §5
/// `edgehop.settings.*`). Keys and defaults match the Go side so the one config shape is
/// readable by both.
struct EdgehopSettings: Codable, Equatable {

    var switchDelayMs: Int
    var edgeCornerDeadZonePx: Int
    var swapCmdCtrl: Bool
    var clipboardSync: Bool
    var hotkeySwitch: String
    var lockCursorWhileRemote: Bool
    /// Spec §6: shaking the pointer flashes a ring where it actually is.
    var shakeToLocate: Bool
    /// Trackpad gestures on this Mac are recognised, swallowed here and sent to the machine in
    /// control as the equivalent shortcut (`GestureTranslator`).
    var translateGestures: Bool
    var port: Int

    static let defaults = EdgehopSettings(switchDelayMs: 0,
                                          edgeCornerDeadZonePx: 32,
                                          swapCmdCtrl: true,
                                          clipboardSync: true,
                                          hotkeySwitch: "ctrl+alt",
                                          lockCursorWhileRemote: true,
                                          shakeToLocate: true,
                                          translateGestures: true,
                                          port: 24800)

    init(switchDelayMs: Int,
         edgeCornerDeadZonePx: Int,
         swapCmdCtrl: Bool,
         clipboardSync: Bool,
         hotkeySwitch: String,
         lockCursorWhileRemote: Bool,
         shakeToLocate: Bool = true,
         translateGestures: Bool = true,
         port: Int) {
        self.switchDelayMs = switchDelayMs
        self.edgeCornerDeadZonePx = edgeCornerDeadZonePx
        self.swapCmdCtrl = swapCmdCtrl
        self.clipboardSync = clipboardSync
        self.hotkeySwitch = hotkeySwitch
        self.lockCursorWhileRemote = lockCursorWhileRemote
        self.shakeToLocate = shakeToLocate
        self.translateGestures = translateGestures
        self.port = port
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case switchDelayMs, edgeCornerDeadZonePx, swapCmdCtrl, clipboardSync, hotkeySwitch, lockCursorWhileRemote
        case shakeToLocate, translateGestures, port
    }

    /// Missing keys take the defaults (a partial object from `edgehop.settings.set` or an
    /// older config file).
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let d = EdgehopSettings.defaults
        switchDelayMs = try container.decodeIfPresent(Int.self, forKey: .switchDelayMs) ?? d.switchDelayMs
        edgeCornerDeadZonePx = try container.decodeIfPresent(Int.self, forKey: .edgeCornerDeadZonePx) ?? d.edgeCornerDeadZonePx
        swapCmdCtrl = try container.decodeIfPresent(Bool.self, forKey: .swapCmdCtrl) ?? d.swapCmdCtrl
        clipboardSync = try container.decodeIfPresent(Bool.self, forKey: .clipboardSync) ?? d.clipboardSync
        hotkeySwitch = try container.decodeIfPresent(String.self, forKey: .hotkeySwitch) ?? d.hotkeySwitch
        lockCursorWhileRemote = try container.decodeIfPresent(Bool.self, forKey: .lockCursorWhileRemote) ?? d.lockCursorWhileRemote
        shakeToLocate = try container.decodeIfPresent(Bool.self, forKey: .shakeToLocate) ?? d.shakeToLocate
        translateGestures = try container.decodeIfPresent(Bool.self, forKey: .translateGestures) ?? d.translateGestures
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? d.port
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(switchDelayMs, forKey: .switchDelayMs)
        try container.encode(edgeCornerDeadZonePx, forKey: .edgeCornerDeadZonePx)
        try container.encode(swapCmdCtrl, forKey: .swapCmdCtrl)
        try container.encode(clipboardSync, forKey: .clipboardSync)
        try container.encode(hotkeySwitch, forKey: .hotkeySwitch)
        try container.encode(lockCursorWhileRemote, forKey: .lockCursorWhileRemote)
        try container.encode(shakeToLocate, forKey: .shakeToLocate)
        try container.encode(translateGestures, forKey: .translateGestures)
        try container.encode(port, forKey: .port)
    }

    // MARK: - Normalisation

    /// Clamps out-of-range values and fills blanks with the defaults (`Settings.Normalize`).
    func normalized() -> EdgehopSettings {
        var s = self
        s.switchDelayMs = min(max(s.switchDelayMs, 0), 5000)
        s.edgeCornerDeadZonePx = min(max(s.edgeCornerDeadZonePx, 0), 500)
        if s.hotkeySwitch.trimmingCharacters(in: .whitespaces).isEmpty {
            s.hotkeySwitch = EdgehopSettings.defaults.hotkeySwitch
        }
        s.hotkeySwitch = s.hotkeySwitch.replacingOccurrences(of: " ", with: "").lowercased()
        if s.port < 0 || s.port > 65535 {
            s.port = EdgehopSettings.defaults.port
        }
        return s
    }
}
