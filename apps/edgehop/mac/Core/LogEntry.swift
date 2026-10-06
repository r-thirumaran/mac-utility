import Foundation

/// The payload of the `edgehop.log` event and of `edgehop.log.recent` (`core.LogEntry`).
struct LogEntry: Codable {
    var level: String
    var msg: String
    /// Milliseconds since the Unix epoch.
    var ts: Int64

    init(level: String, msg: String, ts: Int64 = Int64(Date().timeIntervalSince1970 * 1000)) {
        self.level = level
        self.msg = msg
        self.ts = ts
    }
}
