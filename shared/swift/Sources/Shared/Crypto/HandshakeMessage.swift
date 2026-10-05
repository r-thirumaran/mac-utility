import Foundation

/// The plaintext `hello` / `hello_ack` JSON object of `docs/edgehop-protocol.md` §4, plus the
/// `error` frame.
///
/// The encoder builds the text by hand so the bytes are exactly
/// `{"t":…,"v":1,"id":…,"name":…,"plat":…,"spk":…,"epk":…,"pair":…}` with no whitespace and
/// the keys in that order. Field order does not matter for interoperability (each side hashes
/// the bytes it actually sent and received), but it keeps the Swift and Go transcripts
/// identical so the shared test vectors apply to both. String escaping follows Go's
/// `encoding/json` (`<`, `>`, `&`, U+2028 and U+2029 are `\u`-escaped) for the same reason.
struct HandshakeMessage: Equatable {

    static let protocolVersion = 1

    var type: String
    var version: Int
    var id: String
    var name: String
    var plat: String
    var spk: Data
    var epk: Data
    var pair: Bool

    // MARK: - Encoding

    /// The exact wire bytes.
    func encoded() -> Data {
        var text = "{\"t\":\"" + HandshakeMessage.escape(type) + "\""
        text += ",\"v\":" + String(version)
        text += ",\"id\":\"" + HandshakeMessage.escape(id) + "\""
        text += ",\"name\":\"" + HandshakeMessage.escape(name) + "\""
        text += ",\"plat\":\"" + HandshakeMessage.escape(plat) + "\""
        text += ",\"spk\":\"" + spk.base64EncodedString() + "\""
        text += ",\"epk\":\"" + epk.base64EncodedString() + "\""
        text += ",\"pair\":" + (pair ? "true" : "false")
        text += "}"
        return Data(text.utf8)
    }

    /// `{"t":"error","code":"…"}` — sent before closing.
    static func errorFrame(code: String, message: String? = nil) -> Data {
        var text = "{\"t\":\"error\",\"code\":\"" + escape(code) + "\""
        if let message {
            text += ",\"message\":\"" + escape(message) + "\""
        }
        text += "}"
        return Data(text.utf8)
    }

    /// JSON string escaping, byte-compatible with Go's `encoding/json` for valid UTF-8.
    static func escape(_ s: String) -> String {
        var out = String()
        out.reserveCapacity(s.utf8.count + 2)
        for scalar in s.unicodeScalars {
            switch scalar {
            case "\"":      out += "\\\""
            case "\\":      out += "\\\\"
            case "\n":      out += "\\n"
            case "\r":      out += "\\r"
            case "\t":      out += "\\t"
            case "\u{08}":  out += "\\b"
            case "\u{0C}":  out += "\\f"
            case "<":       out += "\\u003c"
            case ">":       out += "\\u003e"
            case "&":       out += "\\u0026"
            case "\u{2028}": out += "\\u2028"
            case "\u{2029}": out += "\\u2029"
            default:
                if scalar.value < 0x20 {
                    let hex = String(scalar.value, radix: 16)
                    out += "\\u" + String(repeating: "0", count: 4 - hex.count) + hex
                } else {
                    out.unicodeScalars.append(scalar)
                }
            }
        }
        return out
    }

    // MARK: - Decoding

    /// A parsed plaintext frame.
    enum Parsed: Equatable {
        case hello(HandshakeMessage)
        case helloAck(HandshakeMessage)
        case error(code: String, message: String?)
        case other(type: String)
    }

    /// Parses any plaintext handshake frame. Throws `HandshakeError.malformedMessage` when the
    /// JSON is not an object with the fields the type requires.
    static func parse(_ data: Data) throws -> Parsed {
        let any: Any
        do {
            any = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            throw HandshakeError.malformedMessage("not JSON")
        }
        guard let object = any as? [String: Any] else {
            throw HandshakeError.malformedMessage("not an object")
        }
        guard let type = object["t"] as? String else {
            throw HandshakeError.malformedMessage("missing t")
        }

        switch type {
        case "hello", "hello_ack":
            let message = try HandshakeMessage(type: type, object: object)
            return type == "hello" ? .hello(message) : .helloAck(message)
        case "error":
            return .error(code: object["code"] as? String ?? "error",
                          message: object["message"] as? String)
        default:
            return .other(type: type)
        }
    }

    private init(type: String, object: [String: Any]) throws {
        guard let version = HandshakeMessage.integer(object["v"]) else {
            throw HandshakeError.malformedMessage("missing v")
        }
        guard let id = object["id"] as? String else { throw HandshakeError.malformedMessage("missing id") }
        guard let name = object["name"] as? String else { throw HandshakeError.malformedMessage("missing name") }
        guard let plat = object["plat"] as? String else { throw HandshakeError.malformedMessage("missing plat") }
        guard let spkText = object["spk"] as? String, let spk = Data(base64Encoded: spkText) else {
            throw HandshakeError.malformedMessage("bad spk")
        }
        guard let epkText = object["epk"] as? String, let epk = Data(base64Encoded: epkText) else {
            throw HandshakeError.malformedMessage("bad epk")
        }
        guard let pair = HandshakeMessage.boolean(object["pair"]) else {
            throw HandshakeError.malformedMessage("missing pair")
        }
        self.type = type
        self.version = version
        self.id = id
        self.name = name
        self.plat = plat
        self.spk = spk
        self.epk = epk
        self.pair = pair
    }

    init(type: String, id: String, name: String, plat: String, spk: Data, epk: Data, pair: Bool) {
        self.type = type
        self.version = HandshakeMessage.protocolVersion
        self.id = id
        self.name = name
        self.plat = plat
        self.spk = spk
        self.epk = epk
        self.pair = pair
    }

    // JSONSerialization hands numbers back as NSNumber on Darwin and as Int / Double / Bool on
    // Linux, so accept all of them.

    private static func integer(_ value: Any?) -> Int? {
        if let int = value as? Int { return int }
        if let double = value as? Double, double == double.rounded() { return Int(double) }
        if let number = value as? NSNumber { return number.intValue }
        return nil
    }

    private static func boolean(_ value: Any?) -> Bool? {
        if let bool = value as? Bool { return bool }
        if let number = value as? NSNumber { return number.boolValue }
        return nil
    }
}
