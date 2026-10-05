import AppKit
import SwiftUI

struct SettingsView: View {

    @ObservedObject var preferences: Preferences
    @ObservedObject var store: ClipboardStore

    @State private var accessibilityGranted = AccessibilityPermission.isTrusted
    @State private var screenshotStatusText = "Starting…"

    var body: some View {
        TabView {
            shortcutsTab
                .tabItem { Label("Shortcuts", systemImage: "keyboard") }
            clipboardTab
                .tabItem { Label("Clipboard", systemImage: "doc.on.clipboard") }
            generalTab
                .tabItem { Label("General", systemImage: "gearshape") }
        }
        .frame(width: 520, height: 440)
        .onReceive(Timer.publish(every: 2, on: .main, in: .common).autoconnect()) { _ in
            accessibilityGranted = AccessibilityPermission.isTrusted
        }
        .onReceive(NotificationCenter.default.publisher(
            for: ScreenshotWatcher.statusDidChange)) { note in
            guard let watcher = note.object as? ScreenshotWatcher else { return }
            switch watcher.status {
            case .off:
                screenshotStatusText = "Off"
            case .waitingForPermission:
                // The TCC prompt can sit unseen behind other windows, so say so rather than
                // leaving a spinner that looks like a hang.
                screenshotStatusText = "Waiting for permission — look for the macOS prompt"
            case .watching(let path):
                screenshotStatusText = "Watching "
                    + path.replacingOccurrences(of: NSHomeDirectory(), with: "~")
            case .failed(let message):
                screenshotStatusText = message
            }
        }
    }

    // MARK: - Shortcuts

    private var shortcutsTab: some View {
        Form {
            Section {
                recorder("Snap Left Half", $preferences.snapLeft)
                recorder("Snap Right Half", $preferences.snapRight)
                recorder("Maximize", $preferences.maximize)
                recorder("Restore / Minimize", $preferences.restoreOrMinimize)
            } header: {
                Text("Window Snapping")
            } footer: {
                Text("Press Left or Right again on a window that is already snapped to move it "
                     + "to the next display.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Clipboard") {
                recorder("Show History Panel", $preferences.showClipboard)
            }

            Section {
                Toggle("Also use ⌘← and ⌘→ to snap", isOn: $preferences.cmdArrowMode)
                if preferences.cmdArrowMode {
                    Label {
                        Text("⌘← and ⌘→ normally jump to the start and end of a line, and go "
                             + "back and forward in browsers. While this is on, Clipwell takes "
                             + "them over everywhere and those uses stop working.")
                    } icon: {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                    }
                    .font(.caption)
                    .fixedSize(horizontal: false, vertical: true)
                }
            } header: {
                Text("⌘+Arrow Mode")
            }

            Section {
                Toggle("Cycling wraps around at the outermost display",
                       isOn: $preferences.wrapDisplays)
                HStack {
                    Spacer()
                    Button("Reset Shortcuts to Defaults") {
                        preferences.resetHotKeysToDefaults()
                    }
                }
            } header: {
                Text("Displays")
            }
        }
        .formStyle(.grouped)
    }

    private func recorder(_ label: String, _ binding: Binding<KeyCombo>) -> some View {
        HStack {
            Text(label)
            Spacer()
            HotKeyRecorder(combo: binding)
                .frame(width: 130, height: 24)
        }
    }

    // MARK: - Clipboard

    private var clipboardTab: some View {
        Form {
            Section {
                Picker("Keep items for", selection: $preferences.retention) {
                    ForEach(Retention.allCases) { option in
                        Text(option.title).tag(option)
                    }
                }
                Stepper(value: $preferences.historyLimit, in: 10...2000, step: 10) {
                    HStack {
                        Text("Maximum items")
                        Spacer()
                        Text("\(preferences.historyLimit)").foregroundStyle(.secondary)
                    }
                }
            } header: {
                Text("History")
            } footer: {
                Text("Pinned items are never evicted and never expire.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                Label {
                    Text("Screenshots taken with **⌃⌘⇧4** (with Control) go to the clipboard "
                         + "and are captured already — no setting needed.\n\nPlain ⌘⇧4 saves a "
                         + "file instead and never touches the clipboard. To capture those "
                         + "without granting any extra permission, swap the two shortcuts in "
                         + "System Settings ▸ Keyboard ▸ Keyboard Shortcuts ▸ Screenshots.")
                } icon: {
                    Image(systemName: "info.circle").foregroundStyle(Color.accentColor)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)

                Button("Open Keyboard Shortcuts…") {
                    NSWorkspace.shared.open(URL(string:
                        "x-apple.systempreferences:com.apple.preference.keyboard?Shortcuts")!)
                }

                Divider()

                Toggle("Or: watch the screenshots folder and import automatically",
                       isOn: $preferences.captureScreenshots)
                if preferences.captureScreenshots {
                    Toggle("Also copy each screenshot to the clipboard",
                           isOn: $preferences.screenshotsToClipboard)
                    HStack(alignment: .top) {
                        Text("Status")
                        Spacer()
                        Text(screenshotStatusText)
                            .foregroundStyle(.secondary)
                            .font(.system(size: 11))
                            .multilineTextAlignment(.trailing)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            } header: {
                Text("Screenshots")
            } footer: {
                if preferences.captureScreenshots {
                    Text("Watching the folder needs permission to read "
                         + ScreenshotWatcher.screenshotDirectory().lastPathComponent
                         + " — macOS will ask once. If you never see the prompt, look for it "
                         + "behind other windows or on another display; it can appear without "
                         + "bringing itself to the front.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                HStack {
                    Text("Currently stored")
                    Spacer()
                    Text("\(store.items.count) items · \(store.items.filter(\.isPinned).count) pinned")
                        .foregroundStyle(.secondary)
                }
                HStack {
                    Spacer()
                    Button("Clear History (Keeps Pinned)", role: .destructive) {
                        store.clearUnpinned()
                    }
                    .disabled(!store.items.contains { !$0.isPinned })
                }
            }

            Section {
                HStack {
                    Text("Paste delay")
                    Spacer()
                    Text("\(Int(preferences.pasteDelayMs)) ms").foregroundStyle(.secondary)
                }
                Slider(value: $preferences.pasteDelayMs, in: 40...400, step: 10)
            } header: {
                Text("Advanced")
            } footer: {
                Text("How long Clipwell waits after switching back to the target app before "
                     + "pressing ⌘V. Increase it if pasting occasionally lands in the wrong "
                     + "place.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
    }

    // MARK: - General

    private var generalTab: some View {
        Form {
            Section {
                Toggle("Launch Clipwell at login", isOn: $preferences.launchAtLogin)
                if LaunchAtLogin.requiresApproval {
                    HStack {
                        Text("macOS is waiting for your approval in Login Items.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Open") { LaunchAtLogin.openLoginItemsSettings() }
                    }
                }
            } header: {
                Text("Startup")
            } footer: {
                Text("Enable this after running `make install`, so the login item points at the "
                     + "copy in /Applications rather than a build directory.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                HStack {
                    Image(systemName: accessibilityGranted
                          ? "checkmark.circle.fill" : "exclamationmark.circle.fill")
                        .foregroundStyle(accessibilityGranted ? Color.green : Color.orange)
                    Text(accessibilityGranted
                         ? "Accessibility access granted"
                         : "Accessibility access is required")
                    Spacer()
                    Button("Open Settings") { AccessibilityPermission.openSystemSettings() }
                }
            } header: {
                Text("Permissions")
            } footer: {
                Text("Needed to move windows and to press ⌘V for you. If you rebuild Clipwell, "
                     + "macOS may ask for it again — remove the old entry in the list and "
                     + "re-add the new app.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section {
                HStack {
                    Text("Version")
                    Spacer()
                    Text(Bundle.main.shortVersion).foregroundStyle(.secondary)
                }
                HStack {
                    Text("History file")
                    Spacer()
                    Button("Reveal in Finder") {
                        NSWorkspace.shared.activateFileViewerSelecting([Storage.historyFile])
                    }
                }
                HStack {
                    Spacer()
                    Button("Quit Clipwell") { NSApp.terminate(nil) }
                }
            } header: {
                Text("About")
            }
        }
        .formStyle(.grouped)
    }
}

extension Bundle {
    var shortVersion: String {
        let short = infoDictionary?["CFBundleShortVersionString"] as? String ?? "?"
        let build = infoDictionary?["CFBundleVersion"] as? String ?? "?"
        return "\(short) (\(build))"
    }
}
