import Foundation

/// The encrypted message layer of `docs/edgehop-protocol.md` §5–§6, byte-for-byte what
/// `core/protocol.go` produces: a type byte followed by either a JSON control object (`t`
/// names the message) or a fixed big-endian binary body.
///
/// Control messages are plain `Codable` structs with the Go JSON keys. Decoding is lenient
/// where Go's zero values would be (`omitempty` fields, `null` arrays).
enum Wire {

    // MARK: - Type bytes (§5)

    static let typeControl: UInt8 = 0x01
    static let typeMouseMove: UInt8 = 0x02
    static let typeMouseButton: UInt8 = 0x03
    static let typeScroll: UInt8 = 0x04
    static let typeKey: UInt8 = 0x05
    static let typeKeyText: UInt8 = 0x06

    /// Key states (§5 key).
    static let keyUp: UInt8 = 0
    static let keyDown: UInt8 = 1
    static let keyRepeat: UInt8 = 2

    /// Scroll flags (§5 scroll).
    static let scrollFlagDiscrete: UInt8 = 1 << 0
    static let scrollFlagMomentum: UInt8 = 1 << 1

    /// Control message names (§6 `t`).
    static let tAuth = "auth"
    static let tAuthOK = "auth_ok"
    static let tInfo = "info"
    static let tDisplays = "displays"
    static let tLayout = "layout"
    static let tActive = "active"
    static let tClaim = "claim"
    static let tClipboard = "clipboard"
    static let tPing = "ping"
    static let tPong = "pong"
    static let tSecureInput = "secure_input"
    static let tError = "error"

    // MARK: - Control messages

    /// `info` (right after auth) and `displays` (on change). `port` is the sender's listening
    /// TCP port so a peer reached by a typed address can be re-dialled (§6.3).
    struct Info: Codable {
        var t: String
        var name: String?
        var plat: String?
        var version: String?
        var port: Int?
        var displays: [Display]?

        init(t: String, name: String? = nil, plat: String? = nil, version: String? = nil, port: Int? = nil, displays: [Display]) {
            self.t = t
            self.name = name
            self.plat = plat
            self.version = version
            self.port = port
            self.displays = displays
        }
    }

    /// `layout`: newest `updatedAt` wins.
    struct LayoutMessage: Codable {
        var t: String
        var updatedAt: Int64?
        var tiles: [Layout.Tile]?

        init(layout: Layout) {
            t = Wire.tLayout
            updatedAt = layout.updatedAt
            tiles = layout.tiles
        }

        var layout: Layout {
            Layout(updatedAt: updatedAt ?? 0, tiles: tiles ?? [])
        }
    }

    /// Where the cursor appears on the newly active machine.
    struct Enter: Codable {
        var edge: Edge
        var frac: Double

        init(edge: Edge, frac: Double) {
            self.edge = edge
            self.frac = frac
        }

        private enum CodingKeys: String, CodingKey {
            case edge, frac
        }

        /// An unknown edge name decodes to `nil` at the call site (`Active.enter` is optional).
        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            edge = try container.decode(Edge.self, forKey: .edge)
            frac = try container.decodeIfPresent(Double.self, forKey: .frac) ?? 0
        }
    }

    /// `active`: control moves to `id`. `enter` is absent for a hotkey or menu switch, in which
    /// case the new machine shows its cursor at the centre of its primary display.
    struct Active: Codable {
        var t: String
        var id: String
        var from: String
        var enter: Enter?
        var buttons: UInt8
        var modifiers: UInt8
        var seq: UInt32

        init(id: String, from: String, enter: Enter?, buttons: UInt8, modifiers: UInt8, seq: UInt32) {
            t = Wire.tActive
            self.id = id
            self.from = from
            self.enter = enter
            self.buttons = buttons
            self.modifiers = modifiers
            self.seq = seq
        }

        private enum CodingKeys: String, CodingKey {
            case t, id, from, enter, buttons, modifiers, seq
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tActive
            id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
            from = try container.decodeIfPresent(String.self, forKey: .from) ?? ""
            enter = try? container.decodeIfPresent(Enter.self, forKey: .enter)
            buttons = try container.decodeIfPresent(UInt8.self, forKey: .buttons) ?? 0
            modifiers = try container.decodeIfPresent(UInt8.self, forKey: .modifiers) ?? 0
            seq = try container.decodeIfPresent(UInt32.self, forKey: .seq) ?? 0
        }
    }

    /// `claim`: the sender (`id`) wants to become active.
    struct Claim: Codable {
        var t: String
        var id: String
        var seq: UInt32

        init(id: String, seq: UInt32) {
            t = Wire.tClaim
            self.id = id
            self.seq = seq
        }

        private enum CodingKeys: String, CodingKey {
            case t, id, seq
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tClaim
            id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
            seq = try container.decodeIfPresent(UInt32.self, forKey: .seq) ?? 0
        }
    }

    /// `clipboard` (v1: text only).
    struct Clipboard: Codable {
        var t: String
        var kind: String
        var text: String?
        var b64: String?

        init(text: String) {
            t = Wire.tClipboard
            kind = "text"
            self.text = text
            b64 = nil
        }

        private enum CodingKeys: String, CodingKey {
            case t, kind, text, b64
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tClipboard
            kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? ""
            text = try container.decodeIfPresent(String.self, forKey: .text)
            b64 = try container.decodeIfPresent(String.self, forKey: .b64)
        }
    }

    /// `ping` / `pong`.
    struct Ping: Codable {
        var t: String
        var ts: Int64

        init(t: String, ts: Int64) {
            self.t = t
            self.ts = ts
        }

        private enum CodingKeys: String, CodingKey {
            case t, ts
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tPing
            ts = try container.decodeIfPresent(Int64.self, forKey: .ts) ?? 0
        }
    }

    /// `secure_input`.
    struct SecureInput: Codable {
        var t: String
        var on: Bool

        init(on: Bool) {
            t = Wire.tSecureInput
            self.on = on
        }

        private enum CodingKeys: String, CodingKey {
            case t, on
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tSecureInput
            on = try container.decodeIfPresent(Bool.self, forKey: .on) ?? false
        }
    }

    /// `error`: `{code, message}` then close.
    struct ErrorMessage: Codable {
        var t: String
        var code: String
        var message: String?

        init(code: String, message: String? = nil) {
            t = Wire.tError
            self.code = code
            self.message = message
        }

        private enum CodingKeys: String, CodingKey {
            case t, code, message
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            t = try container.decodeIfPresent(String.self, forKey: .t) ?? Wire.tError
            code = try container.decodeIfPresent(String.self, forKey: .code) ?? ""
            message = try container.decodeIfPresent(String.self, forKey: .message)
        }
    }

    // MARK: - Encoding

    /// `0x01 || JSON` for a control message; `nil` only if the value cannot be encoded.
    static func control<T: Encodable>(_ message: T) -> Data? {
        guard let body = try? JSONEncoder().encode(message) else { return nil }
        var out = Data([typeControl])
        out.append(body)
        return out
    }

    private static func clampI16(_ v: Int) -> Int16 {
        Int16(min(max(v, Int(Int16.min)), Int(Int16.max)))
    }

    private static func appendBigEndian(_ value: Int16, to data: inout Data) {
        let bits = UInt16(bitPattern: value)
        data.append(UInt8(truncatingIfNeeded: bits >> 8))
        data.append(UInt8(truncatingIfNeeded: bits))
    }

    /// 0x02: relative motion in the sender's pixels (clamped to int16).
    static func mouseMove(dx: Int, dy: Int) -> Data {
        var out = Data([typeMouseMove])
        appendBigEndian(clampI16(dx), to: &out)
        appendBigEndian(clampI16(dy), to: &out)
        return out
    }

    /// 0x03: a button press or release with its click count.
    static func mouseButton(button: Int, down: Bool, clicks: Int) -> Data {
        Data([typeMouseButton,
              UInt8(truncatingIfNeeded: max(button, 0)),
              down ? 1 : 0,
              UInt8(min(max(clicks, 0), 255))])
    }

    /// 0x04: wheel motion (120 = one notch, pixels when continuous) with its flags.
    static func scroll(dx: Int, dy: Int, flags: UInt8) -> Data {
        var out = Data([typeScroll])
        appendBigEndian(clampI16(dx), to: &out)
        appendBigEndian(clampI16(dy), to: &out)
        out.append(flags)
        return out
    }

    /// 0x05: a key with its HID usage, state and the modifier byte.
    static func key(usage: Int, state: UInt8, mods: UInt8) -> Data {
        let u = UInt16(truncatingIfNeeded: max(usage, 0))
        return Data([typeKey, UInt8(truncatingIfNeeded: u >> 8), UInt8(truncatingIfNeeded: u), state, mods])
    }

    /// 0x06: text to type verbatim.
    static func keyText(_ text: String) -> Data {
        var out = Data([typeKeyText])
        out.append(contentsOf: Array(text.utf8))
        return out
    }

    // MARK: - Decoding

    enum DecodeError: Error, CustomStringConvertible {
        case malformed(String)
        case unknownType(UInt8)

        var description: String {
            switch self {
            case .malformed(let what): return "malformed frame: \(what)"
            case .unknownType(let type): return String(format: "unknown frame type 0x%02x", Int(type))
            }
        }
    }

    /// A decoded frame. Control frames keep their raw JSON with `t` pre-parsed.
    enum Message {
        case control(t: String, body: Data)
        case mouseMove(dx: Int, dy: Int)
        case mouseButton(button: Int, down: Bool, clicks: Int)
        case scroll(dx: Int, dy: Int, flags: UInt8)
        case key(usage: Int, state: UInt8, mods: UInt8)
        case keyText(String)

        /// Whether the frame carries mouse/keyboard input.
        var isInput: Bool {
            if case .control = self { return false }
            return true
        }
    }

    private static func int16(_ bytes: [UInt8], at index: Int) -> Int {
        Int(Int16(bitPattern: UInt16(bytes[index]) << 8 | UInt16(bytes[index + 1])))
    }

    /// Parses a decrypted payload (`type || body`).
    static func decode(_ payload: Data) throws -> Message {
        let bytes = [UInt8](payload)
        guard let type = bytes.first else { throw DecodeError.malformed("empty payload") }
        let body = Array(bytes.dropFirst())
        switch type {
        case typeControl:
            guard let object = try? JSONSerialization.jsonObject(with: Data(body)) as? [String: Any],
                  let t = object["t"] as? String, !t.isEmpty else {
                throw DecodeError.malformed("control JSON")
            }
            return .control(t: t, body: Data(body))
        case typeMouseMove:
            guard body.count == 4 else { throw DecodeError.malformed("mouse_move body is \(body.count) bytes") }
            return .mouseMove(dx: int16(body, at: 0), dy: int16(body, at: 2))
        case typeMouseButton:
            guard body.count == 3 else { throw DecodeError.malformed("mouse_button body is \(body.count) bytes") }
            guard body[1] <= 1 else { throw DecodeError.malformed("mouse_button state \(body[1])") }
            return .mouseButton(button: Int(body[0]), down: body[1] == 1, clicks: Int(body[2]))
        case typeScroll:
            guard body.count == 5 else { throw DecodeError.malformed("scroll body is \(body.count) bytes") }
            return .scroll(dx: int16(body, at: 0), dy: int16(body, at: 2), flags: body[4])
        case typeKey:
            guard body.count == 4 else { throw DecodeError.malformed("key body is \(body.count) bytes") }
            guard body[2] <= keyRepeat else { throw DecodeError.malformed("key state \(body[2])") }
            return .key(usage: Int(UInt16(body[0]) << 8 | UInt16(body[1])), state: body[2], mods: body[3])
        case typeKeyText:
            guard let text = String(bytes: body, encoding: .utf8) else {
                throw DecodeError.malformed("key_text is not UTF-8")
            }
            return .keyText(text)
        default:
            throw DecodeError.unknownType(type)
        }
    }

    /// Decodes a control body into a message struct.
    static func parse<T: Decodable>(_ type: T.Type, from body: Data) throws -> T {
        try JSONDecoder().decode(type, from: body)
    }
}
