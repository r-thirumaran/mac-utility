import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// The Edgehop handshake of `docs/edgehop-protocol.md` §4 as a transport-agnostic state
/// machine: the caller moves plaintext `FrameCodec` payloads between the socket and this
/// object and acts on the returned `HandshakeStep`. Nothing here touches the network, so the
/// initiator and acceptor can be run against each other in a unit test.
///
/// ```
/// initiator                                   acceptor
///   start()  ──── hello ────────────────────▶  receive(hello)
///                                                 ├─ unknown key, pair=false → .failed(reply: error)
///                                                 └─ else derive keys     → .established(reply: hello_ack)
///   receive(hello_ack) ◀──── hello_ack ───────────┘
///     ├─ trusted or pairing with a code → .established
///     ├─ pairing, no code yet           → .needCode  … resume(code:) → .established
///     └─ unknown key, pair=false        → .failed(reply: error)
/// ```
///
/// After `.established` both sides build a `SecureChannel` and exchange the encrypted
/// `auth` / `auth_ok` control messages (`Handshake.authFrame` / `authOKFrame`); once that
/// succeeds the app calls `confirmAuthenticated()` so a pairing handshake is persisted.
/// Rate limiting of failed pairings and the 120 s code display are the app's job.
final class Handshake {

    private enum Phase {
        /// Nothing sent or received yet.
        case idle
        /// Initiator: `hello` is out, waiting for `hello_ack`.
        case sentHello
        /// Initiator: pairing handshake paused in `.needCode`.
        case awaitingCode
        /// Keys derived.
        case established
        /// Over; the caller closes the connection.
        case failed
    }

    /// Plaintext of the first encrypted frame, `u8 0x01 (control) || {"t":"auth"}`.
    static let authFrame = Data([0x01]) + Data("{\"t\":\"auth\"}".utf8)
    /// Plaintext of the acceptor's reply, `u8 0x01 (control) || {"t":"auth_ok"}`.
    static let authOKFrame = Data([0x01]) + Data("{\"t\":\"auth_ok\"}".utf8)

    let isInitiator: Bool

    private let options: HandshakeOptions
    private let ephemeral: Curve25519.KeyAgreement.PrivateKey
    private var phase: Phase = .idle

    /// The `hello` (initiator) or `hello_ack` (acceptor) bytes this side produced.
    private var localMessageBytes = Data()
    /// The bytes the peer sent — hashed exactly as received.
    private var remoteMessageBytes = Data()
    private var pendingPeer: PeerInfo?
    private var pendingPeerEphemeral = Data()
    /// The `pair` flag this side put on the wire.
    private var sentPairFlag = false

    /// The authenticated peer, once established.
    private(set) var peer: PeerInfo?
    /// `true` when either side's `pair` flag was set — a code was mixed into the keys.
    private(set) var isPairing = false
    /// The code in use (acceptor: generated; initiator: supplied). Never log it.
    private(set) var pairingCode: String?

    /// This side's ephemeral public key (the `epk` field).
    var ephemeralPublicKeyData: Data {
        ephemeral.publicKey.rawRepresentation
    }

    init(options: HandshakeOptions, initiator: Bool) {
        self.options = options
        self.isInitiator = initiator
        self.ephemeral = options.ephemeralKeyOverride ?? Curve25519.KeyAgreement.PrivateKey()
    }

    // MARK: - Driving

    /// Initiator: returns the `hello` payload to send. Acceptor: returns `nil`; wait for the
    /// peer's `hello` and feed it to `receive`.
    func start() throws -> Data? {
        guard phase == .idle else { throw HandshakeError.invalidState }
        guard isInitiator else { return nil }

        sentPairFlag = options.pair
        let hello = HandshakeMessage(type: "hello",
                                     id: options.identity.id,
                                     name: options.name,
                                     plat: options.plat,
                                     spk: options.identity.publicKeyData,
                                     epk: ephemeralPublicKeyData,
                                     pair: sentPairFlag)
        localMessageBytes = hello.encoded()
        phase = .sentHello
        return localMessageBytes
    }

    /// Feeds one plaintext frame payload from the peer.
    func receive(_ plaintext: Data) throws -> HandshakeStep {
        if isInitiator {
            return try receiveAsInitiator(plaintext)
        } else {
            return try receiveAsAcceptor(plaintext)
        }
    }

    /// Initiator, after `.needCode`: continue with the code the user typed.
    func resume(code: String) throws -> HandshakeStep {
        guard phase == .awaitingCode, let remotePeer = pendingPeer else {
            throw HandshakeError.invalidState
        }
        return try finishInitiator(peer: remotePeer, code: code)
    }

    /// Call once the first encrypted frame from the peer decrypted (`auth` on the acceptor,
    /// `auth_ok` on the initiator). Stores the peer in the trust store if this was a pairing.
    func confirmAuthenticated() {
        guard phase == .established, let peer, isPairing else { return }
        options.trust.trust(peer)
    }

    // MARK: - Acceptor

    private func receiveAsAcceptor(_ plaintext: Data) throws -> HandshakeStep {
        guard phase == .idle else { throw HandshakeError.invalidState }

        let message: HandshakeMessage
        switch try HandshakeMessage.parse(plaintext) {
        case .hello(let hello):
            message = hello
        case .error(let code, _):
            return fail(reason: code, reply: nil)
        case .helloAck:
            throw HandshakeError.unexpectedMessage("hello_ack")
        case .other(let type):
            throw HandshakeError.unexpectedMessage(type)
        }
        guard message.version == HandshakeMessage.protocolVersion else {
            return fail(reason: "unsupported_version",
                        reply: HandshakeMessage.errorFrame(code: "unsupported_version"))
        }

        let remotePeer = try peerInfo(from: message)
        let trusted = isTrusted(remotePeer)
        guard message.pair || trusted else {
            // §4: unknown static key with pair:false → error + close.
            return fail(reason: "unpaired", reply: HandshakeMessage.errorFrame(code: "unpaired"))
        }

        sentPairFlag = options.pair && !trusted
        isPairing = message.pair || sentPairFlag
        var code: String?
        if isPairing {
            // §4: the acceptor generates the code and shows it; the user types it on the
            // initiator.
            let generated = options.codeOverride ?? Handshake.generateCode()
            pairingCode = generated
            code = generated
            options.showCode?(generated, remotePeer)
        }

        let ack = HandshakeMessage(type: "hello_ack",
                                   id: options.identity.id,
                                   name: options.name,
                                   plat: options.plat,
                                   spk: options.identity.publicKeyData,
                                   epk: ephemeralPublicKeyData,
                                   pair: sentPairFlag)
        remoteMessageBytes = plaintext
        localMessageBytes = ack.encoded()

        let keys = try deriveKeys(peer: remotePeer,
                                  peerEphemeral: message.epk,
                                  code: code,
                                  hello: remoteMessageBytes,
                                  helloAck: localMessageBytes)
        peer = remotePeer
        phase = .established
        return .established(keys: keys, peer: remotePeer, reply: localMessageBytes)
    }

    // MARK: - Initiator

    private func receiveAsInitiator(_ plaintext: Data) throws -> HandshakeStep {
        guard phase == .sentHello else { throw HandshakeError.invalidState }

        let message: HandshakeMessage
        switch try HandshakeMessage.parse(plaintext) {
        case .helloAck(let ack):
            message = ack
        case .error(let code, _):
            return fail(reason: code, reply: nil)
        case .hello:
            throw HandshakeError.unexpectedMessage("hello")
        case .other(let type):
            throw HandshakeError.unexpectedMessage(type)
        }
        guard message.version == HandshakeMessage.protocolVersion else {
            return fail(reason: "unsupported_version",
                        reply: HandshakeMessage.errorFrame(code: "unsupported_version"))
        }

        let remotePeer = try peerInfo(from: message)
        let trusted = isTrusted(remotePeer)
        isPairing = sentPairFlag || message.pair
        guard isPairing || trusted else {
            return fail(reason: "unpaired", reply: HandshakeMessage.errorFrame(code: "unpaired"))
        }

        remoteMessageBytes = plaintext
        pendingPeer = remotePeer
        pendingPeerEphemeral = message.epk

        guard isPairing else {
            return try finishInitiator(peer: remotePeer, code: nil)
        }
        if let code = options.codeOverride {
            return try finishInitiator(peer: remotePeer, code: code)
        }
        if let askCode = options.askCode {
            return try finishInitiator(peer: remotePeer, code: try askCode())
        }
        phase = .awaitingCode
        return .needCode(peer: remotePeer)
    }

    private func finishInitiator(peer remotePeer: PeerInfo, code: String?) throws -> HandshakeStep {
        let keys = try deriveKeys(peer: remotePeer,
                                  peerEphemeral: pendingPeerEphemeral,
                                  code: code,
                                  hello: localMessageBytes,
                                  helloAck: remoteMessageBytes)
        pairingCode = code
        peer = remotePeer
        phase = .established
        return .established(keys: keys, peer: remotePeer, reply: nil)
    }

    // MARK: - Shared pieces

    private func fail(reason: String, reply: Data?) -> HandshakeStep {
        phase = .failed
        return .failed(reason: reason, reply: reply)
    }

    private func peerInfo(from message: HandshakeMessage) throws -> PeerInfo {
        guard message.spk.count == 32, message.epk.count == 32 else {
            throw HandshakeError.badPublicKey
        }
        guard Identity.machineID(forPublicKey: message.spk) == message.id else {
            throw HandshakeError.idMismatch
        }
        return PeerInfo(id: message.id, name: message.name, plat: message.plat,
                        staticPublicKey: message.spk)
    }

    private func isTrusted(_ remotePeer: PeerInfo) -> Bool {
        options.trust.trustedKey(for: remotePeer.id) == remotePeer.staticPublicKey
    }

    /// §4 key derivation. `hello` / `helloAck` are the exact wire bytes.
    private func deriveKeys(peer remotePeer: PeerInfo,
                            peerEphemeral: Data,
                            code: String?,
                            hello: Data,
                            helloAck: Data) throws -> SecureChannelKeys {
        let peerStatic: Curve25519.KeyAgreement.PublicKey
        let peerEphemeralKey: Curve25519.KeyAgreement.PublicKey
        do {
            peerStatic = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: remotePeer.staticPublicKey)
            peerEphemeralKey = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: peerEphemeral)
        } catch {
            throw HandshakeError.badPublicKey
        }

        // dh1 = X25519(e_init, e_acc), dh2 = X25519(s_init, e_acc), dh3 = X25519(e_init, s_acc).
        let dh1 = try EdgehopKDF.agree(ephemeral, peerEphemeralKey)
        let dh2: Data
        let dh3: Data
        if isInitiator {
            dh2 = try EdgehopKDF.agree(options.identity.privateKey, peerEphemeralKey)
            dh3 = try EdgehopKDF.agree(ephemeral, peerStatic)
        } else {
            dh2 = try EdgehopKDF.agree(ephemeral, peerStatic)
            dh3 = try EdgehopKDF.agree(options.identity.privateKey, peerEphemeralKey)
        }

        let psk = EdgehopKDF.presharedKey(code: code, localID: options.identity.id, peerID: remotePeer.id)
        let transcript = EdgehopKDF.transcriptHash(hello: hello, helloAck: helloAck)
        return EdgehopKDF.sessionKeys(dh1: dh1, dh2: dh2, dh3: dh3, psk: psk, transcript: transcript)
    }

    /// A random 6-digit code, zero-padded, from the system CSPRNG.
    static func generateCode() -> String {
        let number = Int.random(in: 0...999_999)
        let digits = String(number)
        return String(repeating: "0", count: 6 - digits.count) + digits
    }
}
