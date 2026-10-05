import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// A machine's long-term X25519 identity (`docs/edgehop-protocol.md` §2).
///
/// The private key is generated on first run and persisted by the app (config file, 0600);
/// `id` is what Bonjour advertises and what peers remember.
struct Identity {

    let privateKey: Curve25519.KeyAgreement.PrivateKey

    /// Raw 32-byte public key — the `spk` field of the handshake and the `pk` TXT entry.
    var publicKeyData: Data {
        privateKey.publicKey.rawRepresentation
    }

    /// Raw 32-byte private key, for persisting.
    var rawPrivateKey: Data {
        privateKey.rawRepresentation
    }

    /// `machineId`: lowercase hex of the first 16 bytes of SHA-256(public key).
    var id: String {
        Identity.machineID(forPublicKey: publicKeyData)
    }

    /// A fresh random identity.
    init() {
        privateKey = Curve25519.KeyAgreement.PrivateKey()
    }

    /// Restores a persisted identity. Throws if `rawPrivateKey` is not a valid 32-byte scalar.
    init(rawPrivateKey: Data) throws {
        privateKey = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: rawPrivateKey)
    }

    init(privateKey: Curve25519.KeyAgreement.PrivateKey) {
        self.privateKey = privateKey
    }

    /// The id a peer must present for a given static public key.
    static func machineID(forPublicKey publicKey: Data) -> String {
        Data(SHA256.hash(data: publicKey)).prefix(16).hexString
    }
}
