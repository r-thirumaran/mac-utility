import Foundation

/// Errors raised by `HTTPRequestParser.parse`. All of them warrant a `400` and closing the
/// connection.
enum HTTPRequestParserError: Error, Equatable {
    case headTooLarge
    case bodyTooLarge
    case malformedRequestLine
    case malformedHeader
    case invalidContentLength
    case unsupportedTransferEncoding
    case invalidEncoding
}

/// Incremental HTTP/1.1 request parser over a caller-owned receive buffer.
///
/// Only what the apps need: a request line, headers, and an optional body sized by
/// `Content-Length`. Chunked request bodies are rejected. The parser is strict about the
/// `CRLF CRLF` head terminator and lenient about everything cosmetic (header whitespace,
/// case).
enum HTTPRequestParser {

    /// Largest request head (request line + headers) accepted.
    static let maxHeadLength = 64 * 1024

    /// Largest request body accepted.
    static let maxBodyLength = 16 * 1024 * 1024

    private static let headTerminator = Data("\r\n\r\n".utf8)

    // MARK: - Parsing

    /// Removes and returns one complete request from the front of `buffer`.
    ///
    /// - Returns: `nil` until the head and the whole `Content-Length` body have arrived
    ///   (nothing is consumed in that case).
    static func parse(from buffer: inout Data) throws -> HTTPRequest? {
        guard let terminator = buffer.range(of: headTerminator) else {
            if buffer.count > maxHeadLength { throw HTTPRequestParserError.headTooLarge }
            return nil
        }
        let start = buffer.startIndex
        guard terminator.lowerBound - start <= maxHeadLength else {
            throw HTTPRequestParserError.headTooLarge
        }

        guard let head = String(data: buffer[start..<terminator.lowerBound], encoding: .utf8) else {
            throw HTTPRequestParserError.invalidEncoding
        }
        var lines = head.components(separatedBy: "\r\n")
        // Tolerate stray CRLFs before the request line (RFC 9112 §2.2).
        while let first = lines.first, first.isEmpty { lines.removeFirst() }
        guard let requestLine = lines.first else { throw HTTPRequestParserError.malformedRequestLine }

        let (method, target) = try parseRequestLine(requestLine)
        let headers = try parseHeaders(lines.dropFirst())

        if let encoding = headers["transfer-encoding"], !encoding.isEmpty,
           encoding.lowercased() != "identity" {
            throw HTTPRequestParserError.unsupportedTransferEncoding
        }
        var contentLength = 0
        if let raw = headers["content-length"] {
            guard let value = Int(raw.trimmingCharacters(in: .whitespaces)), value >= 0 else {
                throw HTTPRequestParserError.invalidContentLength
            }
            guard value <= maxBodyLength else { throw HTTPRequestParserError.bodyTooLarge }
            contentLength = value
        }

        let bodyStart = terminator.upperBound
        let end = bodyStart + contentLength
        guard buffer.endIndex >= end else { return nil }

        let body = Data(buffer[bodyStart..<end])
        buffer.removeSubrange(start..<end)

        let (path, query) = splitTarget(target)
        return HTTPRequest(method: method, target: target, path: path, query: query,
                           headers: headers, body: body)
    }

    // MARK: - Pieces

    private static func parseRequestLine(_ line: String) throws -> (method: String, target: String) {
        let parts = line.split(separator: " ", omittingEmptySubsequences: true)
        guard parts.count == 3, parts[2].hasPrefix("HTTP/1.") else {
            throw HTTPRequestParserError.malformedRequestLine
        }
        return (String(parts[0]).uppercased(), String(parts[1]))
    }

    private static func parseHeaders<S: Sequence>(_ lines: S) throws -> [String: String] where S.Element == String {
        var headers: [String: String] = [:]
        for line in lines {
            if line.isEmpty { continue }
            guard let colon = line.firstIndex(of: ":") else { throw HTTPRequestParserError.malformedHeader }
            let name = line[line.startIndex..<colon].trimmingCharacters(in: .whitespaces).lowercased()
            let value = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
            guard !name.isEmpty else { throw HTTPRequestParserError.malformedHeader }
            if let existing = headers[name] {
                headers[name] = existing + ", " + value
            } else {
                headers[name] = value
            }
        }
        return headers
    }

    /// Splits `/a/b?x=1&y=two` into the decoded path and query dictionary.
    static func splitTarget(_ target: String) -> (path: String, query: [String: String]) {
        var rawPath = target
        var rawQuery = ""
        if let mark = target.firstIndex(of: "?") {
            rawPath = String(target[target.startIndex..<mark])
            rawQuery = String(target[target.index(after: mark)...])
        }
        let path = rawPath.removingPercentEncoding ?? rawPath

        var query: [String: String] = [:]
        for pair in rawQuery.split(separator: "&") where !pair.isEmpty {
            if let equals = pair.firstIndex(of: "=") {
                let key = percentDecode(String(pair[pair.startIndex..<equals]))
                let value = percentDecode(String(pair[pair.index(after: equals)...]))
                query[key] = value
            } else {
                query[percentDecode(String(pair))] = ""
            }
        }
        return (path, query)
    }

    /// Form-style decoding: `+` is a space, then percent escapes.
    static func percentDecode(_ s: String) -> String {
        let plusDecoded = s.replacingOccurrences(of: "+", with: " ")
        return plusDecoded.removingPercentEncoding ?? plusDecoded
    }
}
