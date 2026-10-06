import AppKit
import UserNotifications

/// Desktop notifications for the pairing code and a new pairing.
///
/// `UNUserNotificationCenter` needs a bundle with an identifier and a code signature (the
/// ad-hoc one from the build script is enough); an unbundled binary would throw an
/// Objective-C exception, so the centre is only touched when the process is a `.app`. The
/// delegate keeps banners visible while Reach itself is the active app. Not main-actor bound:
/// the centre calls back on its own queue.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {

    private let available: Bool

    override init() {
        available = Bundle.main.bundleIdentifier != nil && Bundle.main.bundleURL.pathExtension == "app"
        super.init()
        if available {
            UNUserNotificationCenter.current().delegate = self
        }
    }

    // MARK: - Authorization

    func requestAuthorization() {
        guard available else { return }
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { granted, error in
            if let error {
                Log.error("notifications: authorization failed: \(error)", category: "app")
            } else if !granted {
                Log.info("notifications: not granted; pairing codes show in the settings window only", category: "app")
            }
        }
    }

    // MARK: - Posting

    /// Posts a banner. The body may contain a pairing code, so it is never logged.
    func post(title: String, body: String) {
        guard available else { return }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request) { error in
            if let error {
                Log.error("notifications: post failed: \(error)", category: "app")
            }
        }
    }

    // MARK: - UNUserNotificationCenterDelegate

    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .sound])
    }
}
