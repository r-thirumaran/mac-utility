import XCTest
@testable import Shared

final class FrameCodecTests: XCTestCase {

    func testEncodeHeaderIsBigEndianLength() {
        let frame = FrameCodec.encode(Data("hi".utf8))
        XCTAssertEqual(Array(frame), [0, 0, 0, 2, 0x68, 0x69])
    }

    func testEncodeEmptyPayload() {
        XCTAssertEqual(Array(FrameCodec.encode(Data())), [0, 0, 0, 0])
    }

    func testDecodeReturnsNilUntilComplete() throws {
        var buffer = Data([0, 0, 0, 5, 0x61, 0x62])
        XCTAssertNil(try FrameCodec.decode(from: &buffer))
        XCTAssertEqual(buffer.count, 6, "nothing is consumed while incomplete")

        buffer.append(contentsOf: [0x63, 0x64, 0x65])
        XCTAssertEqual(try FrameCodec.decode(from: &buffer), Data("abcde".utf8))
        XCTAssertTrue(buffer.isEmpty)
    }

    func testDecodeShortHeader() throws {
        var buffer = Data([0, 0, 0])
        XCTAssertNil(try FrameCodec.decode(from: &buffer))
    }

    func testDecodeTwoFramesInOneBuffer() throws {
        var buffer = FrameCodec.encode(Data("one".utf8))
        buffer.append(FrameCodec.encode(Data()))
        buffer.append(FrameCodec.encode(Data("three".utf8)))
        buffer.append(0xFF) // partial header of a fourth frame

        XCTAssertEqual(try FrameCodec.decode(from: &buffer), Data("one".utf8))
        XCTAssertEqual(try FrameCodec.decode(from: &buffer), Data())
        XCTAssertEqual(try FrameCodec.decode(from: &buffer), Data("three".utf8))
        XCTAssertNil(try FrameCodec.decode(from: &buffer))
        XCTAssertEqual(buffer, Data([0xFF]))
    }

    func testDecodeRejectsOversizedFrame() {
        var buffer = Data([0, 0x10, 0, 1]) // 1 MiB + 1
        XCTAssertThrowsError(try FrameCodec.decode(from: &buffer)) { error in
            XCTAssertEqual(error as? FrameError, .tooLarge)
        }
        XCTAssertEqual(buffer.count, 4, "header stays so the caller can close")
    }

    func testMaxLengthIsInclusive() throws {
        let payload = Data(repeating: 0xAB, count: FrameCodec.maxLength)
        var buffer = FrameCodec.encode(payload)
        XCTAssertEqual(try FrameCodec.decode(from: &buffer), payload)
    }

    func testDecodeWorksOnDataSliceWithNonZeroStartIndex() throws {
        var whole = Data([0xEE, 0xEE])
        whole.append(FrameCodec.encode(Data("slice".utf8)))
        var slice = whole[2...]
        XCTAssertEqual(slice.startIndex, 2)
        XCTAssertEqual(try FrameCodec.decode(from: &slice), Data("slice".utf8))
        XCTAssertTrue(slice.isEmpty)
    }

    func testRoundTripRandomSizes() throws {
        for size in [1, 3, 125, 126, 127, 255, 256, 65535, 65536, 100_000] {
            let payload = Data((0..<size).map { UInt8(truncatingIfNeeded: $0) })
            var buffer = FrameCodec.encode(payload)
            XCTAssertEqual(try FrameCodec.decode(from: &buffer), payload, "size \(size)")
        }
    }
}
