import Foundation

/// A paired machine as remembered in `config.json` (`core.Peer`): its id, name, platform,
/// base64 static public key and the last `host:port` it was reached at.
struct StoredPeer: Codable, Equatable {

    var id: String
    var name: String
    var plat: String
    /// Base64 of the raw 32-byte X25519 public key.
    var staticPub: String
    var lastAddr: String
    /// Spec §4: the displays it last reported, so an offline machine still draws on the
    /// canvas and its boundaries still exist.
    var displays: [Display]

    init(id: String, name: String, plat: String, staticPub: String, lastAddr: String = "",
         displays: [Display] = []) {
        self.id = id
        self.name = name
        self.plat = plat
        self.staticPub = staticPub
        self.lastAddr = lastAddr
        self.displays = displays
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case id, name, plat, staticPub, lastAddr, displays
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        plat = try container.decodeIfPresent(String.self, forKey: .plat) ?? ""
        staticPub = try container.decodeIfPresent(String.self, forKey: .staticPub) ?? ""
        lastAddr = try container.decodeIfPresent(String.self, forKey: .lastAddr) ?? ""
        displays = try container.decodeIfPresent([Display].self, forKey: .displays) ?? []
    }

    /// `lastAddr` is `omitempty` on the Go side.
    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(name, forKey: .name)
        try container.encode(plat, forKey: .plat)
        try container.encode(staticPub, forKey: .staticPub)
        if !lastAddr.isEmpty {
            try container.encode(lastAddr, forKey: .lastAddr)
        }
        if !displays.isEmpty {
            try container.encode(displays, forKey: .displays)
        }
    }
}
