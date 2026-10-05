import Foundation
#if canImport(os)
import os
#endif

/// Unified logging (subsystem = the bundle identifier, per `docs/ARCHITECTURE.md` §8) with a
/// `print` fallback where `os` is unavailable (Linux tests).
///
/// Messages are logged `.public`: these apps log state transitions and errors, never
/// secrets — pairing codes and tokens must not be passed here.
enum Log {

    static let subsystem: String = Bundle.main.bundleIdentifier ?? "com.thiru.shared"

    static func info(_ message: String, category: String = "app") {
        #if canImport(os)
        logger(category).info("\(message, privacy: .public)")
        #else
        print("[\(category)] info: \(message)")
        #endif
    }

    static func error(_ message: String, category: String = "app") {
        #if canImport(os)
        logger(category).error("\(message, privacy: .public)")
        #else
        print("[\(category)] error: \(message)")
        #endif
    }

    static func debug(_ message: String, category: String = "app") {
        #if canImport(os)
        logger(category).debug("\(message, privacy: .public)")
        #else
        print("[\(category)] debug: \(message)")
        #endif
    }

    #if canImport(os)
    private static let lock = NSLock()
    private static var loggers: [String: Logger] = [:]

    private static func logger(_ category: String) -> Logger {
        lock.lock()
        defer { lock.unlock() }
        if let existing = loggers[category] { return existing }
        let created = Logger(subsystem: subsystem, category: category)
        loggers[category] = created
        return created
    }
    #endif
}
