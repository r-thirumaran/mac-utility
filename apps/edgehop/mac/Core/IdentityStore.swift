import Foundation

/// Loads this machine's long-term X25519 key from `<dir>/identity.json`
/// (`{"version":1,"privateKey":"<base64 raw private key>"}`, the file `core/identity.go`
/// writes), creating a fresh one (mode 0600) on first run.
enum IdentityStore {

    static let fileName = "identity.json"

    enum LoadError: Error {
        case badPrivateKey(String)
    }

    private struct IdentityFile: Codable {
        var version: Int
        var privateKey: String
    }

    // MARK: - Loading

    static func load(from directory: URL) throws -> Identity {
        let url = directory.appendingPathComponent(fileName)
        if FileManager.default.fileExists(atPath: url.path) {
            let data = try Data(contentsOf: url)
            let file = try JSONDecoder().decode(IdentityFile.self, from: data)
            guard let raw = Data(base64Encoded: file.privateKey), raw.count == 32 else {
                throw LoadError.badPrivateKey(url.path)
            }
            return try Identity(rawPrivateKey: raw)
        }
        let identity = Identity()
        let file = IdentityFile(version: 1, privateKey: identity.rawPrivateKey.base64EncodedString())
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try PeerStore.writeProtected(try encoder.encode(file), to: url)
        return identity
    }

    /// `~/Library/Application Support/Edgehop`, created (0700) when missing.
    static func configDirectory() throws -> URL {
        let base = try FileManager.default.url(for: .applicationSupportDirectory,
                                               in: .userDomainMask,
                                               appropriateFor: nil,
                                               create: true)
        let dir = base.appendingPathComponent("Edgehop", isDirectory: true)
        try FileManager.default.createDirectory(at: dir,
                                                withIntermediateDirectories: true,
                                                attributes: [.posixPermissions: 0o700])
        return dir
    }
}
