import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// The encrypted transport of `docs/edgehop-protocol.md` §3, built on the keys a `Handshake`
/// derived.
///
/// Every frame payload after the handshake is `ChaCha20-Poly1305(key_dir, nonce_dir,
/// plaintext)` with the 16-byte tag appended and no associated data. The nonce is 12 bytes:
/// four zero bytes then a big-endian `u64` counter that starts at 0 and increments per frame,
/// separately per direction. A counter that would wrap, a ciphertext that fails to
/// authenticate, or any frame after such a failure throws — and the caller must close.
///
/// Not thread-safe: drive it from the connection's queue.
final class SecureChannel {

    static let nonceLength = 12
    static let tagLength = 16

    private let sendKey: SymmetricKey
    private let receiveKey: SymmetricKey

    /// Next nonce counters. Internal so tests can push them to the wrap point.
    var sendCounter: UInt64 = 0
    var receiveCounter: UInt64 = 0
    private var sendExhausted = false
    private var receiveExhausted = false

    /// Set after a failed `open`; the channel refuses further work.
    private(set) var isBroken = false

    init(keys: SecureChannelKeys, isInitiator: Bool) {
        if isInitiator {
            sendKey = SymmetricKey(data: keys.initiatorToAcceptor)
            receiveKey = SymmetricKey(data: keys.acceptorToInitiator)
        } else {
            sendKey = SymmetricKey(data: keys.acceptorToInitiator)
            receiveKey = SymmetricKey(data: keys.initiatorToAcceptor)
        }
    }

    // MARK: - Sealing

    /// Encrypts one frame payload: `ciphertext || tag`.
    func seal(_ plaintext: Data) throws -> Data {
        guard !sendExhausted else { throw SecureChannelError.counterExhausted }
        let nonce = try ChaChaPoly.Nonce(data: SecureChannel.nonce(for: sendCounter))
        let box = try ChaChaPoly.seal(plaintext, using: sendKey, nonce: nonce)
        advanceSendCounter()
        // swift-crypto hands back slices of the combined box (non-zero startIndex on Linux);
        // return a fresh Data so callers can index from 0 and frame it directly.
        var out = Data(box.ciphertext)
        out.append(contentsOf: box.tag)
        return out
    }

    // MARK: - Opening

    /// Decrypts one frame payload produced by the peer's `seal`.
    func open(_ payload: Data) throws -> Data {
        guard !isBroken else { throw SecureChannelError.broken }
        guard !receiveExhausted else { throw SecureChannelError.counterExhausted }
        guard payload.count >= SecureChannel.tagLength else {
            isBroken = true
            throw SecureChannelError.malformedCiphertext
        }

        let split = payload.endIndex - SecureChannel.tagLength
        let ciphertext = payload[payload.startIndex..<split]
        let tag = payload[split..<payload.endIndex]
        let nonce = try ChaChaPoly.Nonce(data: SecureChannel.nonce(for: receiveCounter))

        let plaintext: Data
        do {
            let box = try ChaChaPoly.SealedBox(nonce: nonce, ciphertext: ciphertext, tag: tag)
            plaintext = try ChaChaPoly.open(box, using: receiveKey)
        } catch {
            isBroken = true
            throw SecureChannelError.decryptionFailed
        }
        advanceReceiveCounter()
        return plaintext
    }

    // MARK: - Nonces

    /// `4 zero bytes || u64 big-endian counter`.
    static func nonce(for counter: UInt64) -> Data {
        var data = Data(count: 4)
        for shift in stride(from: 56, through: 0, by: -8) {
            data.append(UInt8(truncatingIfNeeded: counter >> UInt64(shift)))
        }
        return data
    }

    private func advanceSendCounter() {
        let (next, overflow) = sendCounter.addingReportingOverflow(1)
        if overflow {
            sendExhausted = true
        } else {
            sendCounter = next
        }
    }

    private func advanceReceiveCounter() {
        let (next, overflow) = receiveCounter.addingReportingOverflow(1)
        if overflow {
            receiveExhausted = true
        } else {
            receiveCounter = next
        }
    }
}
