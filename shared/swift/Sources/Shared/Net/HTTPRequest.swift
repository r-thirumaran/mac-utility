import Foundation

/// One parsed HTTP/1.1 request (see `HTTPRequestParser`).
///
/// Header names are lowercased; repeated headers are joined with `", "`. `path` is the
/// percent-decoded path without the query string, `target` the request-target exactly as sent.
struct HTTPRequest: Equatable {
    var method: String
    var target: String
    var path: String
    var query: [String: String]
    var headers: [String: String]
    var body: Data

    /// Case-insensitive header lookup (`headers` already uses lowercase keys).
    func header(_ name: String) -> String? {
        headers[name.lowercased()]
    }

    /// `true` for a `GET` carrying `Upgrade: websocket`, `Connection: … upgrade …` and a
    /// `Sec-WebSocket-Key` (RFC 6455 §4.2.1).
    var isWebSocketUpgrade: Bool {
        guard method == "GET",
              header("upgrade")?.lowercased() == "websocket",
              let connection = header("connection")?.lowercased(),
              connection.split(separator: ",").contains(where: {
                  $0.trimmingCharacters(in: .whitespaces) == "upgrade"
              }),
              webSocketKey != nil else { return false }
        return true
    }

    /// The client's `Sec-WebSocket-Key`, if any.
    var webSocketKey: String? {
        guard let key = header("sec-websocket-key"), !key.isEmpty else { return nil }
        return key
    }

    /// The request body as UTF-8 text.
    var bodyText: String? {
        String(data: body, encoding: .utf8)
    }
}
