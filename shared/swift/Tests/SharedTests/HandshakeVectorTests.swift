import XCTest
@testable import Shared
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// Replays `shared/testdata/edgehop/handshake-vectors.json`, produced by the Go
/// implementation (`go test ./shared/go/securechan -update`), and checks that the Swift side
/// derives the same hello bytes, keys and first ciphertexts.
///
/// Written defensively: skipped when the file is missing, and every field is compared only
/// when present, so the two implementations can land independently. Expected schema (one
/// object, or an array of them):
///
/// ```
/// {"initiator": {"staticPriv": hex, "ephemeralPriv": hex, "id", "name", "plat"},
///  "acceptor":  {…same…},
///  "pair": bool, "code": string|null,
///  "hello": string, "helloAck": string,
///  "keyInitToAcc": hex, "keyAccToInit": hex,
///  "firstInitFrame": hex, "firstAccFrame": hex}
/// ```
final class HandshakeVectorTests: XCTestCase {

    private static var vectorsURL: URL {
        if let override = ProcessInfo.processInfo.environment["EDGEHOP_VECTORS"] {
            return URL(fileURLWithPath: override)
        }
        // …/shared/swift/Tests/SharedTests/HandshakeVectorTests.swift → …/shared/
        return URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()  // SharedTests
            .deletingLastPathComponent()  // Tests
            .deletingLastPathComponent()  // swift
            .deletingLastPathComponent()  // shared
            .appendingPathComponent("testdata/edgehop/handshake-vectors.json")
    }

    private struct Side {
        let identity: Identity
        let ephemeral: Curve25519.KeyAgreement.PrivateKey
        let name: String
        let plat: String

        init(_ object: [String: Any]) throws {
            let staticHex = try XCTUnwrap(object["staticPriv"] as? String, "staticPriv")
            let ephemeralHex = try XCTUnwrap(object["ephemeralPriv"] as? String, "ephemeralPriv")
            identity = try Identity(rawPrivateKey: try XCTUnwrap(Data(hexString: staticHex)))
            ephemeral = try Curve25519.KeyAgreement.PrivateKey(
                rawRepresentation: try XCTUnwrap(Data(hexString: ephemeralHex)))
            name = object["name"] as? String ?? ""
            plat = object["plat"] as? String ?? ""
            if let id = object["id"] as? String {
                XCTAssertEqual(identity.id, id, "machine id derivation")
            }
        }

        var peerInfo: PeerInfo {
            PeerInfo(id: identity.id, name: name, plat: plat, staticPublicKey: identity.publicKeyData)
        }
    }

    func testVectors() throws {
        let url = HandshakeVectorTests.vectorsURL
        guard FileManager.default.fileExists(atPath: url.path) else {
            throw XCTSkip("no handshake vectors at \(url.path)")
        }
        let root = try JSONSerialization.jsonObject(with: try Data(contentsOf: url), options: [])
        let vectors: [[String: Any]]
        if let array = root as? [[String: Any]] {
            vectors = array
        } else if let object = root as? [String: Any] {
            vectors = (object["vectors"] as? [[String: Any]]) ?? [object]
        } else {
            return XCTFail("unexpected vector file layout")
        }
        XCTAssertFalse(vectors.isEmpty)
        for (index, vector) in vectors.enumerated() {
            try check(vector, label: "vector[\(index)]")
        }
    }

    private func check(_ vector: [String: Any], label: String) throws {
        let initiatorSide = try Side(try XCTUnwrap(vector["initiator"] as? [String: Any], "\(label).initiator"))
        let acceptorSide = try Side(try XCTUnwrap(vector["acceptor"] as? [String: Any], "\(label).acceptor"))
        let code = vector["code"] as? String
        let expectedHello = vector["hello"] as? String
        let expectedAck = vector["helloAck"] as? String

        // The `pair` flags each side put on the wire decide trust requirements; take them
        // from the recorded messages when present, else from the top-level flag.
        let topLevelPair = (vector["pair"] as? Bool) ?? (code != nil)
        let helloPair = expectedHello.flatMap(HandshakeVectorTests.pairFlag(in:)) ?? topLevelPair
        let ackPair = expectedAck.flatMap(HandshakeVectorTests.pairFlag(in:)) ?? false

        let initiatorTrust = MemoryTrustStore()
        let acceptorTrust = MemoryTrustStore()
        if !helloPair { acceptorTrust.trust(initiatorSide.peerInfo) }
        if !ackPair { initiatorTrust.trust(acceptorSide.peerInfo) }
        if !topLevelPair {
            acceptorTrust.trust(initiatorSide.peerInfo)
            initiatorTrust.trust(acceptorSide.peerInfo)
        }

        let initiator = Handshake(options: HandshakeOptions(
            identity: initiatorSide.identity, name: initiatorSide.name, plat: initiatorSide.plat,
            trust: initiatorTrust, pair: helloPair,
            ephemeralKeyOverride: initiatorSide.ephemeral, codeOverride: code), initiator: true)
        let acceptor = Handshake(options: HandshakeOptions(
            identity: acceptorSide.identity, name: acceptorSide.name, plat: acceptorSide.plat,
            trust: acceptorTrust, pair: ackPair,
            ephemeralKeyOverride: acceptorSide.ephemeral, codeOverride: code), initiator: false)

        // Hello bytes.
        let hello = try XCTUnwrap(try initiator.start())
        if let expectedHello {
            XCTAssertEqual(String(decoding: hello, as: UTF8.self), expectedHello, "\(label) hello bytes")
        }

        // Feed the recorded bytes (not ours) so key comparison is meaningful even if the
        // JSON text differs.
        let helloOnWire = expectedHello.map { Data($0.utf8) } ?? hello
        guard case .established(keys: let acceptorKeys, peer: let acceptorPeer, reply: let reply) = try acceptor.receive(helloOnWire),
              let ack = reply else {
            return XCTFail("\(label): acceptor did not establish")
        }
        XCTAssertEqual(acceptorPeer.id, initiatorSide.identity.id)
        if let expectedAck {
            XCTAssertEqual(String(decoding: ack, as: UTF8.self), expectedAck, "\(label) hello_ack bytes")
        }

        let ackOnWire = expectedAck.map { Data($0.utf8) } ?? ack
        guard case .established(keys: let initiatorKeys, peer: let initiatorPeer, reply: _) = try initiator.receive(ackOnWire) else {
            return XCTFail("\(label): initiator did not establish")
        }
        XCTAssertEqual(initiatorPeer.id, acceptorSide.identity.id)
        XCTAssertEqual(initiatorKeys, acceptorKeys, "\(label): both sides derive the same keys")

        // Keys.
        if let hex = vector["keyInitToAcc"] as? String {
            XCTAssertEqual(initiatorKeys.initiatorToAcceptor.hexString, hex.lowercased(), "\(label) key_init→acc")
        }
        if let hex = vector["keyAccToInit"] as? String {
            XCTAssertEqual(initiatorKeys.acceptorToInitiator.hexString, hex.lowercased(), "\(label) key_acc→init")
        }

        // First ciphertexts. The plaintext is `0x01 || {"t":"auth"}` per §3/§5; accept the
        // bare JSON too, and a frame with or without its length prefix, so the comparison is
        // strict on crypto but tolerant of how the fixture was serialised.
        if let hex = vector["firstInitFrame"] as? String {
            let recorded = try XCTUnwrap(Data(hexString: hex), "\(label) firstInitFrame hex")
            let plaintext = (vector["firstInitPlaintext"] as? String).flatMap(Data.init(hexString:))
            try checkFirstFrame(recorded, plaintext: plaintext,
                                candidates: [Handshake.authFrame, Data("{\"t\":\"auth\"}".utf8)],
                                sender: SecureChannel(keys: initiatorKeys, isInitiator: true),
                                receiver: SecureChannel(keys: acceptorKeys, isInitiator: false),
                                label: "\(label) firstInitFrame")
        }
        if let hex = vector["firstAccFrame"] as? String {
            let recorded = try XCTUnwrap(Data(hexString: hex), "\(label) firstAccFrame hex")
            let plaintext = (vector["firstAccPlaintext"] as? String).flatMap(Data.init(hexString:))
            try checkFirstFrame(recorded, plaintext: plaintext,
                                candidates: [Handshake.authOKFrame, Data("{\"t\":\"auth_ok\"}".utf8)],
                                sender: SecureChannel(keys: acceptorKeys, isInitiator: false),
                                receiver: SecureChannel(keys: initiatorKeys, isInitiator: true),
                                label: "\(label) firstAccFrame")
        }
    }

    /// `recorded` is either a bare `ciphertext||tag` or a whole `FrameCodec` frame around one.
    private func checkFirstFrame(_ recorded: Data, plaintext: Data?, candidates: [Data],
                                 sender: SecureChannel, receiver: SecureChannel, label: String) throws {
        var payload = recorded
        var framed = recorded
        if let unframed = try? FrameCodec.decode(from: &framed), framed.isEmpty {
            payload = unframed
        }

        // Our receiver must open the recorded ciphertext with counter 0.
        let opened = try receiver.open(payload)
        if let plaintext {
            XCTAssertEqual(opened, plaintext, "\(label): plaintext")
        } else {
            XCTAssertTrue(candidates.contains(opened), "\(label): unexpected plaintext \(opened.hexString)")
        }

        // And our sender must produce the identical bytes (deterministic nonce).
        XCTAssertEqual(try sender.seal(opened), payload, "\(label): ciphertext")
    }

    private static func pairFlag(in json: String) -> Bool? {
        guard let object = try? JSONSerialization.jsonObject(with: Data(json.utf8), options: []) as? [String: Any] else {
            return nil
        }
        if let flag = object["pair"] as? Bool { return flag }
        if let number = object["pair"] as? NSNumber { return number.boolValue }
        return nil
    }
}
