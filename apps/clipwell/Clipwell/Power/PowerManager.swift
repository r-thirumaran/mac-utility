import Foundation

/// Lid-close, display-off and sleep timers on top of `pmset`, for the Power settings tab.
///
/// Reading needs no privileges: `pmset -g custom` lists `Battery Power:` / `AC Power:`
/// sections of ` key value` lines, `pmset -g` adds the system-wide `SleepDisabled` flag, and
/// `sysadminctl -screenLock status` says how soon a password is required after the display
/// turns off. Writing needs root, so every change is batched into ONE `/bin/sh` script run
/// through the macOS administrator dialog (the GUI form of `sudo pmset …`); each pmset call
/// reports its own failure as a `FAIL <tag>: …` line.
///
/// | UI | pmset |
/// |---|---|
/// | Lid closed → stay awake / sleep | `-a disablesleep 1` / `0` (system-wide) |
/// | Turn display off after (battery / charger) | `-b displaysleep N` / `-c displaysleep N` |
/// | Sleep after (battery / charger) | `-b sleep N` / `-c sleep N` |
///
/// Minutes everywhere; 0 means never.
struct PowerSnapshot: Equatable {
    var hasBattery: Bool
    var batteryDisplayMin: Int
    var batterySleepMin: Int
    var chargerDisplayMin: Int
    var chargerSleepMin: Int
    /// `SleepDisabled 1`: closing the lid (and idle timers) never sleep the Mac.
    var lidStaysAwake: Bool
    /// "off", "immediate", "5 seconds", … from `sysadminctl -screenLock status`.
    var screenLock: String
}

struct PowerApplyResult {
    var applied: [String]
    var failed: [(tag: String, reason: String)]
}

enum PowerError: LocalizedError {
    case cancelled
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .cancelled: return "Administrator authorisation was cancelled; nothing was changed."
        case .failed(let detail): return detail
        }
    }
}

enum PowerControl {

    // MARK: - Reading

    static func read() throws -> PowerSnapshot {
        let custom = try run("/usr/bin/pmset", ["-g", "custom"])
        guard custom.status == 0 else {
            throw PowerError.failed("pmset -g custom failed: \(custom.stderr)")
        }
        let live = (try? run("/usr/bin/pmset", ["-g"]))?.stdout ?? ""
        let lock = (try? run("/usr/sbin/sysadminctl", ["-screenLock", "status"]))
        return snapshot(custom: custom.stdout, live: live,
                        screenLockOutput: (lock?.stderr ?? "") + (lock?.stdout ?? ""))
    }

    static func snapshot(custom: String, live: String, screenLockOutput: String) -> PowerSnapshot {
        let sections = parseSections(custom)
        let battery = sections["Battery Power"]
        let ac = sections["AC Power"] ?? battery ?? [:]
        let system = parseSections(live)["System-wide power settings"] ?? [:]
        return PowerSnapshot(
            hasBattery: battery != nil,
            batteryDisplayMin: int(battery?["displaysleep"]) ?? 0,
            batterySleepMin: int(battery?["sleep"]) ?? 0,
            chargerDisplayMin: int(ac["displaysleep"]) ?? 0,
            chargerSleepMin: int(ac["sleep"]) ?? 0,
            lidStaysAwake: (int(system["SleepDisabled"]) ?? 0) != 0,
            screenLock: parseScreenLock(screenLockOutput))
    }

    /// `Header:` lines open a section, ` key value` lines fill it; a trailing
    /// ` (sleep prevented by …)` is dropped from values.
    static func parseSections(_ text: String) -> [String: [String: String]] {
        var sections: [String: [String: String]] = [:]
        var current = ""
        for raw in text.components(separatedBy: CharacterSet.newlines) {
            if !raw.hasPrefix(" "), raw.hasSuffix(":") {
                current = String(raw.dropLast())
                continue
            }
            guard raw.hasPrefix(" ") else { continue }
            let parts = raw.trimmingCharacters(in: .whitespaces)
                .split(maxSplits: 1, whereSeparator: { $0 == " " || $0 == "\t" })
            guard parts.count == 2 else { continue }
            var value = parts[1].trimmingCharacters(in: .whitespaces)
            if let suffix = value.range(of: " (") { value = String(value[..<suffix.lowerBound]) }
            sections[current, default: [:]][String(parts[0])] = value
        }
        return sections
    }

    /// `… sysadminctl[…] screenLock is off` → `off`; `… is 300 seconds` → `300 seconds`.
    static func parseScreenLock(_ output: String) -> String {
        guard let range = output.range(of: "screenLock is ") else { return "unknown" }
        let rest = output[range.upperBound...]
        return rest.prefix { !$0.isNewline }.trimmingCharacters(in: .whitespaces)
    }

    // MARK: - Applying

    /// Writes only what differs between `current` and `wanted`, in one admin prompt.
    static func apply(from current: PowerSnapshot, to wanted: PowerSnapshot) throws -> PowerApplyResult {
        var commands: [(tag: String, arguments: String)] = []
        if wanted.lidStaysAwake != current.lidStaysAwake {
            commands.append(("Lid close", "-a disablesleep \(wanted.lidStaysAwake ? 1 : 0)"))
        }
        if current.hasBattery {
            if wanted.batteryDisplayMin != current.batteryDisplayMin {
                commands.append(("Battery display off", "-b displaysleep \(wanted.batteryDisplayMin)"))
            }
            if wanted.batterySleepMin != current.batterySleepMin {
                commands.append(("Battery sleep", "-b sleep \(wanted.batterySleepMin)"))
            }
        }
        let charger = current.hasBattery ? "-c" : "-a"
        if wanted.chargerDisplayMin != current.chargerDisplayMin {
            commands.append(("Charger display off", "\(charger) displaysleep \(wanted.chargerDisplayMin)"))
        }
        if wanted.chargerSleepMin != current.chargerSleepMin {
            commands.append(("Charger sleep", "\(charger) sleep \(wanted.chargerSleepMin)"))
        }
        guard !commands.isEmpty else { return PowerApplyResult(applied: [], failed: []) }

        // Tags contain spaces, so they are single-quoted for the shell; no tag contains a quote.
        let script = commands.map { command in
            "o=$(/usr/bin/pmset \(command.arguments) 2>&1) || echo 'FAIL \(command.tag)': \"$o\""
        }.joined(separator: " ; ")
        let output = try runAsAdmin(script: script,
                                    prompt: "Clipwell wants to change lid-close and sleep settings.")

        var failures: [String: String] = [:]
        for line in output.components(separatedBy: CharacterSet.newlines) where line.hasPrefix("FAIL ") {
            let body = line.dropFirst(5)
            if let colon = body.range(of: ": ") {
                failures[String(body[..<colon.lowerBound])] =
                    String(body[colon.upperBound...]).trimmingCharacters(in: .whitespaces)
            } else {
                failures[String(body).trimmingCharacters(in: CharacterSet(charactersIn: ": "))] = "pmset failed"
            }
        }
        var result = PowerApplyResult(applied: [], failed: [])
        for command in commands {
            if let reason = failures[command.tag] {
                result.failed.append((command.tag, reason.isEmpty ? "pmset failed" : reason))
            } else {
                result.applied.append(command.tag)
            }
        }
        return result
    }

    // MARK: - Process helpers

    struct Output {
        var status: Int32
        var stdout: String
        var stderr: String
    }

    static func run(_ executable: String, _ arguments: [String]) throws -> Output {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        try process.run()
        // Read before waiting so a full pipe cannot stall the child.
        let stdout = out.fileHandleForReading.readDataToEndOfFile()
        let stderr = err.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        return Output(status: process.terminationStatus,
                      stdout: String(decoding: stdout, as: UTF8.self),
                      stderr: String(decoding: stderr, as: UTF8.self))
    }

    /// `osascript -e 'do shell script "…" with administrator privileges'` — the system
    /// password dialog, running `script` with `/bin/sh` as root.
    static func runAsAdmin(script: String, prompt: String) throws -> String {
        let source = "do shell script \"" + escape(script) + "\" with administrator privileges"
            + " with prompt \"" + escape(prompt) + "\""
        let result = try run("/usr/bin/osascript", ["-e", source])
        guard result.status == 0 else {
            if result.stderr.contains("User canceled") || result.stderr.contains("(-128)") {
                throw PowerError.cancelled
            }
            throw PowerError.failed("pmset failed (status \(result.status)): "
                                    + result.stderr.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return result.stdout
    }

    /// Escapes text for an AppleScript double-quoted string literal.
    static func escape(_ text: String) -> String {
        text.replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
    }

    private static func int(_ text: String?) -> Int? {
        guard let text else { return nil }
        return Int(text.trimmingCharacters(in: .whitespaces))
    }
}

/// Holds the last-read settings and the user's edits for the Power tab.
@MainActor
final class PowerModel: ObservableObject {

    @Published private(set) var current: PowerSnapshot?
    @Published var draft: PowerSnapshot?
    @Published private(set) var busy = false
    @Published private(set) var message: String?
    @Published private(set) var messageIsError = false

    var hasChanges: Bool {
        guard let current, let draft else { return false }
        return current != draft
    }

    func refresh() {
        busy = true
        Task.detached {
            let result = Result { try PowerControl.read() }
            await MainActor.run {
                self.busy = false
                switch result {
                case .success(let snapshot):
                    self.current = snapshot
                    self.draft = snapshot
                case .failure(let error):
                    self.show(error.localizedDescription, error: true)
                }
            }
        }
    }

    func revert() {
        draft = current
        message = nil
    }

    func apply() {
        guard let current, let draft, current != draft else { return }
        busy = true
        message = nil
        Task.detached {
            let result = Result { try PowerControl.apply(from: current, to: draft) }
            let reread = try? PowerControl.read()
            await MainActor.run {
                self.busy = false
                if let reread {
                    self.current = reread
                    self.draft = reread
                }
                switch result {
                case .success(let outcome):
                    if outcome.failed.isEmpty {
                        self.show(outcome.applied.isEmpty ? "Nothing to change."
                                  : "Applied: " + outcome.applied.joined(separator: ", "), error: false)
                    } else {
                        let failed = outcome.failed.map { "\($0.tag) — \($0.reason)" }
                        self.show("Failed: " + failed.joined(separator: "; "), error: true)
                    }
                case .failure(let error):
                    self.show(error.localizedDescription, error: true)
                }
            }
        }
    }

    private func show(_ text: String, error: Bool) {
        message = text
        messageIsError = error
    }
}
