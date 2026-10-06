import Foundation

/// A machine seen on the network (Bonjour or a typed address), as the transport reports it to
/// the node (`core.Discovered`). An empty `addr` means it disappeared.
struct Discovered {
    var id: String
    var name: String
    var plat: String
    var addr: String
    /// Base64 static public key from the TXT record ("" when unknown).
    var pk: String
}
