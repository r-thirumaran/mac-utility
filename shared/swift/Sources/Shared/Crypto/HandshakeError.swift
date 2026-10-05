import Foundation

/// Thrown by `Handshake` for things the peer did wrong (or the caller did out of order).
/// Any of them means: close the connection.
enum HandshakeError: Error, Equatable {
    /// `start` / `receive` / `resume` called in the wrong phase.
    case invalidState
    /// The frame was not a JSON object with the expected fields.
    case malformedMessage(String)
    /// A message of a type this phase does not accept.
    case unexpectedMessage(String)
    /// `v` was not 1.
    case unsupportedVersion(Int)
    /// `spk` / `epk` was not a valid 32-byte X25519 key.
    case badPublicKey
    /// The `id` field does not match SHA-256 of `spk`.
    case idMismatch
    /// X25519 agreement failed (low-order point).
    case keyAgreementFailed
}
