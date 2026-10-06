import Foundation

/// Polls the two permissions Edgehop needs (Accessibility to inject and swallow input, Input
/// Monitoring to see the keyboard) while either is missing. macOS sends no notification for
/// a grant, so the state badges and the event tap can only follow by looking again.
@MainActor
final class PermissionWatcher {

    private static let interval: TimeInterval = 2

    /// Fired on the main actor whenever the pair of flags changes.
    var onChange: ((_ accessibility: Bool, _ inputMonitoring: Bool) -> Void)?

    private var timer: DispatchSourceTimer?
    private var last: (Bool, Bool) = (false, false)

    // MARK: - Control

    /// Shows the system prompts once (macOS shows each at most once per app version) and
    /// starts polling.
    func start() {
        last = (Permissions.accessibilityGranted, Permissions.inputMonitoringGranted)
        if !last.0 {
            Permissions.requestAccessibility()
        }
        if !last.1 {
            Permissions.requestInputMonitoring()
        }
        guard timer == nil, !(last.0 && last.1) else { return }
        let source = DispatchSource.makeTimerSource(queue: .main)
        source.schedule(deadline: .now() + PermissionWatcher.interval, repeating: PermissionWatcher.interval, leeway: .milliseconds(500))
        source.setEventHandler { [weak self] in
            MainActor.assumeIsolated { self?.poll() }
        }
        source.resume()
        timer = source
    }

    func stop() {
        timer?.cancel()
        timer = nil
    }

    private func poll() {
        let now = (Permissions.accessibilityGranted, Permissions.inputMonitoringGranted)
        guard now != last else { return }
        last = now
        onChange?(now.0, now.1)
        if now.0 && now.1 {
            stop()
        }
    }
}
