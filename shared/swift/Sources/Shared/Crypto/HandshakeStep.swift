import Foundation

/// What the caller must do after feeding a frame to `Handshake.receive`.
///
/// Whenever a `reply` is present it must be written to the wire (as one `FrameCodec` frame)
/// before anything else happens on the connection.
enum HandshakeStep {
    /// Send this plaintext payload and keep waiting for the peer.
    case send(Data)
    /// Keys are derived. Send `reply` first if present (the acceptor's `hello_ack`), then
    /// switch to `SecureChannel` and exchange `auth` / `auth_ok`.
    case established(keys: SecureChannelKeys, peer: PeerInfo, reply: Data?)
    /// Initiator only: this is a pairing handshake and no code was supplied. Ask the user for
    /// the code shown on `peer`, then call `Handshake.resume(code:)`.
    case needCode(peer: PeerInfo)
    /// The handshake is over. Send `reply` if present (an `error` frame), then close.
    case failed(reason: String, reply: Data?)
}
