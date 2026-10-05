import XCTest
@testable import Shared

final class SecureChannelTests: XCTestCase {

    private let keys = SecureChannelKeys(initiatorToAcceptor: Data(repeating: 0x11, count: 32),
                                         acceptorToInitiator: Data(repeating: 0x22, count: 32))

    private func pair() -> (initiator: SecureChannel, acceptor: SecureChannel) {
        (SecureChannel(keys: keys, isInitiator: true), SecureChannel(keys: keys, isInitiator: false))
    }

    func testNonceLayout() {
        XCTAssertEqual(Array(SecureChannel.nonce(for: 0)), [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0])
        XCTAssertEqual(Array(SecureChannel.nonce(for: 1)), [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1])
        XCTAssertEqual(Array(SecureChannel.nonce(for: 0x0102030405060708)),
                       [0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8])
        XCTAssertEqual(SecureChannel.nonce(for: UInt64.max).count, 12)
    }

    func testRoundTripBothDirections() throws {
        let (initiator, acceptor) = pair()
        let message = Data([0x02, 0x00, 0x05, 0xFF, 0xFB])
        let sealed = try initiator.seal(message)
        XCTAssertEqual(sealed.count, message.count + 16)
        XCTAssertEqual(try acceptor.open(sealed), message)

        let reply = try acceptor.seal(Data("{\"t\":\"pong\"}".utf8))
        XCTAssertEqual(try initiator.open(reply), Data("{\"t\":\"pong\"}".utf8))
        XCTAssertEqual(initiator.sendCounter, 1)
        XCTAssertEqual(initiator.receiveCounter, 1)
        XCTAssertEqual(acceptor.sendCounter, 1)
        XCTAssertEqual(acceptor.receiveCounter, 1)
    }

    func testEmptyPlaintext() throws {
        let (initiator, acceptor) = pair()
        let sealed = try initiator.seal(Data())
        XCTAssertEqual(sealed.count, 16)
        XCTAssertEqual(try acceptor.open(sealed), Data())
    }

    func testCounterMakesIdenticalPlaintextsDiffer() throws {
        let (initiator, acceptor) = pair()
        let a = try initiator.seal(Data("same".utf8))
        let b = try initiator.seal(Data("same".utf8))
        XCTAssertNotEqual(a, b)
        XCTAssertEqual(try acceptor.open(a), Data("same".utf8))
        XCTAssertEqual(try acceptor.open(b), Data("same".utf8))
    }

    func testOutOfOrderFrameFailsAndBreaksChannel() throws {
        let (initiator, acceptor) = pair()
        let first = try initiator.seal(Data("1".utf8))
        let second = try initiator.seal(Data("2".utf8))
        XCTAssertThrowsError(try acceptor.open(second)) { error in
            XCTAssertEqual(error as? SecureChannelError, .decryptionFailed)
        }
        XCTAssertTrue(acceptor.isBroken)
        XCTAssertThrowsError(try acceptor.open(first)) { error in
            XCTAssertEqual(error as? SecureChannelError, .broken)
        }
    }

    func testTamperedCiphertextFails() throws {
        let (initiator, acceptor) = pair()
        var sealed = try initiator.seal(Data("hello".utf8))
        sealed[sealed.startIndex] ^= 0x01
        XCTAssertThrowsError(try acceptor.open(sealed)) { error in
            XCTAssertEqual(error as? SecureChannelError, .decryptionFailed)
        }
    }

    func testTooShortCiphertext() {
        let (_, acceptor) = pair()
        XCTAssertThrowsError(try acceptor.open(Data(repeating: 0, count: 15))) { error in
            XCTAssertEqual(error as? SecureChannelError, .malformedCiphertext)
        }
    }

    func testWrongDirectionKeyFails() throws {
        // Two initiators share keys but use the wrong direction for each other.
        let a = SecureChannel(keys: keys, isInitiator: true)
        let b = SecureChannel(keys: keys, isInitiator: true)
        XCTAssertThrowsError(try b.open(try a.seal(Data("x".utf8))))
    }

    func testCounterWrapThrows() throws {
        let (initiator, acceptor) = pair()
        initiator.sendCounter = UInt64.max
        acceptor.receiveCounter = UInt64.max
        let last = try initiator.seal(Data("last".utf8))
        XCTAssertEqual(try acceptor.open(last), Data("last".utf8))
        XCTAssertThrowsError(try initiator.seal(Data("one too many".utf8))) { error in
            XCTAssertEqual(error as? SecureChannelError, .counterExhausted)
        }
        XCTAssertThrowsError(try acceptor.open(last)) { error in
            XCTAssertEqual(error as? SecureChannelError, .counterExhausted)
        }
    }

    func testHexHelpers() {
        XCTAssertEqual(Data([0x00, 0xAB, 0xFF]).hexString, "00abff")
        XCTAssertEqual(Data(hexString: "00abFF"), Data([0x00, 0xAB, 0xFF]))
        XCTAssertEqual(Data(hexString: ""), Data())
        XCTAssertNil(Data(hexString: "abc"))
        XCTAssertNil(Data(hexString: "zz"))
    }
}
