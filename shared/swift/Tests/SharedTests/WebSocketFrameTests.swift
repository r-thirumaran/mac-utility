import XCTest
@testable import Shared

final class WebSocketFrameTests: XCTestCase {

    // RFC 6455 §5.7 examples.

    func testUnmaskedHelloTextFrame() {
        let bytes = WebSocketFrame.encode(.text("Hello"))
        XCTAssertEqual(Array(bytes), [0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f])
    }

    func testMaskedHelloTextFrame() {
        let bytes = WebSocketFrame.encode(.text("Hello"), masked: true, maskingKey: 0x37fa213d)
        XCTAssertEqual(Array(bytes), [0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58])
    }

    func testDecodeMaskedHello() throws {
        var buffer = Data([0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58])
        let frame = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer, requireMask: true))
        XCTAssertEqual(frame.opcode, .text)
        XCTAssertTrue(frame.fin)
        XCTAssertEqual(frame.textPayload, "Hello")
        XCTAssertTrue(buffer.isEmpty)
    }

    func testDecodeUnmaskedHello() throws {
        var buffer = Data([0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f])
        let frame = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(frame, .text("Hello"))
    }

    func testUnmaskedClientFrameRejectedWhenMaskRequired() {
        var buffer = Data([0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f])
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &buffer, requireMask: true)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .unmaskedClientFrame)
        }
    }

    func testPingAndPongExamples() throws {
        // "Hello" ping, unmasked.
        let ping = WebSocketFrame.encode(WebSocketFrame(opcode: .ping, payload: Data("Hello".utf8)))
        XCTAssertEqual(Array(ping), [0x89, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f])
        // "Hello" pong, masked with the RFC key.
        let pong = WebSocketFrame.encode(WebSocketFrame(opcode: .pong, payload: Data("Hello".utf8)),
                                         masked: true, maskingKey: 0x37fa213d)
        XCTAssertEqual(Array(pong), [0x8a, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58])
    }

    func testSixteenBitLength() throws {
        let payload = Data(repeating: 0x42, count: 256)
        let bytes = WebSocketFrame.encode(.binary(payload))
        XCTAssertEqual(Array(bytes.prefix(4)), [0x82, 0x7E, 0x01, 0x00])
        XCTAssertEqual(bytes.count, 4 + 256)

        var buffer = bytes
        let frame = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(frame.opcode, .binary)
        XCTAssertEqual(frame.payload, payload)
    }

    func testSixtyFourBitLength() throws {
        let payload = Data(repeating: 0x42, count: 65536)
        let bytes = WebSocketFrame.encode(.binary(payload))
        XCTAssertEqual(Array(bytes.prefix(10)), [0x82, 0x7F, 0, 0, 0, 0, 0, 1, 0, 0])

        var buffer = bytes
        let frame = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(frame.payload, payload)
    }

    func testMaskedRoundTripLongPayload() throws {
        let payload = Data((0..<70_000).map { UInt8(truncatingIfNeeded: $0 &* 31) })
        var buffer = WebSocketFrame.encode(.binary(payload), masked: true)
        let frame = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer, requireMask: true))
        XCTAssertEqual(frame.payload, payload)
        XCTAssertTrue(buffer.isEmpty)
    }

    func testIncompleteFrameReturnsNil() throws {
        var buffer = Data([0x81, 0x05, 0x48, 0x65])
        XCTAssertNil(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(buffer.count, 4)

        var header = Data([0x82, 0x7E, 0x01])
        XCTAssertNil(try WebSocketFrame.decode(from: &header))

        var masked = Data([0x81, 0x85, 0x37, 0xfa])
        XCTAssertNil(try WebSocketFrame.decode(from: &masked))
    }

    func testRejectsOversizedFrame() {
        // 64-bit length announcing 2 MiB.
        var buffer = Data([0x82, 0x7F, 0, 0, 0, 0, 0, 0x20, 0, 0])
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &buffer)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .tooLarge)
        }
    }

    func testRejectsReservedBitsAndUnknownOpcodes() {
        var rsv = Data([0xC1, 0x00])
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &rsv)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .reservedBitsSet)
        }
        var opcode = Data([0x83, 0x00])
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &opcode)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .unknownOpcode(3))
        }
    }

    func testRejectsFragmentedOrLongControlFrames() {
        var fragmented = Data([0x09, 0x00]) // ping without FIN
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &fragmented)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .invalidControlFrame)
        }
        var long = Data([0x89, 0x7E, 0x00, 0x7E]) // ping with 126-byte payload
        XCTAssertThrowsError(try WebSocketFrame.decode(from: &long)) { error in
            XCTAssertEqual(error as? WebSocketFrameError, .invalidControlFrame)
        }
    }

    func testTwoFramesInOneBuffer() throws {
        var buffer = WebSocketFrame.encode(.text("a"), masked: true)
        buffer.append(WebSocketFrame.encode(.text("b"), masked: true))
        XCTAssertEqual(try WebSocketFrame.decode(from: &buffer, requireMask: true)?.textPayload, "a")
        XCTAssertEqual(try WebSocketFrame.decode(from: &buffer, requireMask: true)?.textPayload, "b")
        XCTAssertNil(try WebSocketFrame.decode(from: &buffer, requireMask: true))
    }

    func testCloseFrameCarriesCodeAndReason() throws {
        let close = WebSocketFrame.close(code: 1001, reason: "bye")
        XCTAssertEqual(Array(close.payload), [0x03, 0xE9, 0x62, 0x79, 0x65])
        XCTAssertEqual(close.closeCode, 1001)
        var buffer = WebSocketFrame.encode(close)
        let decoded = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(decoded.closeCode, 1001)
        XCTAssertNil(WebSocketFrame.text("x").closeCode)
    }

    func testFragmentedMessageFrames() throws {
        var buffer = WebSocketFrame.encode(WebSocketFrame(opcode: .text, fin: false, payload: Data("Hel".utf8)))
        buffer.append(WebSocketFrame.encode(WebSocketFrame(opcode: .continuation, fin: true, payload: Data("lo".utf8))))
        let first = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(first.opcode, .text)
        XCTAssertFalse(first.fin)
        let second = try XCTUnwrap(try WebSocketFrame.decode(from: &buffer))
        XCTAssertEqual(second.opcode, .continuation)
        XCTAssertTrue(second.fin)
    }

    func testAcceptKeyVector() {
        // RFC 6455 §1.3.
        XCTAssertEqual(WebSocketFrame.acceptKey(for: "dGhlIHNhbXBsZSBub25jZQ=="),
                       "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=")
    }
}
