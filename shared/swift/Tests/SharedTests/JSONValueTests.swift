import XCTest
@testable import Shared

final class JSONValueTests: XCTestCase {

    private struct Sample: Codable, Equatable {
        var name: String
        var count: Int
        var ratio: Double
        var flags: [Bool]
        var nested: Nested?

        struct Nested: Codable, Equatable {
            var id: String
        }
    }

    func testEncodableToObjectAndBack() throws {
        let sample = Sample(name: "x", count: 3, ratio: 0.5, flags: [true, false], nested: .init(id: "n"))
        let object = try jsonObject(sample)
        let dictionary = try XCTUnwrap(object as? [String: Any])
        XCTAssertEqual(dictionary["name"] as? String, "x")
        XCTAssertEqual(dictionary["count"] as? Int, 3)
        XCTAssertEqual((dictionary["nested"] as? [String: Any])?["id"] as? String, "n")

        let decoded = try decodeJSON(Sample.self, from: object)
        XCTAssertEqual(decoded, sample)
    }

    func testDecodeFromBridgeParams() throws {
        let params: [String: Any] = ["name": "p", "count": 1, "ratio": 2.5, "flags": []]
        let decoded = try decodeJSON(Sample.self, from: params)
        XCTAssertEqual(decoded, Sample(name: "p", count: 1, ratio: 2.5, flags: [], nested: nil))
        XCTAssertThrowsError(try decodeJSON(Sample.self, from: ["name": "missing fields"]))
    }

    func testFragments() throws {
        XCTAssertEqual(try jsonObject(42) as? Int, 42)
        XCTAssertEqual(try jsonObject(["a", "b"]) as? [String], ["a", "b"])
        XCTAssertEqual(try decodeJSON(String.self, from: "s"), "s")
    }

    func testStringAndParse() throws {
        let text = try JSONValue.string(["event": "x", "data": ["n": 1]])
        let parsed = try XCTUnwrap(try JSONValue.parse(text) as? [String: Any])
        XCTAssertEqual(parsed["event"] as? String, "x")
        XCTAssertEqual((parsed["data"] as? [String: Any])?["n"] as? Int, 1)
    }

    func testBridgeError() {
        let error = BridgeError(.needsPermission, "grant Accessibility")
        XCTAssertEqual(error.code, "needs_permission")
        XCTAssertEqual(error.asDictionary["code"] as? String, "needs_permission")
        XCTAssertEqual(error.asDictionary["message"] as? String, "grant Accessibility")
        XCTAssertEqual(BridgeError.wrap(error), error)
        XCTAssertEqual(BridgeError.wrap(HandshakeError.invalidState).code, "internal")
        XCTAssertEqual(BridgeError.invalidParams().code, "invalid_params")
        XCTAssertEqual(BridgeError.notSupported().code, "not_supported")
        XCTAssertEqual(BridgeError.notAuthorized().code, "not_authorized")
    }
}
