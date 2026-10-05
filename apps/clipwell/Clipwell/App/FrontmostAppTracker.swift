import AppKit

/// Keeps a running record of the last application that was frontmost **other than Clipwell**.
///
/// Every paste needs a target, and asking `NSWorkspace.frontmostApplication` at paste time is
/// unreliable: opening an `NSStatusItem` menu activates Clipwell, so by the time a menu
/// handler runs, "frontmost" is us. Observing activations continuously sidesteps the whole
/// race — whichever entry point triggers a paste (panel or status menu), the target is
/// already known and correct.
final class FrontmostAppTracker {

    private(set) var lastExternalApp: NSRunningApplication?

    private let ownPID = ProcessInfo.processInfo.processIdentifier
    private var observer: NSObjectProtocol?

    init() {
        let workspace = NSWorkspace.shared

        if let app = workspace.frontmostApplication, app.processIdentifier != ownPID {
            lastExternalApp = app
        }

        observer = workspace.notificationCenter.addObserver(
            forName: NSWorkspace.didActivateApplicationNotification,
            object: nil,
            queue: .main
        ) { [weak self] note in
            guard let self,
                  let app = note.userInfo?[NSWorkspace.applicationUserInfoKey]
                      as? NSRunningApplication,
                  app.processIdentifier != self.ownPID else { return }
            self.lastExternalApp = app
        }
    }

    deinit {
        if let observer {
            NSWorkspace.shared.notificationCenter.removeObserver(observer)
        }
    }
}
