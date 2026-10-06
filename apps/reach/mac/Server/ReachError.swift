import Foundation

/// A method failure with the wire shape of `docs/reach-api.md` §2: `{code, message, details?}`.
///
/// The shared `BridgeError` has no `details`; phones need `details.permission` on
/// `needs_permission` to tell the user which switch to flip, so the WebSocket path carries
/// this type and converts a `BridgeError` thrown by shared code with `wrap`.
struct ReachError: Error {

    static let needsPermission = "needs_permission"
    static let notSupported = "not_supported"
    static let invalidParams = "invalid_params"
    static let notFound = "not_found"
    static let notAuthorized = "not_authorized"
    static let internalError = "internal"

    let code: String
    let message: String
    /// Structured hints, e.g. `{"permission": "accessibility"}`. String-valued so the error
    /// stays `Sendable` like every `Error`.
    let details: [String: String]?

    init(code: String, message: String, details: [String: String]? = nil) {
        self.code = code
        self.message = message
        self.details = details
    }

    // MARK: - Conveniences

    static func invalidParams(_ message: String) -> ReachError {
        ReachError(code: invalidParams, message: message)
    }

    static func notFound(_ message: String) -> ReachError {
        ReachError(code: notFound, message: message)
    }

    static func notSupported(_ message: String) -> ReachError {
        ReachError(code: notSupported, message: message)
    }

    static func notAuthorized(_ message: String) -> ReachError {
        ReachError(code: notAuthorized, message: message)
    }

    static func internalError(_ message: String) -> ReachError {
        ReachError(code: internalError, message: message)
    }

    /// `permission` is `accessibility`, `screenRecording`, `inputMonitoring` (the contract's
    /// vocabulary) or `automation` for AppleScript consent.
    static func needsPermission(_ permission: String, _ message: String) -> ReachError {
        ReachError(code: needsPermission, message: message, details: ["permission": permission])
    }

    // MARK: - Conversions

    /// The `error` member of a WebSocket reply.
    var asDictionary: [String: Any] {
        var object: [String: Any] = ["code": code, "message": message]
        if let details {
            object["details"] = details
        }
        return object
    }

    /// The same failure for the `NativeBridge` (which has no `details` on the wire).
    var asBridgeError: BridgeError {
        BridgeError(code: code, message: message)
    }

    /// Any error as a `ReachError`: pass-through, a `BridgeError`'s code and message, or
    /// `internal` with the description.
    static func wrap(_ error: Error) -> ReachError {
        if let reachError = error as? ReachError { return reachError }
        if let bridgeError = error as? BridgeError {
            return ReachError(code: bridgeError.code, message: bridgeError.message)
        }
        return ReachError(code: internalError, message: String(describing: error))
    }
}
