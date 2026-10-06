import Foundation

/// One machine as reported in `edgehop.state` (`core.Machine`). The optional fields are
/// `omitempty` on the Go side and are left out here too when empty.
struct Machine: Codable {

    var id: String
    var name: String
    var plat: String
    var version: String
    /// `self` | `online` | `offline` | `discovered`.
    var status: String
    var paired: Bool
    var active: Bool
    var addr: String
    var displays: [Display]
    /// Milliseconds since the Unix epoch; 0 = never.
    var lastSeen: Int64
    var secureInput: Bool

    init(id: String,
         name: String,
         plat: String,
         version: String = "",
         status: String,
         paired: Bool,
         active: Bool = false,
         addr: String = "",
         displays: [Display] = [],
         lastSeen: Int64 = 0,
         secureInput: Bool = false) {
        self.id = id
        self.name = name
        self.plat = plat
        self.version = version
        self.status = status
        self.paired = paired
        self.active = active
        self.addr = addr
        self.displays = displays
        self.lastSeen = lastSeen
        self.secureInput = secureInput
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case id, name, plat, version, status, paired, active, addr, displays, lastSeen, secureInput
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        plat = try container.decodeIfPresent(String.self, forKey: .plat) ?? ""
        version = try container.decodeIfPresent(String.self, forKey: .version) ?? ""
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        paired = try container.decodeIfPresent(Bool.self, forKey: .paired) ?? false
        active = try container.decodeIfPresent(Bool.self, forKey: .active) ?? false
        addr = try container.decodeIfPresent(String.self, forKey: .addr) ?? ""
        displays = try container.decodeIfPresent([Display].self, forKey: .displays) ?? []
        lastSeen = try container.decodeIfPresent(Int64.self, forKey: .lastSeen) ?? 0
        secureInput = try container.decodeIfPresent(Bool.self, forKey: .secureInput) ?? false
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(name, forKey: .name)
        try container.encode(plat, forKey: .plat)
        if !version.isEmpty {
            try container.encode(version, forKey: .version)
        }
        try container.encode(status, forKey: .status)
        try container.encode(paired, forKey: .paired)
        try container.encode(active, forKey: .active)
        if !addr.isEmpty {
            try container.encode(addr, forKey: .addr)
        }
        try container.encode(displays, forKey: .displays)
        if lastSeen != 0 {
            try container.encode(lastSeen, forKey: .lastSeen)
        }
        if secureInput {
            try container.encode(secureInput, forKey: .secureInput)
        }
    }
}
