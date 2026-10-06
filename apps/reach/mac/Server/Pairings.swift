import Foundation

/// The config store: `ReachConfig` in memory and on disk (0600, it holds the phone tokens).
/// Every mutation is saved immediately. Safe to call from any thread — the HTTP auth closure
/// runs on the main actor, but nothing here depends on that.
final class Pairings {

    static let defaultPort = 7788
    static let maxDeviceName = 64

    private let lock = NSLock()
    private let path: URL?
    private var config: ReachConfig
    private let iso8601 = ISO8601DateFormatter()

    /// `~/Library/Application Support/Reach/config.json`.
    static func defaultPath() -> URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Library/Application Support")
        return base.appendingPathComponent("Reach", isDirectory: true).appendingPathComponent("config.json")
    }

    /// Loads `path` (a missing or unreadable file yields defaults) and fills in the agent id,
    /// name and port when they are absent, saving the result. `nil` keeps everything in memory.
    init(path: URL?, defaultName: String) {
        self.path = path
        var loaded = ReachConfig()
        if let path, let data = try? Data(contentsOf: path) {
            let decoder = JSONDecoder()
            decoder.dateDecodingStrategy = .iso8601
            do {
                loaded = try decoder.decode(ReachConfig.self, from: data)
            } catch {
                Log.error("config: \(path.path) is unreadable (\(error)); starting fresh", category: "config")
            }
        }
        var changed = false
        if loaded.agentId.isEmpty {
            loaded.agentId = UUID().uuidString.lowercased()
            changed = true
        }
        if loaded.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            loaded.name = defaultName.isEmpty ? "Mac" : defaultName
            changed = true
        }
        if loaded.port <= 0 || loaded.port > 65535 {
            loaded.port = Pairings.defaultPort
            changed = true
        }
        config = loaded
        if changed {
            saveLocked()
        }
    }

    // MARK: - Snapshot and updates

    var snapshot: ReachConfig {
        lock.lock()
        defer { lock.unlock() }
        return config
    }

    /// Applies `mutate` under the lock and saves.
    func update(_ mutate: (inout ReachConfig) -> Void) {
        lock.lock()
        mutate(&config)
        saveLocked()
        lock.unlock()
    }

    // MARK: - Pairings

    /// Stores a new token for `device`, replacing an older pairing of the same device name,
    /// and returns the token.
    func addPairing(device: String) -> String {
        let token = Pairings.newToken()
        let now = Date()
        lock.lock()
        config.pairings.removeAll { $0.device == device }
        config.pairings.append(ReachConfig.Pairing(device: device, token: token, createdAt: now, lastSeen: now))
        saveLocked()
        lock.unlock()
        return token
    }

    /// Removes every pairing of `device` and returns their tokens (empty when none).
    func revoke(device: String) -> [String] {
        lock.lock()
        defer { lock.unlock() }
        let tokens = config.pairings.filter { $0.device == device }.map(\.token)
        guard !tokens.isEmpty else { return [] }
        config.pairings.removeAll { $0.device == device }
        saveLocked()
        return tokens
    }

    /// The pairing holding `token`, compared in constant time per entry.
    func find(token: String) -> ReachConfig.Pairing? {
        guard !token.isEmpty else { return nil }
        lock.lock()
        defer { lock.unlock() }
        return config.pairings.first { Pairings.constantTimeEqual($0.token, token) }
    }

    /// Updates `lastSeen` of the pairing holding `token` (best effort).
    func touch(token: String) {
        guard !token.isEmpty else { return }
        lock.lock()
        defer { lock.unlock() }
        guard let index = config.pairings.firstIndex(where: { Pairings.constantTimeEqual($0.token, token) }) else {
            return
        }
        config.pairings[index].lastSeen = Date()
        saveLocked()
    }

    /// The pairings without tokens, newest first (`reach.state`).
    var publicPairings: [[String: Any]] {
        lock.lock()
        defer { lock.unlock() }
        return config.pairings
            .sorted { $0.createdAt > $1.createdAt }
            .map { pairing -> [String: Any] in
                [
                    "device": pairing.device,
                    "createdAt": iso8601.string(from: pairing.createdAt),
                    "lastSeen": iso8601.string(from: pairing.lastSeen),
                ]
            }
    }

    // MARK: - Disk

    /// Writes the config with 0600 permissions inside a 0700 directory. Caller holds the lock.
    private func saveLocked() {
        guard let path else { return }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        do {
            let data = try encoder.encode(config)
            let directory = path.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true,
                                                    attributes: [.posixPermissions: 0o700])
            try data.write(to: path, options: [.atomic])
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
        } catch {
            Log.error("config: save failed: \(error)", category: "config")
        }
    }

    // MARK: - Tokens

    /// 32 random bytes as unpadded base64url (`docs/reach-api.md` §1); 43 characters.
    static func newToken() -> String {
        var generator = SystemRandomNumberGenerator()
        let bytes = (0..<32).map { _ in UInt8.random(in: UInt8.min...UInt8.max, using: &generator) }
        return Data(bytes).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    /// Compares two strings without an early exit on the first differing byte.
    static func constantTimeEqual(_ a: String, _ b: String) -> Bool {
        let left = Array(a.utf8)
        let right = Array(b.utf8)
        guard left.count == right.count else { return false }
        var difference: UInt8 = 0
        for index in left.indices {
            difference |= left[index] ^ right[index]
        }
        return difference == 0
    }
}
