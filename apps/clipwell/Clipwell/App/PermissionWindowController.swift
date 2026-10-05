import AppKit
import SwiftUI

/// The onboarding / "we still need Accessibility" screen.
///
/// macOS shows its own system prompt only once per app version, and never again — so an app
/// that relies on it alone becomes permanently, silently broken for anyone who dismissed it.
/// This window is the durable path: it can be reopened any time, and its button deep-links
/// straight to the right System Settings pane.
final class PermissionWindowController: NSWindowController {

    convenience init() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 400),
            styleMask: [.titled, .closable, .fullSizeContentView],
            backing: .buffered,
            defer: false)
        window.title = "Clipwell Setup"
        window.titlebarAppearsTransparent = true
        window.isReleasedWhenClosed = false
        window.center()
        self.init(window: window)

        window.contentView = NSHostingView(rootView: PermissionView(
            onOpenSettings: { AccessibilityPermission.openSystemSettings() },
            onRecheck: { [weak self] in
                if AccessibilityPermission.isTrusted { self?.close() }
            },
            onQuit: { NSApp.terminate(nil) }))
    }

    func present() {
        // This is the one place Clipwell genuinely wants the user's attention, so it is also
        // the one place a background-only app should activate itself.
        NSApp.activate()
        window?.center()
        showWindow(nil)
        window?.makeKeyAndOrderFront(nil)
    }
}

private struct PermissionView: View {

    let onOpenSettings: () -> Void
    let onRecheck: () -> Void
    let onQuit: () -> Void

    @State private var isTrusted = AccessibilityPermission.isTrusted

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Image(systemName: "rectangle.split.2x1")
                    .font(.system(size: 28))
                    .foregroundStyle(Color.accentColor)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Clipwell needs Accessibility access")
                        .font(.system(size: 15, weight: .semibold))
                    Text("Window snapping and paste injection depend on it.")
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                }
            }

            Divider()

            VStack(alignment: .leading, spacing: 10) {
                step(1, "Click **Open Accessibility Settings** below.")
                step(2, "Unlock the pane if it is locked, then turn on the switch next to **Clipwell**.")
                step(3, "Come back here — the window closes itself once access is granted.")
            }

            Text("Clipwell uses this permission only to move windows and to press ⌘V for you. "
                 + "Nothing leaves your Mac.")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            Spacer()

            HStack(spacing: 8) {
                statusPill
                Spacer()
                Button("Quit", action: onQuit)
                Button("Open Accessibility Settings", action: onOpenSettings)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 460, height: 400)
        // No notification exists for a TCC grant, so poll while this window is up.
        .onReceive(Timer.publish(every: 1, on: .main, in: .common).autoconnect()) { _ in
            let trusted = AccessibilityPermission.isTrusted
            if trusted != isTrusted {
                isTrusted = trusted
                if trusted { onRecheck() }
            }
        }
    }

    private var statusPill: some View {
        HStack(spacing: 5) {
            Image(systemName: isTrusted ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                .foregroundStyle(isTrusted ? Color.green : Color.orange)
            Text(isTrusted ? "Access granted" : "Waiting for access")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
        }
    }

    private func step(_ number: Int, _ markdown: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Text("\(number)")
                .font(.system(size: 11, weight: .bold, design: .rounded))
                .frame(width: 18, height: 18)
                .background(Circle().fill(.quaternary))
            Text(.init(markdown))
                .font(.system(size: 12))
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}
