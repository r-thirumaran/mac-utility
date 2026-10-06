import Foundation

/// The outstanding pairing code and the strike counter (`docs/reach-api.md` §1): a code
/// lives 120 s and is single-use; while it is valid every request (the laptop's "Show pairing
/// code" or a phone's `/api/pair/request`) returns the same code, so both sides agree. Five
/// wrong, missing or expired codes within ten minutes lock pairing for ten minutes.
final class Pairer {

    static let codeTTL: TimeInterval = 120
    static let maxFailures = 5
    static let failureWindow: TimeInterval = 600
    static let lockoutPeriod: TimeInterval = 600

    /// Outcome of `redeem`.
    enum Outcome {
        case ok
        case badCode
        case locked
    }

    private let lock = NSLock()
    private var code = ""
    private var expires = Date.distantPast
    private var device = ""
    private var failures: [Date] = []
    private var lockedUntil = Date.distantPast

    // MARK: - Lockout

    /// When the lockout ends, or `nil` when pairing is open.
    var lockedUntilDate: Date? {
        lock.lock()
        defer { lock.unlock() }
        return isLockedLocked(Date()) ? lockedUntil : nil
    }

    private func isLockedLocked(_ now: Date) -> Bool {
        now < lockedUntil
    }

    // MARK: - Codes

    /// The valid code, if one is outstanding (not consumed, not expired, not locked). Does
    /// not issue a new one.
    var outstanding: (code: String, expiresAt: Date)? {
        lock.lock()
        defer { lock.unlock() }
        let now = Date()
        guard !isLockedLocked(now), !code.isEmpty, now < expires else { return nil }
        return (code, expires)
    }

    /// The code to show for `device`: the outstanding one while it is valid, otherwise a fresh
    /// one. `nil` during a lockout.
    func request(device: String) -> (code: String, expiresAt: Date)? {
        lock.lock()
        defer { lock.unlock() }
        let now = Date()
        guard !isLockedLocked(now) else { return nil }
        if code.isEmpty || now >= expires {
            code = Pairer.sixDigits()
            expires = now.addingTimeInterval(Pairer.codeTTL)
        }
        if !device.isEmpty {
            self.device = device
        }
        return (code, expires)
    }

    /// Checks `candidate`. A wrong, missing or expired code is a strike; the fifth strike
    /// within the window starts a lockout. A right code is consumed.
    func redeem(_ candidate: String) -> Outcome {
        lock.lock()
        defer { lock.unlock() }
        let now = Date()
        guard !isLockedLocked(now) else { return .locked }
        if !code.isEmpty, now < expires, Pairings.constantTimeEqual(candidate, code) {
            code = ""
            expires = .distantPast
            device = ""
            failures = []
            return .ok
        }
        failures = failures.filter { now.timeIntervalSince($0) < Pairer.failureWindow }
        failures.append(now)
        if failures.count >= Pairer.maxFailures {
            lockedUntil = now.addingTimeInterval(Pairer.lockoutPeriod)
            failures = []
            code = ""
            expires = .distantPast
            return .locked
        }
        return .badCode
    }

    /// A uniformly random 6-digit code. `SystemRandomNumberGenerator` is cryptographically
    /// secure on Apple platforms. Never logged.
    static func sixDigits() -> String {
        String(format: "%06d", Int.random(in: 0..<1_000_000))
    }
}
