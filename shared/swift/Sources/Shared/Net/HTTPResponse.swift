import Foundation

/// An HTTP/1.1 response and the helpers the apps use to build the common ones.
///
/// `serialize()` adds `Content-Length` (except for 1xx / 204 / 304) and writes headers in
/// sorted key order so the bytes are deterministic and testable.
struct HTTPResponse: Equatable {
    var status: Int
    var reason: String
    var headers: [String: String]
    var body: Data

    init(status: Int, reason: String? = nil, headers: [String: String] = [:], body: Data = Data()) {
        self.status = status
        self.reason = reason ?? HTTPResponse.reasonPhrase(for: status)
        self.headers = headers
        self.body = body
    }

    // MARK: - Common responses

    static func ok(body: Data, contentType: String) -> HTTPResponse {
        HTTPResponse(status: 200, headers: ["Content-Type": contentType], body: body)
    }

    static func ok(text: String, contentType: String = "text/plain; charset=utf-8") -> HTTPResponse {
        ok(body: Data(text.utf8), contentType: contentType)
    }

    static func noContent() -> HTTPResponse {
        HTTPResponse(status: 204)
    }

    static func badRequest(_ message: String = "bad request") -> HTTPResponse {
        HTTPResponse(status: 400, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data(message.utf8))
    }

    static func unauthorized() -> HTTPResponse {
        HTTPResponse(status: 401, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data("unauthorized".utf8))
    }

    static func forbidden() -> HTTPResponse {
        HTTPResponse(status: 403, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data("forbidden".utf8))
    }

    static func notFound() -> HTTPResponse {
        HTTPResponse(status: 404, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data("not found".utf8))
    }

    static func methodNotAllowed() -> HTTPResponse {
        HTTPResponse(status: 405, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data("method not allowed".utf8))
    }

    static func internalError(_ message: String = "internal error") -> HTTPResponse {
        HTTPResponse(status: 500, headers: ["Content-Type": "text/plain; charset=utf-8"],
                     body: Data(message.utf8))
    }

    /// Serialises `object` (anything `JSONSerialization` accepts; use `jsonObject(_:)` for
    /// Codable models). A non-serialisable object yields a 500 rather than a crash.
    static func json(_ object: Any, status: Int = 200) -> HTTPResponse {
        guard JSONSerialization.isValidJSONObject(object),
              let data = try? JSONSerialization.data(withJSONObject: object, options: []) else {
            return internalError("response is not JSON-serialisable")
        }
        return HTTPResponse(status: status, headers: ["Content-Type": "application/json"], body: data)
    }

    /// The `101 Switching Protocols` reply to a WebSocket upgrade (RFC 6455 §4.2.2).
    static func switchingProtocols(acceptKey: String) -> HTTPResponse {
        HTTPResponse(status: 101, headers: [
            "Upgrade": "websocket",
            "Connection": "Upgrade",
            "Sec-WebSocket-Accept": acceptKey,
        ])
    }

    // MARK: - Modifiers

    /// Returns a copy with `Access-Control-Allow-Origin: *` and friends, for `/api/*` routes
    /// that a PWA served by another host must reach.
    func allowingAnyOrigin() -> HTTPResponse {
        var copy = self
        copy.headers["Access-Control-Allow-Origin"] = "*"
        copy.headers["Access-Control-Allow-Methods"] = "GET, POST, OPTIONS"
        copy.headers["Access-Control-Allow-Headers"] = "Content-Type, Authorization"
        return copy
    }

    /// Returns a copy with an extra header.
    func withHeader(_ name: String, _ value: String) -> HTTPResponse {
        var copy = self
        copy.headers[name] = value
        return copy
    }

    // MARK: - Serialisation

    /// Status line, headers (sorted by name) and body as wire bytes.
    func serialize() -> Data {
        var head = "HTTP/1.1 \(status) \(reason)\r\n"
        var all = headers
        let bodiless = status < 200 || status == 204 || status == 304
        if !bodiless && !all.keys.contains(where: { $0.lowercased() == "content-length" }) {
            all["Content-Length"] = String(body.count)
        }
        for key in all.keys.sorted() {
            if let value = all[key] {
                head += "\(key): \(value)\r\n"
            }
        }
        head += "\r\n"
        var out = Data(head.utf8)
        if !bodiless { out.append(body) }
        return out
    }

    // MARK: - Tables

    static func reasonPhrase(for status: Int) -> String {
        switch status {
        case 101: return "Switching Protocols"
        case 200: return "OK"
        case 201: return "Created"
        case 204: return "No Content"
        case 301: return "Moved Permanently"
        case 302: return "Found"
        case 304: return "Not Modified"
        case 400: return "Bad Request"
        case 401: return "Unauthorized"
        case 403: return "Forbidden"
        case 404: return "Not Found"
        case 405: return "Method Not Allowed"
        case 413: return "Content Too Large"
        case 426: return "Upgrade Required"
        case 429: return "Too Many Requests"
        case 500: return "Internal Server Error"
        case 501: return "Not Implemented"
        case 503: return "Service Unavailable"
        default:  return "Status \(status)"
        }
    }

    /// MIME type for a file extension (without the dot, any case). Unknown extensions map to
    /// `application/octet-stream`.
    static func mimeType(forExtension ext: String) -> String {
        switch ext.lowercased() {
        case "html", "htm":  return "text/html; charset=utf-8"
        case "js", "mjs":    return "text/javascript; charset=utf-8"
        case "css":          return "text/css; charset=utf-8"
        case "json", "map":  return "application/json"
        case "webmanifest":  return "application/manifest+json"
        case "txt":          return "text/plain; charset=utf-8"
        case "svg":          return "image/svg+xml"
        case "png":          return "image/png"
        case "jpg", "jpeg":  return "image/jpeg"
        case "gif":          return "image/gif"
        case "webp":         return "image/webp"
        case "ico":          return "image/x-icon"
        case "woff2":        return "font/woff2"
        case "woff":         return "font/woff"
        case "ttf":          return "font/ttf"
        case "wasm":         return "application/wasm"
        default:             return "application/octet-stream"
        }
    }
}
