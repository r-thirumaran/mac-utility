import Foundation

/// An error a bridge handler throws to reach the UI as `{id, error: {code, message}}`.
/// Codes are the short snake_case strings of `docs/ARCHITECTURE.md` §4.
struct BridgeError: Error, Equatable {

    enum Code: String {
        case notAuthorized = "not_authorized"
        case needsPermission = "needs_permission"
        case notSupported = "not_supported"
        case invalidParams = "invalid_params"
        case internalError = "internal"
    }

    let code: String
    let message: String

    init(code: String, message: String) {
        self.code = code
        self.message = message
    }

    init(_ code: Code, _ message: String) {
        self.code = code.rawValue
        self.message = message
    }

    // MARK: - Conveniences

    static func notAuthorized(_ message: String = "not authorized") -> BridgeError {
        BridgeError(.notAuthorized, message)
    }

    static func needsPermission(_ message: String) -> BridgeError {
        BridgeError(.needsPermission, message)
    }

    static func notSupported(_ message: String = "not supported") -> BridgeError {
        BridgeError(.notSupported, message)
    }

    static func invalidParams(_ message: String = "invalid params") -> BridgeError {
        BridgeError(.invalidParams, message)
    }

    static func internalError(_ message: String) -> BridgeError {
        BridgeError(.internalError, message)
    }

    /// The `error` object of a bridge response.
    var asDictionary: [String: Any] {
        ["code": code, "message": message]
    }

    /// Wraps any error: `BridgeError` passes through, everything else becomes `internal`.
    static func wrap(_ error: Error) -> BridgeError {
        if let bridgeError = error as? BridgeError { return bridgeError }
        return BridgeError(.internalError, String(describing: error))
    }
}
