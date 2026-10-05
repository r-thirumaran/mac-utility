import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// The key derivation of `docs/edgehop-protocol.md` §4, as pure functions so the test vectors
/// can exercise each step:
///
/// ```
/// psk        = pairing ? HKDF-SHA256(ikm = UTF8(code), salt = "edgehop-pair", info = min(id)||max(id), 32)
///                      : 32 zero bytes
/// transcript = SHA-256("edgehop-v1" || hello_bytes || hello_ack_bytes)
/// okm        = HKDF-SHA256(ikm = dh1||dh2||dh3||psk, salt = transcript, info = "edgehop-v1-keys", 64)
/// ```
enum EdgehopKDF {

    static let protocolLabel = Data("edgehop-v1".utf8)
    static let pairingSalt = Data("edgehop-pair".utf8)
    static let keysInfo = Data("edgehop-v1-keys".utf8)

    /// The pairing pre-shared key, or 32 zero bytes for a trusted handshake (`code == nil`).
    static func presharedKey(code: String?, localID: String, peerID: String) -> Data {
        guard let code else { return Data(count: 32) }
        let low = min(localID, peerID)
        let high = max(localID, peerID)
        let key = HKDF<SHA256>.deriveKey(inputKeyMaterial: SymmetricKey(data: Data(code.utf8)),
                                         salt: pairingSalt,
                                         info: Data((low + high).utf8),
                                         outputByteCount: 32)
        return key.withUnsafeBytes { Data($0) }
    }

    /// `SHA-256("edgehop-v1" || hello || hello_ack)` over the exact bytes on the wire.
    static func transcriptHash(hello: Data, helloAck: Data) -> Data {
        var input = protocolLabel
        input.append(hello)
        input.append(helloAck)
        return Data(SHA256.hash(data: input))
    }

    /// Splits the 64-byte HKDF output into the two directional keys.
    static func sessionKeys(dh1: Data, dh2: Data, dh3: Data, psk: Data, transcript: Data) -> SecureChannelKeys {
        var ikm = dh1
        ikm.append(dh2)
        ikm.append(dh3)
        ikm.append(psk)
        let okm = HKDF<SHA256>.deriveKey(inputKeyMaterial: SymmetricKey(data: ikm),
                                         salt: transcript,
                                         info: keysInfo,
                                         outputByteCount: 64)
        let bytes = okm.withUnsafeBytes { Data($0) }
        return SecureChannelKeys(initiatorToAcceptor: Data(bytes.prefix(32)),
                                 acceptorToInitiator: Data(bytes.suffix(32)))
    }

    /// `X25519(priv, pub)` as raw bytes.
    static func agree(_ privateKey: Curve25519.KeyAgreement.PrivateKey,
                      _ publicKey: Curve25519.KeyAgreement.PublicKey) throws -> Data {
        let secret: SharedSecret
        do {
            secret = try privateKey.sharedSecretFromKeyAgreement(with: publicKey)
        } catch {
            throw HandshakeError.keyAgreementFailed
        }
        return secret.withUnsafeBytes { Data($0) }
    }
}
