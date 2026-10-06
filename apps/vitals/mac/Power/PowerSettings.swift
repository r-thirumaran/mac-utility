import Foundation
import IOKit

/// The `power.get` / `power.apply` model on top of `pmset`.
///
/// Reading is root-free: `pmset -g custom` lists every setting per power source
/// (`Battery Power:` / `AC Power:` sections of ` key value` lines), `pmset -g` adds the
/// system-wide `SleepDisabled` flag and `pmset -g cap` says which settings this Mac honours
/// (`custom` prints values even for settings the hardware ignores). The platform-neutral keys
/// map to:
///
/// | key | pmset |
/// |---|---|
/// | `onBattery.displaySleepMin` / `onPower.displaySleepMin` | `-b displaysleep` / `-c displaysleep` |
/// | `onBattery.systemSleepMin` / `onPower.systemSleepMin` | `-b sleep` / `-c sleep` |
/// | `*.lidClose` (`"sleep"` or `"nothing"`) | `-a disablesleep 0 / 1` — system-wide, so both sources read the same |
/// | `lidOpenWakes` | `-a lidwake` |
/// | `wakeOnNetwork` | `-a womp` |
/// | `powerNap` | `-a powernap` |
/// | `lowPowerMode` | `-a lowpowermode` (`powermode` on Macs with High Power Mode) |
///
/// Single-value keys are read from the AC Power section (Battery Power on a Mac that only has
/// that one) and written with `-a`, so the toggle does not flip when the charger is unplugged.
/// Writes need root: `apply` batches every change into ONE `/bin/sh` script run under a single
/// administrator prompt, and each pmset call reports its own failure through a `FAIL <key>: …`
/// line so the result can say exactly which keys were applied. Hibernation and the power
/// button have no pmset equivalent and are reported unsupported.
///
/// Not thread-safe: call it from one queue (the bridge's power queue).
final class PowerSettings {

    struct Source: Codable {
        var displaySleepMin: Int
        var systemSleepMin: Int
        var lidClose: String
    }

    struct Support: Codable {
        var battery: Bool
        var lid: Bool
        var lidClose: [String]
        var lidOpenWakes: Bool
        var wakeOnNetwork: Bool
        var powerNap: Bool
        var lowPowerMode: Bool
        var hibernate: Bool
        var powerButton: Bool
        var displaySleep: Bool
        var systemSleep: Bool
        var keepAwake: Bool
        var actions: [String]
        var scheduleTypes: [String]
    }

    struct Model: Codable {
        var onBattery: Source
        var onPower: Source
        var lidOpenWakes: Bool
        var wakeOnNetwork: Bool
        var powerNap: Bool?
        var lowPowerMode: Bool?
        var supported: Support
        var elevated: Bool
    }

    struct Failure: Codable {
        var key: String
        var reason: String
    }

    struct ApplyResult: Codable {
        var applied: [String]
        var failed: [Failure]
    }

    private struct Command {
        var keys: [String]
        var tag: String
        var arguments: String
    }

    static let actions = ["sleep", "displayOff", "lock", "restart", "shutdown"]
    static let scheduleTypes = ["sleep", "shutdown", "restart", "wake", "wakeorpoweron"]
    static let lidChoices = ["sleep", "nothing"]

    let hasBattery: Bool
    let hasLid: Bool

    init() {
        let battery = BatterySampler().sample().present
        hasBattery = battery
        hasLid = PowerSettings.detectLid() || battery
    }

    // MARK: - Reading

    func read() throws -> Model {
        let custom = try PowerSettings.pmset(["-g", "custom"])
        let live = (try? PowerSettings.pmset(["-g"])) ?? ""
        let capabilities = (try? PowerSettings.pmset(["-g", "cap"])) ?? ""
        return PowerSettings.model(custom: custom, live: live, capabilities: capabilities,
                                   hasBattery: hasBattery, hasLid: hasLid)
    }

    static func model(custom: String, live: String, capabilities: String, hasBattery: Bool, hasLid: Bool) -> Model {
        let sections = parseSections(custom)
        let battery = sections["Battery Power"] ?? [:]
        let ac = sections["AC Power"] ?? [:]
        let primary = ac.isEmpty ? battery : ac
        let system = parseSections(live)["System-wide power settings"] ?? [:]
        let caps = parseCapabilities(capabilities)
        func supports(_ setting: String) -> Bool {
            caps.isEmpty ? primary[setting] != nil : caps.contains(setting)
        }

        let lidClose = (int(system["SleepDisabled"]) ?? 0) != 0 ? "nothing" : "sleep"
        let lowPowerSetting = lowPowerSetting(in: primary)
        let supportsLowPower = supports("lowpowermode") || supports("powermode")
        let supportsPowerNap = supports("powernap")

        let support = Support(battery: hasBattery,
                              lid: hasLid,
                              lidClose: hasLid ? lidChoices : [],
                              lidOpenWakes: hasLid && supports("lidwake"),
                              wakeOnNetwork: supports("womp"),
                              powerNap: supportsPowerNap,
                              lowPowerMode: supportsLowPower,
                              hibernate: false,
                              powerButton: false,
                              displaySleep: true,
                              systemSleep: true,
                              keepAwake: true,
                              actions: actions,
                              scheduleTypes: scheduleTypes)

        return Model(onBattery: Source(displaySleepMin: int(battery["displaysleep"]) ?? 0,
                                       systemSleepMin: int(battery["sleep"]) ?? 0,
                                       lidClose: lidClose),
                     onPower: Source(displaySleepMin: int(ac["displaysleep"]) ?? 0,
                                     systemSleepMin: int(ac["sleep"]) ?? 0,
                                     lidClose: lidClose),
                     lidOpenWakes: (int(primary["lidwake"]) ?? 0) != 0,
                     wakeOnNetwork: (int(primary["womp"]) ?? 0) != 0,
                     powerNap: supportsPowerNap ? (int(primary["powernap"]) ?? 0) != 0 : nil,
                     lowPowerMode: supportsLowPower ? int(primary[lowPowerSetting]) == 1 : nil,
                     supported: support,
                     elevated: false)
    }

    // MARK: - Applying

    /// Validates the nested `changes` object, writes everything writable in one admin prompt
    /// and reports per key. Throws `not_authorized` when the prompt is cancelled.
    func apply(_ changes: [String: Any]) throws -> ApplyResult {
        let flat = PowerSettings.flatten(changes)
        var result = ApplyResult(applied: [], failed: [])
        var commands: [Command] = []
        var lidKeys: [String] = []
        var lidWanted = Set<String>()

        let sections = PowerSettings.parseSections((try? PowerSettings.pmset(["-g", "custom"])) ?? "")
        let primary = sections["AC Power"] ?? sections["Battery Power"] ?? [:]
        let lowPowerSetting = PowerSettings.lowPowerSetting(in: primary)

        for key in flat.keys.sorted() {
            guard let value = flat[key] else { continue }
            switch key {
            case "onBattery.displaySleepMin", "onBattery.systemSleepMin",
                 "onPower.displaySleepMin", "onPower.systemSleepMin":
                guard let minutes = PowerSettings.minutes(value) else {
                    result.failed.append(Failure(key: key, reason: "\(key) must be a non-negative number of minutes"))
                    continue
                }
                let scope = key.hasPrefix("onBattery.") ? "-b" : "-c"
                let setting = key.hasSuffix("displaySleepMin") ? "displaysleep" : "sleep"
                commands.append(Command(keys: [key], tag: key, arguments: "\(scope) \(setting) \(minutes)"))

            case "onBattery.lidClose", "onPower.lidClose":
                guard let choice = value as? String, PowerSettings.lidChoices.contains(choice) else {
                    result.failed.append(Failure(key: key, reason: "\(key) must be sleep or nothing on macOS"))
                    continue
                }
                lidKeys.append(key)
                lidWanted.insert(choice)

            case "lidOpenWakes", "wakeOnNetwork", "powerNap", "lowPowerMode":
                guard let flag = PowerSettings.flag(value) else {
                    result.failed.append(Failure(key: key, reason: "\(key) must be a boolean"))
                    continue
                }
                let setting: String
                switch key {
                case "lidOpenWakes": setting = "lidwake"
                case "wakeOnNetwork": setting = "womp"
                case "powerNap": setting = "powernap"
                default: setting = lowPowerSetting
                }
                commands.append(Command(keys: [key], tag: key, arguments: "-a \(setting) \(flag ? 1 : 0)"))

            case "hibernate.enabled", "hibernate.mode":
                result.failed.append(Failure(key: key, reason: "hibernation (safe sleep) is managed by macOS and cannot be changed here"))

            case "powerButton":
                result.failed.append(Failure(key: key, reason: "the power button action cannot be changed on macOS"))

            default:
                result.failed.append(Failure(key: key, reason: "unknown setting \"\(key)\""))
            }
        }

        if !lidKeys.isEmpty {
            if lidWanted.count > 1 {
                for key in lidKeys {
                    result.failed.append(Failure(key: key, reason: "lid behaviour is system-wide on macOS; choose the same value for battery and power"))
                }
            } else {
                let disable = lidWanted.contains("nothing") ? 1 : 0
                commands.append(Command(keys: lidKeys, tag: "lid", arguments: "-a disablesleep \(disable)"))
            }
        }

        guard !commands.isEmpty else { return result }

        // One prompt for the whole batch; every pmset call reports its own failure.
        let script = commands.map { command in
            "o=$(/usr/bin/pmset \(command.arguments) 2>&1) || echo \"FAIL \(command.tag): $o\""
        }.joined(separator: " ; ")

        let output: String
        do {
            output = try AdminShell.run(script: script, prompt: "Vitals wants to change power settings.")
        } catch AdminShellError.cancelled {
            throw BridgeError.notAuthorized("Administrator authorisation was cancelled; nothing was changed.")
        } catch AdminShellError.failed(let status, let stderr) {
            let detail = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            throw BridgeError.internalError("pmset failed (status \(status)): \(detail)")
        }

        let failures = PowerSettings.parseFailures(output)
        for command in commands {
            if let reason = failures[command.tag] {
                for key in command.keys {
                    result.failed.append(Failure(key: key, reason: reason))
                }
            } else {
                result.applied.append(contentsOf: command.keys)
            }
        }
        return result
    }

    // MARK: - pmset output

    /// `pmset -g custom` / `pmset -g`: `Header:` lines open a section, ` key value` lines fill
    /// it. A trailing ` (sleep prevented by …)` suffix is stripped from values.
    static func parseSections(_ text: String) -> [String: [String: String]] {
        var sections: [String: [String: String]] = [:]
        var current = ""
        for raw in text.components(separatedBy: "\n") {
            if !raw.hasPrefix(" "), raw.hasSuffix(":") {
                current = String(raw.dropLast())
                continue
            }
            guard raw.hasPrefix(" ") else { continue }
            let trimmed = raw.trimmingCharacters(in: .whitespaces)
            let parts = trimmed.split(maxSplits: 1, whereSeparator: { $0 == " " || $0 == "\t" })
            guard parts.count == 2 else { continue }
            var value = String(parts[1]).trimmingCharacters(in: .whitespaces)
            if let suffix = value.range(of: " (") {
                value = String(value[..<suffix.lowerBound])
            }
            sections[current, default: [:]][String(parts[0])] = value
        }
        return sections
    }

    /// `pmset -g cap`: `Capabilities for AC Power:` then one ` setting` per line. The union
    /// over every source is returned.
    static func parseCapabilities(_ text: String) -> Set<String> {
        var names = Set<String>()
        for raw in text.components(separatedBy: "\n") where raw.hasPrefix(" ") {
            let name = raw.trimmingCharacters(in: .whitespaces)
            if !name.isEmpty { names.insert(name) }
        }
        return names
    }

    /// `FAIL <tag>: <first line of pmset's message>` lines of the apply script.
    // Reviewer note: `do shell script` rewrites the output's line endings to CR by default
    // ("altering line endings"), so split on every newline character, not just LF.
    private static func parseFailures(_ output: String) -> [String: String] {
        var failures: [String: String] = [:]
        for line in output.components(separatedBy: CharacterSet.newlines) where line.hasPrefix("FAIL ") {
            let body = line.dropFirst(5)
            guard let separator = body.range(of: ": ") else {
                failures[String(body).trimmingCharacters(in: .whitespaces)] = "pmset failed"
                continue
            }
            let tag = String(body[..<separator.lowerBound])
            let reason = String(body[separator.upperBound...]).trimmingCharacters(in: .whitespaces)
            failures[tag] = reason.isEmpty ? "pmset failed" : reason
        }
        return failures
    }

    /// Macs with High Power Mode show `powermode 0|1|2` instead of `lowpowermode 0|1`.
    private static func lowPowerSetting(in section: [String: String]) -> String {
        if section["lowpowermode"] == nil, section["powermode"] != nil { return "powermode" }
        return "lowpowermode"
    }

    // MARK: - Values

    /// `{onBattery: {lidClose: "sleep"}}` → `["onBattery.lidClose": "sleep"]`; flat keys pass through.
    static func flatten(_ changes: [String: Any], prefix: String = "") -> [String: Any] {
        var out: [String: Any] = [:]
        for (key, value) in changes {
            let path = prefix.isEmpty ? key : prefix + "." + key
            if let nested = value as? [String: Any] {
                out.merge(flatten(nested, prefix: path)) { _, new in new }
            } else {
                out[path] = value
            }
        }
        return out
    }

    /// JSON numbers, integral doubles and numeric strings; nothing negative or absurd.
    static func minutes(_ value: Any) -> Int? {
        if let number = value as? NSNumber {
            let double = number.doubleValue
            guard double >= 0, double == double.rounded(), double <= 24 * 60 * 365 else { return nil }
            return Int(double)
        }
        if let text = value as? String, let number = Int(text.trimmingCharacters(in: .whitespaces)), number >= 0 {
            return number
        }
        return nil
    }

    static func flag(_ value: Any) -> Bool? {
        if let flag = value as? Bool { return flag }
        if let number = value as? NSNumber { return number.doubleValue != 0 }
        if let text = value as? String {
            switch text.lowercased() {
            case "true", "1", "on", "yes": return true
            case "false", "0", "off", "no": return false
            default: return nil
            }
        }
        return nil
    }

    private static func int(_ text: String?) -> Int? {
        guard let text else { return nil }
        return Int(text.trimmingCharacters(in: .whitespaces))
    }

    // MARK: - Helpers

    private static func pmset(_ arguments: [String]) throws -> String {
        let result = try Shell.run("/usr/bin/pmset", arguments)
        guard result.status == 0 else {
            let detail = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            throw BridgeError.internalError("pmset \(arguments.joined(separator: " ")) failed: \(detail)")
        }
        return result.stdout
    }

    /// `AppleClamshellState` exists on the power-management root domain only on Macs with a lid.
    private static func detectLid() -> Bool {
        let service = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceMatching("IOPMrootDomain"))
        guard service != 0 else { return false }
        defer { IOObjectRelease(service) }
        guard let property = IORegistryEntryCreateCFProperty(service, "AppleClamshellState" as CFString, kCFAllocatorDefault, 0) else {
            return false
        }
        _ = property.takeRetainedValue()
        return true
    }
}
