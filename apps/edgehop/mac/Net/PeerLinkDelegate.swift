import Foundation

/// What a `PeerLink` reports, always on the main actor.
@MainActor
protocol PeerLinkDelegate: AnyObject {
    /// `auth` / `auth_ok` succeeded: the link carries encrypted frames from now on.
    func linkDidEstablish(_ link: PeerLink, peer: PeerInfo)
    /// Initiator pairing: the acceptor is showing a code; call `link.resume(code:)`.
    func link(_ link: PeerLink, needsCodeFor peer: PeerInfo)
    /// Acceptor pairing: show this code to the user for 120 s.
    func link(_ link: PeerLink, showCode code: String, for peer: PeerInfo)
    /// A decrypted frame (`u8 type || body`).
    func link(_ link: PeerLink, didReceive frame: Data)
    /// The connection ended. `reason` is a short code (`wrong_code`, `unpaired`, `unreachable`,
    /// `cancelled`, `timeout`, `code_expired`, a remote error code …) or `nil` for a plain close.
    func link(_ link: PeerLink, didCloseWith reason: String?)
}
