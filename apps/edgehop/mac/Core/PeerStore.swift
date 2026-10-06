import Foundation

/// The persistent configuration: trusted peers (it is the `TrustStore` the handshake consults),
/// the layout, the settings and the machine name, in `config.json` with the same shape as
/// `core/peers.go` writes (`{version, name, peers, layout, settings}`), mode 0600. The
/// identity lives next to it in `identity.json` (`IdentityStore`), as on the Go side.
///
/// Every mutation is written through to disk. Thread-safe: the handshake calls `trust` from a
/// connection queue while the node reads on the main actor.
final class PeerStore: TrustStore {

    static let configFile = "config.json"

    /// The on-disk document.
    private struct ConfigFile: Codable {
        var version: Int
        var name: String?
        var peers: [StoredPeer]
        var layout: Layout
        var settings: EdgehopSettings

        private enum CodingKeys: String, CodingKey {
            case version, name, peers, layout, settings
        }

        init() {
            version = 1
            name = nil
            peers = []
            layout = Layout()
            settings = EdgehopSettings.defaults
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            version = try container.decodeIfPresent(Int.self, forKey: .version) ?? 1
            name = try container.decodeIfPresent(String.self, forKey: .name)
            peers = try container.decodeIfPresent([StoredPeer].self, forKey: .peers) ?? []
            layout = try container.decodeIfPresent(Layout.self, forKey: .layout) ?? Layout()
            settings = try container.decodeIfPresent(EdgehopSettings.self, forKey: .settings) ?? EdgehopSettings.defaults
        }

        func encode(to encoder: Encoder) throws {
            var container = encoder.container(keyedBy: CodingKeys.self)
            try container.encode(version, forKey: .version)
            if let name, !name.isEmpty {
                try container.encode(name, forKey: .name)
            }
            try container.encode(peers, forKey: .peers)
            try container.encode(layout, forKey: .layout)
            try container.encode(settings, forKey: .settings)
        }
    }

    private let lock = NSLock()
    private let fileURL: URL?
    private var config: ConfigFile

    /// A store that is never written to disk.
    init() {
        fileURL = nil
        config = ConfigFile()
    }

    /// Loads `<directory>/config.json`, creating defaults when it is missing.
    init(directory: URL) throws {
        let url = directory.appendingPathComponent(PeerStore.configFile)
        fileURL = url
        var loaded = ConfigFile()
        var needsSave = true
        if FileManager.default.fileExists(atPath: url.path) {
            let data = try Data(contentsOf: url)
            loaded = try JSONDecoder().decode(ConfigFile.self, from: data)
            needsSave = false
        }
        if loaded.version == 0 {
            loaded.version = 1
        }
        loaded.settings = loaded.settings.normalized()
        config = loaded
        if needsSave {
            try PeerStore.write(config, to: url)
        }
    }

    /// The config file path (`nil` for a memory store).
    var path: String? {
        fileURL?.path
    }

    // MARK: - Persistence

    private func saveLocked() {
        guard let fileURL else { return }
        do {
            try PeerStore.write(config, to: fileURL)
        } catch {
            Log.error("PeerStore: save failed: \(error)", category: "store")
        }
    }

    private static func write(_ config: ConfigFile, to url: URL) throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let data = try encoder.encode(config)
        try PeerStore.writeProtected(data, to: url)
    }

    /// Writes `data` atomically and keeps the file private to the user (0600), like
    /// `appdirs.SaveJSON` on the Go side.
    static func writeProtected(_ data: Data, to url: URL) throws {
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }

    // MARK: - Name

    /// The stored machine name ("" = use the default).
    var name: String {
        lock.lock()
        defer { lock.unlock() }
        return config.name ?? ""
    }

    func setName(_ name: String) {
        lock.lock()
        defer { lock.unlock() }
        config.name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        saveLocked()
    }

    // MARK: - TrustStore

    func trustedKey(for id: String) -> Data? {
        lock.lock()
        defer { lock.unlock() }
        guard let peer = config.peers.first(where: { $0.id == id }),
              let key = Data(base64Encoded: peer.staticPub), key.count == 32 else {
            return nil
        }
        return key
    }

    /// Adds or updates the peer, keeping its last address.
    func trust(_ peer: PeerInfo) {
        guard Identity.machineID(forPublicKey: peer.staticPublicKey) == peer.id else {
            Log.error("PeerStore: peer \(peer.id): id does not match key", category: "store")
            return
        }
        lock.lock()
        defer { lock.unlock() }
        let pub = peer.staticPublicKey.base64EncodedString()
        if let index = config.peers.firstIndex(where: { $0.id == peer.id }) {
            config.peers[index].name = peer.name
            config.peers[index].plat = peer.plat
            config.peers[index].staticPub = pub
        } else {
            config.peers.append(StoredPeer(id: peer.id, name: peer.name, plat: peer.plat, staticPub: pub))
            config.peers.sort { $0.id < $1.id }
        }
        saveLocked()
    }

    /// Whether `id` is paired with exactly this key.
    func isTrusted(_ id: String, key: Data) -> Bool {
        guard let stored = trustedKey(for: id) else { return false }
        return stored == key
    }

    // MARK: - Peers

    /// Removes a paired peer (and its layout tile).
    func forget(_ id: String) {
        lock.lock()
        defer { lock.unlock() }
        config.peers.removeAll { $0.id == id }
        config.layout = config.layout.without(id)
        saveLocked()
    }

    func peer(_ id: String) -> StoredPeer? {
        lock.lock()
        defer { lock.unlock() }
        return config.peers.first(where: { $0.id == id })
    }

    /// The paired machines sorted by id.
    var peers: [StoredPeer] {
        lock.lock()
        defer { lock.unlock() }
        return config.peers
    }

    /// Remembers where `id` was last reached (`host:port`).
    func setLastAddr(_ id: String, _ addr: String) {
        lock.lock()
        defer { lock.unlock() }
        guard let index = config.peers.firstIndex(where: { $0.id == id }),
              config.peers[index].lastAddr != addr else { return }
        config.peers[index].lastAddr = addr
        saveLocked()
    }

    /// Refreshes the name and platform of a paired peer.
    func updatePeerInfo(_ id: String, name: String, plat: String) {
        lock.lock()
        defer { lock.unlock() }
        guard let index = config.peers.firstIndex(where: { $0.id == id }) else { return }
        if config.peers[index].name == name && config.peers[index].plat == plat { return }
        config.peers[index].name = name
        config.peers[index].plat = plat
        saveLocked()
    }

    // MARK: - Layout

    var layout: Layout {
        lock.lock()
        defer { lock.unlock() }
        return config.layout
    }

    /// Stores `layout` (after `validate`).
    func setLayout(_ layout: Layout) throws {
        try layout.validate()
        lock.lock()
        defer { lock.unlock() }
        config.layout = layout.normalized()
        saveLocked()
    }

    // MARK: - Settings

    var settings: EdgehopSettings {
        lock.lock()
        defer { lock.unlock() }
        return config.settings
    }

    func setSettings(_ settings: EdgehopSettings) {
        lock.lock()
        defer { lock.unlock() }
        config.settings = settings.normalized()
        saveLocked()
    }
}
