import XCTest
@testable import Shared

final class HTTPRequestParserTests: XCTestCase {

    func testSimpleGet() throws {
        var buffer = Data("GET /api/info HTTP/1.1\r\nHost: localhost:7788\r\nAccept: */*\r\n\r\n".utf8)
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(request.method, "GET")
        XCTAssertEqual(request.target, "/api/info")
        XCTAssertEqual(request.path, "/api/info")
        XCTAssertEqual(request.query, [:])
        XCTAssertEqual(request.headers["host"], "localhost:7788")
        XCTAssertEqual(request.header("Accept"), "*/*")
        XCTAssertTrue(request.body.isEmpty)
        XCTAssertTrue(buffer.isEmpty)
        XCTAssertFalse(request.isWebSocketUpgrade)
    }

    func testIncompleteHeadReturnsNil() throws {
        var buffer = Data("GET / HTTP/1.1\r\nHost: x\r\n".utf8)
        XCTAssertNil(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(buffer.count, 25)
    }

    func testQueryParsing() throws {
        var buffer = Data("GET /ws?token=abc%20def&flag&x=1+2 HTTP/1.1\r\n\r\n".utf8)
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(request.path, "/ws")
        XCTAssertEqual(request.target, "/ws?token=abc%20def&flag&x=1+2")
        XCTAssertEqual(request.query, ["token": "abc def", "flag": "", "x": "1 2"])
    }

    func testPercentDecodedPath() throws {
        var buffer = Data("GET /a%20b/c.js HTTP/1.1\r\n\r\n".utf8)
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(request.path, "/a b/c.js")
    }

    func testPostBodyWaitsForContentLength() throws {
        let head = "POST /api/pair HTTP/1.1\r\nContent-Type: application/json\r\nContent-Length: 17\r\n\r\n"
        var buffer = Data((head + "{\"code\":\"12").utf8)
        XCTAssertNil(try HTTPRequestParser.parse(from: &buffer))
        buffer.append(Data("3456\"}".utf8))
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(request.method, "POST")
        XCTAssertEqual(request.bodyText, "{\"code\":\"123456\"}")
        XCTAssertTrue(buffer.isEmpty)
    }

    func testPipelinedRequestsLeaveTheSecondInTheBuffer() throws {
        var buffer = Data("GET /a HTTP/1.1\r\n\r\nGET /b HTTP/1.1\r\n\r\n".utf8)
        XCTAssertEqual(try HTTPRequestParser.parse(from: &buffer)?.path, "/a")
        XCTAssertEqual(try HTTPRequestParser.parse(from: &buffer)?.path, "/b")
        XCTAssertNil(try HTTPRequestParser.parse(from: &buffer))
    }

    func testHeaderNamesAreLowercasedAndDuplicatesJoined() throws {
        var buffer = Data("GET / HTTP/1.1\r\nX-Thing:  a \r\nx-thing: b\r\n\r\n".utf8)
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertEqual(request.headers["x-thing"], "a, b")
    }

    func testWebSocketUpgradeDetection() throws {
        var buffer = Data("""
            GET /ws?token=t HTTP/1.1\r
            Host: 192.168.1.2:7788\r
            Upgrade: websocket\r
            Connection: keep-alive, Upgrade\r
            Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r
            Sec-WebSocket-Version: 13\r
            \r

            """.utf8)
        let request = try XCTUnwrap(try HTTPRequestParser.parse(from: &buffer))
        XCTAssertTrue(request.isWebSocketUpgrade)
        XCTAssertEqual(request.webSocketKey, "dGhlIHNhbXBsZSBub25jZQ==")
        XCTAssertEqual(request.query["token"], "t")
    }

    func testMalformedRequestLineThrows() {
        var buffer = Data("HELLO\r\n\r\n".utf8)
        XCTAssertThrowsError(try HTTPRequestParser.parse(from: &buffer)) { error in
            XCTAssertEqual(error as? HTTPRequestParserError, .malformedRequestLine)
        }
    }

    func testBadContentLengthThrows() {
        var buffer = Data("POST / HTTP/1.1\r\nContent-Length: nope\r\n\r\n".utf8)
        XCTAssertThrowsError(try HTTPRequestParser.parse(from: &buffer)) { error in
            XCTAssertEqual(error as? HTTPRequestParserError, .invalidContentLength)
        }
    }

    func testChunkedBodiesAreRejected() {
        var buffer = Data("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n".utf8)
        XCTAssertThrowsError(try HTTPRequestParser.parse(from: &buffer)) { error in
            XCTAssertEqual(error as? HTTPRequestParserError, .unsupportedTransferEncoding)
        }
    }

    func testOversizedHeadThrows() {
        var buffer = Data(repeating: UInt8(ascii: "a"), count: HTTPRequestParser.maxHeadLength + 1)
        XCTAssertThrowsError(try HTTPRequestParser.parse(from: &buffer)) { error in
            XCTAssertEqual(error as? HTTPRequestParserError, .headTooLarge)
        }
    }

    // MARK: - Responses

    func testResponseSerialization() {
        let response = HTTPResponse.ok(body: Data("hi".utf8), contentType: "text/plain")
        let text = String(decoding: response.serialize(), as: UTF8.self)
        XCTAssertEqual(text, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: text/plain\r\n\r\nhi")
    }

    func testSwitchingProtocolsHasNoContentLength() {
        let response = HTTPResponse.switchingProtocols(acceptKey: "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=")
        let text = String(decoding: response.serialize(), as: UTF8.self)
        XCTAssertEqual(text, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\n"
            + "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nUpgrade: websocket\r\n\r\n")
    }

    func testJSONResponse() throws {
        let response = HTTPResponse.json(["ok": true])
        XCTAssertEqual(response.status, 200)
        XCTAssertEqual(response.headers["Content-Type"], "application/json")
        XCTAssertEqual(response.bodyObject as? [String: Bool], ["ok": true])
    }

    func testStatusHelpers() {
        XCTAssertEqual(HTTPResponse.notFound().status, 404)
        XCTAssertEqual(HTTPResponse.unauthorized().status, 401)
        XCTAssertEqual(HTTPResponse.noContent().status, 204)
        XCTAssertEqual(HTTPResponse.noContent().serialize(),
                       Data("HTTP/1.1 204 No Content\r\n\r\n".utf8))
        XCTAssertEqual(HTTPResponse.ok(text: "x").allowingAnyOrigin().headers["Access-Control-Allow-Origin"], "*")
    }

    func testMimeTypes() {
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "html"), "text/html; charset=utf-8")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "js"), "text/javascript; charset=utf-8")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "mjs"), "text/javascript; charset=utf-8")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "css"), "text/css; charset=utf-8")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "json"), "application/json")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "PNG"), "image/png")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "svg"), "image/svg+xml")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "ico"), "image/x-icon")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "webmanifest"), "application/manifest+json")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "woff2"), "font/woff2")
        XCTAssertEqual(HTTPResponse.mimeType(forExtension: "xyz"), "application/octet-stream")
    }

    // MARK: - Static files

    func testStaticFileResolver() throws {
        let root = FileManager.default.temporaryDirectory
            .appendingPathComponent("shared-static-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: root.appendingPathComponent("sub"),
                                                withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        try Data("<html>".utf8).write(to: root.appendingPathComponent("index.html"))
        try Data("x".utf8).write(to: root.appendingPathComponent("sub/app.js"))

        XCTAssertEqual(StaticFileResolver.resolve(path: "/", under: root)?.lastPathComponent, "index.html")
        XCTAssertEqual(StaticFileResolver.resolve(path: "/index.html", under: root)?.lastPathComponent, "index.html")
        XCTAssertEqual(StaticFileResolver.resolve(path: "/sub/app.js", under: root)?.lastPathComponent, "app.js")
        XCTAssertEqual(StaticFileResolver.resolve(path: "sub/app.js", under: root)?.lastPathComponent, "app.js")
        XCTAssertNil(StaticFileResolver.resolve(path: "/sub", under: root), "directory without index.html")
        XCTAssertNil(StaticFileResolver.resolve(path: "/missing.js", under: root))
        XCTAssertNil(StaticFileResolver.resolve(path: "/../etc/passwd", under: root))
        XCTAssertNil(StaticFileResolver.resolve(path: "/sub/../../etc/passwd", under: root))
    }
}

private extension HTTPResponse {
    var bodyObject: Any? {
        try? JSONSerialization.jsonObject(with: body, options: [])
    }
}
