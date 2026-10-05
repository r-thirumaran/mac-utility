import Foundation

/// Codable ⇄ `JSONSerialization` object bridging, so bridge handlers and HTTP routes can work
/// with typed models while the transports speak `[String: Any]`.
enum JSONValue {

    /// Encodes `value` and returns the equivalent `JSONSerialization` object
    /// (`[String: Any]`, `[Any]`, `String`, `NSNumber`, `NSNull`).
    static func object<T: Encodable>(_ value: T) throws -> Any {
        let data = try JSONEncoder().encode(value)
        return try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
    }

    /// Decodes a `JSONSerialization` object (e.g. bridge `params`) into a Codable model.
    static func decode<T: Decodable>(_ type: T.Type, from any: Any) throws -> T {
        let data = try JSONSerialization.data(withJSONObject: any, options: [.fragmentsAllowed])
        return try JSONDecoder().decode(type, from: data)
    }

    /// Compact JSON text for a `JSONSerialization` object.
    static func string(_ any: Any) throws -> String {
        let data = try JSONSerialization.data(withJSONObject: any, options: [.fragmentsAllowed])
        guard let text = String(data: data, encoding: .utf8) else {
            throw BridgeError(.internalError, "JSON is not UTF-8")
        }
        return text
    }

    /// Parses JSON text into a `JSONSerialization` object.
    static func parse(_ text: String) throws -> Any {
        try JSONSerialization.jsonObject(with: Data(text.utf8), options: [.fragmentsAllowed])
    }
}

/// `JSONValue.object` as a free function, for terse bridge handlers.
func jsonObject<T: Encodable>(_ value: T) throws -> Any {
    try JSONValue.object(value)
}

/// `JSONValue.decode` as a free function, for terse bridge handlers.
func decodeJSON<T: Decodable>(_ type: T.Type, from any: Any) throws -> T {
    try JSONValue.decode(type, from: any)
}
