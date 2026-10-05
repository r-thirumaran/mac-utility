import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// Everything a `Handshake` needs besides the bytes on the wire.
///
/// Pairing codes: the **acceptor** generates the code and hands it to `showCode` for the UI;
/// the **initiator** needs the user to type it. The initiator gets it from, in order,
/// `codeOverride` (tests / a code already collected by the UI), `askCode` (a blocking prompt,
/// only sensible off the main thread), or — if neither is set — the handshake pauses with
/// `HandshakeStep.needCode` until `Handshake.resume(code:)` is called.
struct HandshakeOptions {

    /// This machine's long-term key.
    var identity: Identity
    /// Display name sent in `hello` / `hello_ack`.
    var name: String
    /// `macos`, `windows` or `linux`.
    var plat: String
    /// Known peers. Consulted on every handshake; written only after `auth`/`auth_ok`.
    var trust: any TrustStore
    /// `true` when the user initiated pairing in the UI and this side is willing to pair now.
    var pair: Bool
    /// Initiator only: synchronously obtain the code the acceptor is showing.
    var askCode: (() throws -> String)?
    /// Acceptor only: display the generated code for `peer` (for 120 s per the spec).
    var showCode: ((String, PeerInfo) -> Void)?
    /// Tests / vectors: use this ephemeral key instead of a random one.
    var ephemeralKeyOverride: Curve25519.KeyAgreement.PrivateKey?
    /// Tests / vectors: initiator — the code to use; acceptor — the code to generate.
    var codeOverride: String?

    init(identity: Identity,
         name: String,
         plat: String,
         trust: any TrustStore,
         pair: Bool = false,
         askCode: (() throws -> String)? = nil,
         showCode: ((String, PeerInfo) -> Void)? = nil,
         ephemeralKeyOverride: Curve25519.KeyAgreement.PrivateKey? = nil,
         codeOverride: String? = nil) {
        self.identity = identity
        self.name = name
        self.plat = plat
        self.trust = trust
        self.pair = pair
        self.askCode = askCode
        self.showCode = showCode
        self.ephemeralKeyOverride = ephemeralKeyOverride
        self.codeOverride = codeOverride
    }
}
