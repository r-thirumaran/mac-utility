import Foundation

/// Thrown by `SecureChannel`. Every case means the connection must be closed.
enum SecureChannelError: Error, Equatable {
    /// The 64-bit nonce counter for this direction wrapped.
    case counterExhausted
    /// Shorter than a Poly1305 tag.
    case malformedCiphertext
    /// Authentication failed: wrong key (wrong pairing code), replay, or tampering.
    case decryptionFailed
    /// A previous `open` failed; the channel is dead.
    case broken
}
