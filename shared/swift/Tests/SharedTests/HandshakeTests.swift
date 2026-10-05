import XCTest
@testable import Shared
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// Runs the initiator and acceptor state machines against each other in memory.
final class HandshakeTests: XCTestCase {

    private struct Party {
        let identity = Identity()
        let trust = MemoryTrustStore()
        let name: String
        let plat: String

        var peerInfo: PeerInfo {
            PeerInfo(id: identity.id, name: name, plat: plat, staticPublicKey: identity.publicKeyData)
        }

        func options(pair: Bool = false,
                     askCode: (() throws -> String)? = nil,
                     showCode: ((String, PeerInfo) -> Void)? = nil,
                     code: String? = nil) -> HandshakeOptions {
            HandshakeOptions(identity: identity, name: name, plat: plat, trust: trust, pair: pair,
                             askCode: askCode, showCode: showCode, codeOverride: code)
        }
    }

    private final class Box<T> {
        var value: T?
    }

    private let mac = Party(name: "Thiru's Mac", plat: "macos")
    private let pc = Party(name: "Office PC", plat: "windows")

    // MARK: - Helpers

    /// Drives a full handshake and returns both sides' keys (initiator's, acceptor's) plus
    /// the handshakes themselves.
    private func complete(initiator: Handshake, acceptor: Handshake,
                          code: (() -> String)? = nil,
                          file: StaticString = #filePath, line: UInt = #line) throws
        -> (initiatorKeys: SecureChannelKeys, acceptorKeys: SecureChannelKeys)? {
        let hello = try XCTUnwrap(try initiator.start(), file: file, line: line)
        XCTAssertNil(try acceptor.start(), file: file, line: line)

        guard case .established(keys: let acceptorKeys, peer: let acceptorPeer, reply: let reply) = try acceptor.receive(hello),
              let ack = reply else {
            XCTFail("acceptor did not establish", file: file, line: line)
            return nil
        }
        XCTAssertEqual(acceptorPeer, mac.peerInfo, file: file, line: line)

        var step = try initiator.receive(ack)
        if case .needCode(peer: let peer) = step {
            XCTAssertEqual(peer.id, pc.identity.id, file: file, line: line)
            let typed = try XCTUnwrap(code?(), "no code available", file: file, line: line)
            step = try initiator.resume(code: typed)
        }
        guard case .established(keys: let initiatorKeys, peer: let initiatorPeer, reply: let none) = step else {
            XCTFail("initiator did not establish", file: file, line: line)
            return nil
        }
        XCTAssertNil(none, file: file, line: line)
        XCTAssertEqual(initiatorPeer, pc.peerInfo, file: file, line: line)
        return (initiatorKeys, acceptorKeys)
    }

    private func exchangeAuth(initiatorKeys: SecureChannelKeys, acceptorKeys: SecureChannelKeys,
                              file: StaticString = #filePath, line: UInt = #line) throws {
        let initiator = SecureChannel(keys: initiatorKeys, isInitiator: true)
        let acceptor = SecureChannel(keys: acceptorKeys, isInitiator: false)
        let auth = try initiator.seal(Handshake.authFrame)
        XCTAssertEqual(try acceptor.open(auth), Handshake.authFrame, file: file, line: line)
        let authOK = try acceptor.seal(Handshake.authOKFrame)
        XCTAssertEqual(try initiator.open(authOK), Handshake.authOKFrame, file: file, line: line)
    }

    // MARK: - Trusted

    func testTrustedHandshake() throws {
        mac.trust.trust(pc.peerInfo)
        pc.trust.trust(mac.peerInfo)
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: pc.options(), initiator: false)

        let keys = try XCTUnwrap(try complete(initiator: initiator, acceptor: acceptor))
        XCTAssertEqual(keys.initiatorKeys, keys.acceptorKeys)
        XCTAssertEqual(keys.initiatorKeys.initiatorToAcceptor.count, 32)
        XCTAssertEqual(keys.initiatorKeys.acceptorToInitiator.count, 32)
        XCTAssertNotEqual(keys.initiatorKeys.initiatorToAcceptor, keys.initiatorKeys.acceptorToInitiator)
        XCTAssertFalse(initiator.isPairing)
        XCTAssertFalse(acceptor.isPairing)
        XCTAssertNil(acceptor.pairingCode)
        XCTAssertEqual(initiator.peer, pc.peerInfo)
        XCTAssertEqual(acceptor.peer, mac.peerInfo)
        try exchangeAuth(initiatorKeys: keys.initiatorKeys, acceptorKeys: keys.acceptorKeys)
    }

    // MARK: - Pairing

    func testPairingHandshakeWithCodeFromAcceptorUI() throws {
        let shown = Box<String>()
        let initiator = Handshake(options: mac.options(pair: true), initiator: true)
        let acceptor = Handshake(options: pc.options(pair: true, showCode: { code, peer in
            shown.value = code
            XCTAssertEqual(peer.id, self.mac.identity.id)
        }), initiator: false)

        let keys = try XCTUnwrap(try complete(initiator: initiator, acceptor: acceptor, code: { shown.value ?? "" }))
        let code = try XCTUnwrap(shown.value)
        XCTAssertEqual(code.count, 6)
        XCTAssertTrue(code.allSatisfy { $0.isNumber })
        XCTAssertEqual(acceptor.pairingCode, code)
        XCTAssertEqual(initiator.pairingCode, code)
        XCTAssertTrue(initiator.isPairing)
        XCTAssertTrue(acceptor.isPairing)
        XCTAssertEqual(keys.initiatorKeys, keys.acceptorKeys)
        try exchangeAuth(initiatorKeys: keys.initiatorKeys, acceptorKeys: keys.acceptorKeys)

        // Nothing is trusted until the encrypted auth round trip succeeded.
        XCTAssertNil(mac.trust.trustedKey(for: pc.identity.id))
        XCTAssertNil(pc.trust.trustedKey(for: mac.identity.id))
        initiator.confirmAuthenticated()
        acceptor.confirmAuthenticated()
        XCTAssertEqual(mac.trust.trustedKey(for: pc.identity.id), pc.identity.publicKeyData)
        XCTAssertEqual(pc.trust.trustedKey(for: mac.identity.id), mac.identity.publicKeyData)
    }

    func testPairingWithAskCodeCallback() throws {
        let shown = Box<String>()
        let initiator = Handshake(options: mac.options(pair: true, askCode: { shown.value ?? "" }), initiator: true)
        let acceptor = Handshake(options: pc.options(showCode: { code, _ in shown.value = code }), initiator: false)
        let keys = try XCTUnwrap(try complete(initiator: initiator, acceptor: acceptor))
        XCTAssertEqual(keys.initiatorKeys, keys.acceptorKeys)
        try exchangeAuth(initiatorKeys: keys.initiatorKeys, acceptorKeys: keys.acceptorKeys)
    }

    func testPairingOnlyInitiatorFlagStillPairs() throws {
        // hello.pair = true from an unknown key; the acceptor's own flag is off but it is
        // not trusted either → a code is generated and the ack carries pair:false.
        let initiator = Handshake(options: mac.options(pair: true, code: "424242"), initiator: true)
        let acceptor = Handshake(options: pc.options(pair: false, code: "424242"), initiator: false)
        let keys = try XCTUnwrap(try complete(initiator: initiator, acceptor: acceptor))
        XCTAssertTrue(acceptor.isPairing)
        XCTAssertEqual(keys.initiatorKeys, keys.acceptorKeys)
    }

    func testWrongPairingCodeFailsWhenOpeningFirstFrame() throws {
        let initiator = Handshake(options: mac.options(pair: true, code: "000000"), initiator: true)
        let acceptor = Handshake(options: pc.options(pair: true, code: "123456"), initiator: false)
        let keys = try XCTUnwrap(try complete(initiator: initiator, acceptor: acceptor))
        XCTAssertNotEqual(keys.initiatorKeys, keys.acceptorKeys)

        let initiatorChannel = SecureChannel(keys: keys.initiatorKeys, isInitiator: true)
        let acceptorChannel = SecureChannel(keys: keys.acceptorKeys, isInitiator: false)
        let auth = try initiatorChannel.seal(Handshake.authFrame)
        XCTAssertThrowsError(try acceptorChannel.open(auth)) { error in
            XCTAssertEqual(error as? SecureChannelError, .decryptionFailed)
        }
        XCTAssertTrue(acceptorChannel.isBroken)
        // The transport only calls confirmAuthenticated() after the auth frame opened, which
        // it never did here — so nothing was trusted on either side.
        XCTAssertNil(pc.trust.trustedKey(for: mac.identity.id))
        XCTAssertNil(mac.trust.trustedKey(for: pc.identity.id))
    }

    // MARK: - Rejections

    func testUnknownPeerWithoutPairingGetsErrorFrame() throws {
        let initiator = Handshake(options: mac.options(pair: false), initiator: true)
        let acceptor = Handshake(options: pc.options(pair: false), initiator: false)
        let hello = try XCTUnwrap(try initiator.start())

        guard case .failed(reason: let reason, reply: let reply) = try acceptor.receive(hello) else {
            return XCTFail("expected .failed")
        }
        XCTAssertEqual(reason, "unpaired")
        let frame = try XCTUnwrap(reply)
        XCTAssertEqual(String(decoding: frame, as: UTF8.self), "{\"t\":\"error\",\"code\":\"unpaired\"}")

        // The initiator reports the peer's error and sends nothing back.
        guard case .failed(reason: let initiatorReason, reply: let none) = try initiator.receive(frame) else {
            return XCTFail("expected .failed on the initiator")
        }
        XCTAssertEqual(initiatorReason, "unpaired")
        XCTAssertNil(none)
        XCTAssertThrowsError(try initiator.receive(frame)) { error in
            XCTAssertEqual(error as? HandshakeError, .invalidState)
        }
    }

    func testInitiatorRejectsUnknownAcceptorWhenNobodyPairs() throws {
        // The acceptor trusts us but we have never seen it, and nobody asked to pair.
        pc.trust.trust(mac.peerInfo)
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: pc.options(), initiator: false)
        let hello = try XCTUnwrap(try initiator.start())
        guard case .established(keys: _, peer: _, reply: let reply) = try acceptor.receive(hello),
              let ack = reply else { return XCTFail("acceptor should establish") }
        guard case .failed(reason: let reason, reply: let errorFrame) = try initiator.receive(ack) else {
            return XCTFail("initiator should refuse")
        }
        XCTAssertEqual(reason, "unpaired")
        XCTAssertNotNil(errorFrame)
    }

    func testTamperedIdIsRejected() throws {
        pc.trust.trust(mac.peerInfo)
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: pc.options(), initiator: false)
        let hello = try XCTUnwrap(try initiator.start())
        var text = String(decoding: hello, as: UTF8.self)
        text = text.replacingOccurrences(of: "\"id\":\"" + mac.identity.id, with: "\"id\":\"" + pc.identity.id)
        XCTAssertThrowsError(try acceptor.receive(Data(text.utf8))) { error in
            XCTAssertEqual(error as? HandshakeError, .idMismatch)
        }
    }

    func testTamperedTranscriptBreaksKeys() throws {
        mac.trust.trust(pc.peerInfo)
        pc.trust.trust(mac.peerInfo)
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: pc.options(), initiator: false)
        let hello = try XCTUnwrap(try initiator.start())
        guard case .established(keys: let acceptorKeys, peer: _, reply: let reply) = try acceptor.receive(hello),
              let ack = reply else { return XCTFail() }

        // Flip the display name in flight: the id/keys still check out, but the transcript
        // hash differs, so the derived keys do not match and the first frame cannot be opened.
        let tampered = Data(String(decoding: ack, as: UTF8.self)
            .replacingOccurrences(of: "Office PC", with: "Evil PC").utf8)
        guard case .established(keys: let initiatorKeys, peer: _, reply: _) = try initiator.receive(tampered) else {
            return XCTFail()
        }
        XCTAssertNotEqual(initiatorKeys, acceptorKeys)
        let channel = SecureChannel(keys: acceptorKeys, isInitiator: false)
        XCTAssertThrowsError(try channel.open(SecureChannel(keys: initiatorKeys, isInitiator: true).seal(Handshake.authFrame)))
    }

    func testMalformedAndUnexpectedMessages() throws {
        let acceptor = Handshake(options: pc.options(), initiator: false)
        XCTAssertThrowsError(try acceptor.receive(Data("not json".utf8))) { error in
            XCTAssertEqual(error as? HandshakeError, .malformedMessage("not JSON"))
        }
        XCTAssertThrowsError(try acceptor.receive(Data("{\"t\":\"ping\"}".utf8))) { error in
            XCTAssertEqual(error as? HandshakeError, .unexpectedMessage("ping"))
        }
        XCTAssertThrowsError(try acceptor.receive(Data("{\"t\":\"hello\",\"v\":1}".utf8)))

        let initiator = Handshake(options: mac.options(), initiator: true)
        XCTAssertThrowsError(try initiator.receive(Data("{}".utf8))) { error in
            XCTAssertEqual(error as? HandshakeError, .invalidState)
        }
        XCTAssertThrowsError(try initiator.resume(code: "1")) { error in
            XCTAssertEqual(error as? HandshakeError, .invalidState)
        }
    }

    func testUnsupportedVersionIsRefused() throws {
        pc.trust.trust(mac.peerInfo)
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: pc.options(), initiator: false)
        let hello = try XCTUnwrap(try initiator.start())
        let v2 = Data(String(decoding: hello, as: UTF8.self).replacingOccurrences(of: "\"v\":1", with: "\"v\":2").utf8)
        guard case .failed(reason: let reason, reply: let reply) = try acceptor.receive(v2) else { return XCTFail() }
        XCTAssertEqual(reason, "unsupported_version")
        XCTAssertNotNil(reply)
    }

    // MARK: - Wire format

    func testHelloBytesAreExactlyTheSpecifiedJSON() throws {
        let ephemeral = Curve25519.KeyAgreement.PrivateKey()
        var options = mac.options(pair: true)
        options.ephemeralKeyOverride = ephemeral
        let initiator = Handshake(options: options, initiator: true)
        let hello = try XCTUnwrap(try initiator.start())

        let expected = "{\"t\":\"hello\",\"v\":1,\"id\":\"" + mac.identity.id
            + "\",\"name\":\"Thiru's Mac\",\"plat\":\"macos\",\"spk\":\"" + mac.identity.publicKeyData.base64EncodedString()
            + "\",\"epk\":\"" + ephemeral.publicKey.rawRepresentation.base64EncodedString()
            + "\",\"pair\":true}"
        XCTAssertEqual(String(decoding: hello, as: UTF8.self), expected)
        XCTAssertEqual(mac.identity.id.count, 32)
        XCTAssertEqual(mac.identity.id, mac.identity.id.lowercased())
    }

    func testHelloAckBytes() throws {
        pc.trust.trust(mac.peerInfo)
        let ephemeral = Curve25519.KeyAgreement.PrivateKey()
        var options = pc.options()
        options.ephemeralKeyOverride = ephemeral
        let initiator = Handshake(options: mac.options(), initiator: true)
        let acceptor = Handshake(options: options, initiator: false)
        let hello = try XCTUnwrap(try initiator.start())
        guard case .established(keys: _, peer: _, reply: let reply) = try acceptor.receive(hello), let ack = reply else {
            return XCTFail()
        }
        let expected = "{\"t\":\"hello_ack\",\"v\":1,\"id\":\"" + pc.identity.id
            + "\",\"name\":\"Office PC\",\"plat\":\"windows\",\"spk\":\"" + pc.identity.publicKeyData.base64EncodedString()
            + "\",\"epk\":\"" + ephemeral.publicKey.rawRepresentation.base64EncodedString()
            + "\",\"pair\":false}"
        XCTAssertEqual(String(decoding: ack, as: UTF8.self), expected)
    }

    func testNameEscapingMatchesGoEncodingJSON() {
        XCTAssertEqual(HandshakeMessage.escape("plain"), "plain")
        XCTAssertEqual(HandshakeMessage.escape("a\"b\\c"), "a\\\"b\\\\c")
        XCTAssertEqual(HandshakeMessage.escape("<Mac> & PC"), "\\u003cMac\\u003e \\u0026 PC")
        XCTAssertEqual(HandshakeMessage.escape("tab\there\nnl\r\u{08}\u{0C}\u{01}"), "tab\\there\\nnl\\r\\b\\f\\u0001")
        XCTAssertEqual(HandshakeMessage.escape("é😀\u{2028}"), "é😀\\u2028")
    }

    func testMessageRoundTrip() throws {
        let message = HandshakeMessage(type: "hello", id: mac.identity.id, name: "Thiru's \"Mac\" <2>",
                                       plat: "macos", spk: mac.identity.publicKeyData,
                                       epk: Data(repeating: 7, count: 32), pair: true)
        guard case .hello(let parsed) = try HandshakeMessage.parse(message.encoded()) else { return XCTFail() }
        XCTAssertEqual(parsed, message)
        XCTAssertEqual(try HandshakeMessage.parse(HandshakeMessage.errorFrame(code: "unpaired")),
                       .error(code: "unpaired", message: nil))
    }

    func testDeterministicWithFixedKeys() throws {
        func run() throws -> (Data, Data, SecureChannelKeys) {
            let e1 = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: Data(repeating: 1, count: 32))
            let e2 = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: Data(repeating: 2, count: 32))
            var a = mac.options(pair: true, code: "555555"); a.ephemeralKeyOverride = e1
            var b = pc.options(pair: true, code: "555555"); b.ephemeralKeyOverride = e2
            let initiator = Handshake(options: a, initiator: true)
            let acceptor = Handshake(options: b, initiator: false)
            let hello = try XCTUnwrap(try initiator.start())
            guard case .established(keys: let keys, peer: _, reply: let reply) = try acceptor.receive(hello),
                  let ack = reply else { throw HandshakeError.invalidState }
            return (hello, ack, keys)
        }
        let first = try run()
        let second = try run()
        XCTAssertEqual(first.0, second.0)
        XCTAssertEqual(first.1, second.1)
        XCTAssertEqual(first.2, second.2)
    }

    func testIdentityPersistence() throws {
        let restored = try Identity(rawPrivateKey: mac.identity.rawPrivateKey)
        XCTAssertEqual(restored.id, mac.identity.id)
        XCTAssertEqual(restored.publicKeyData, mac.identity.publicKeyData)
        XCTAssertThrowsError(try Identity(rawPrivateKey: Data(repeating: 0, count: 5)))
    }

    func testKDFPrimitives() {
        // psk uses min(id)||max(id) regardless of argument order.
        XCTAssertEqual(EdgehopKDF.presharedKey(code: "123456", localID: "bbb", peerID: "aaa"),
                       EdgehopKDF.presharedKey(code: "123456", localID: "aaa", peerID: "bbb"))
        XCTAssertNotEqual(EdgehopKDF.presharedKey(code: "123456", localID: "aaa", peerID: "bbb"),
                          EdgehopKDF.presharedKey(code: "123457", localID: "aaa", peerID: "bbb"))
        XCTAssertEqual(EdgehopKDF.presharedKey(code: nil, localID: "a", peerID: "b"), Data(count: 32))
        XCTAssertEqual(EdgehopKDF.transcriptHash(hello: Data("h".utf8), helloAck: Data("a".utf8)).count, 32)
        // The transcript is a plain concatenation (the JSON messages are self-delimiting), so only
        // genuinely different bytes must produce different hashes.
        XCTAssertNotEqual(EdgehopKDF.transcriptHash(hello: Data("h".utf8), helloAck: Data("a".utf8)),
                          EdgehopKDF.transcriptHash(hello: Data("h".utf8), helloAck: Data("b".utf8)))
    }

    func testGeneratedCodesAreSixDigits() {
        for _ in 0..<50 {
            let code = Handshake.generateCode()
            XCTAssertEqual(code.count, 6)
            XCTAssertTrue(code.allSatisfy { $0.isASCII && $0.isNumber })
        }
    }
}
