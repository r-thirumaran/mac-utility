import Foundation

/// `~/Library/Application Support/Reach/config.json` (`docs/ARCHITECTURE.md` §8), the same
/// shape the Go agent writes: `{name, port, agentId, pairings: [{device, token, createdAt,
/// lastSeen}], launchAtLogin}`. Every field is optional on read so a hand-edited or older
/// file still loads; `Pairings` fills in the defaults.
struct ReachConfig: Codable {

    /// One phone that may open `/ws`. The token is the secret.
    struct Pairing: Codable {
        var device: String
        var token: String
        var createdAt: Date
        var lastSeen: Date
    }

    var name: String
    var port: Int
    var agentId: String
    var pairings: [Pairing]
    var launchAtLogin: Bool

    init(name: String = "", port: Int = 0, agentId: String = "", pairings: [Pairing] = [], launchAtLogin: Bool = false) {
        self.name = name
        self.port = port
        self.agentId = agentId
        self.pairings = pairings
        self.launchAtLogin = launchAtLogin
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case name, port, agentId, pairings, launchAtLogin
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? 0
        agentId = try container.decodeIfPresent(String.self, forKey: .agentId) ?? ""
        pairings = try container.decodeIfPresent([Pairing].self, forKey: .pairings) ?? []
        launchAtLogin = try container.decodeIfPresent(Bool.self, forKey: .launchAtLogin) ?? false
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(name, forKey: .name)
        try container.encode(port, forKey: .port)
        try container.encode(agentId, forKey: .agentId)
        try container.encode(pairings, forKey: .pairings)
        try container.encode(launchAtLogin, forKey: .launchAtLogin)
    }
}
