import AppKit
import SwiftUI

/// The Power tab: lid close, display-off ("screen lock") and sleep timers per power source.
struct PowerSettingsView: View {

    @StateObject private var model = PowerModel()

    /// Minutes; 0 = never. Hours are offered because battery sleep is usually set in hours.
    private static let presets = [0, 1, 2, 3, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 240, 360, 480]

    var body: some View {
        Form {
            if let draft = model.draft {
                lidSection(draft)
                timersSection(draft)
                lockSection(draft)
            } else {
                Section {
                    HStack {
                        ProgressView().controlSize(.small)
                        Text("Reading current power settings…").foregroundStyle(.secondary)
                    }
                }
            }
            actionsSection
        }
        .formStyle(.grouped)
        .onAppear { if model.current == nil { model.refresh() } }
    }

    // MARK: - Sections

    private func lidSection(_ draft: PowerSnapshot) -> some View {
        Section {
            Picker("When the lid is closed", selection: binding(\.lidStaysAwake)) {
                Text("Sleep").tag(false)
                Text("Stay awake").tag(true)
            }
            .pickerStyle(.segmented)
            if draft.lidStaysAwake {
                Label {
                    Text("Stay awake sets `pmset disablesleep 1`: the Mac never sleeps on its own — "
                         + "not on lid close and not from the idle timers below — until you switch "
                         + "back to Sleep. Watch the battery and heat in a bag.")
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                .font(.caption)
                .fixedSize(horizontal: false, vertical: true)
            }
        } header: {
            Text("Lid Close")
        } footer: {
            Text("This is system-wide on macOS: the same choice applies on battery and on the charger.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func timersSection(_ draft: PowerSnapshot) -> some View {
        Section {
            if draft.hasBattery {
                minutesPicker("Turn display off — on battery", \.batteryDisplayMin)
                minutesPicker("Turn display off — on charger", \.chargerDisplayMin)
                minutesPicker("Sleep — on battery", \.batterySleepMin)
                minutesPicker("Sleep — on charger", \.chargerSleepMin)
            } else {
                minutesPicker("Turn display off after", \.chargerDisplayMin)
                minutesPicker("Sleep after", \.chargerSleepMin)
            }
        } header: {
            Text("Idle Timers")
        } footer: {
            Text(timersFooter(draft))
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func lockSection(_ draft: PowerSnapshot) -> some View {
        Section {
            HStack {
                Text("Require password after display turns off")
                Spacer()
                Text(draft.screenLock).foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                Button("Open Lock Screen Settings…") {
                    NSWorkspace.shared.open(URL(string:
                        "x-apple.systempreferences:com.apple.Lock-Screen-Settings.extension")!)
                }
            }
        } header: {
            Text("Screen Lock")
        } footer: {
            Text("The screen locks when the display turns off (timer above) if a password is required. "
                 + "Set “Require password” to Immediately in Lock Screen settings — macOS only lets "
                 + "you change it there.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private var actionsSection: some View {
        Section {
            HStack {
                Button("Refresh") { model.refresh() }
                Button("Revert") { model.revert() }
                    .disabled(!model.hasChanges)
                Spacer()
                if model.busy { ProgressView().controlSize(.small) }
                Button("Apply…") { model.apply() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!model.hasChanges || model.busy)
            }
            if let message = model.message {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(model.messageIsError ? Color.red : Color.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        } footer: {
            Text("Apply asks once for your administrator password (the same as `sudo pmset`).")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Helpers

    private func minutesPicker(_ label: String, _ keyPath: WritableKeyPath<PowerSnapshot, Int>) -> some View {
        let value = model.draft?[keyPath: keyPath] ?? 0
        var options = Self.presets
        if !options.contains(value) { options.append(value); options.sort() }
        return Picker(label, selection: binding(keyPath)) {
            ForEach(options, id: \.self) { minutes in
                Text(Self.describe(minutes)).tag(minutes)
            }
        }
    }

    private func binding<Value>(_ keyPath: WritableKeyPath<PowerSnapshot, Value>) -> Binding<Value> {
        Binding(
            get: { model.draft![keyPath: keyPath] },
            set: { model.draft?[keyPath: keyPath] = $0 })
    }

    private func timersFooter(_ draft: PowerSnapshot) -> String {
        var text = "Never keeps the display or Mac on while idle."
        let pairs = draft.hasBattery
            ? [(draft.batteryDisplayMin, draft.batterySleepMin), (draft.chargerDisplayMin, draft.chargerSleepMin)]
            : [(draft.chargerDisplayMin, draft.chargerSleepMin)]
        if pairs.contains(where: { $0.1 != 0 && ($0.0 == 0 || $0.0 > $0.1) }) {
            text += " The display timer should be shorter than the sleep timer."
        }
        if draft.lidStaysAwake {
            text += " Sleep timers have no effect while the lid is set to Stay awake."
        }
        return text
    }

    static func describe(_ minutes: Int) -> String {
        if minutes == 0 { return "Never" }
        if minutes < 60 { return "\(minutes) min" }
        let hours = minutes / 60, rest = minutes % 60
        let hourText = hours == 1 ? "1 hour" : "\(hours) hours"
        return rest == 0 ? hourText : "\(hourText) \(rest) min"
    }
}
