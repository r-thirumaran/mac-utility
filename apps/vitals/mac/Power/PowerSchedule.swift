import Foundation

/// Repeating power events (`power.schedule.get` / `.set`) on top of `pmset repeat`.
///
/// macOS keeps at most one repeating "on" event (wake / wakeorpoweron) and one "off" event
/// (sleep / shutdown / restart), and every `pmset repeat` call replaces both. `items()` parses
/// `pmset -g sched` (`Repeating power events:` lines such as `sleep at 11:00PM weekdays only`)
/// and, when pmset only says "Some days", recovers the exact day mask from
/// `com.apple.AutoWake.plist`. `set` writes the enabled items in one `pmset repeat` (or
/// `pmset repeat cancel`) under the administrator prompt. pmset has no notion of a disabled
/// event, so items the user switches off are parked in UserDefaults and merged back on read.
///
/// Not thread-safe: call it from one queue (the bridge's power queue).
final class PowerSchedule {

    struct Item: Codable, Equatable {
        var id: String
        /// sleep | shutdown | restart | wake | wakeorpoweron
        var type: String
        /// "mon" … "sun"; empty means every day.
        var days: [String]
        /// "HH:MM", 24-hour.
        var time: String
        var enabled: Bool
    }

    static let types = PowerSettings.scheduleTypes
    static let weekDays = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]

    private static let onTypes: Set<String> = ["wake", "poweron", "wakeorpoweron", "wakepoweron"]
    private static let dayLetters = ["mon": "M", "tue": "T", "wed": "W", "thu": "R", "fri": "F", "sat": "S", "sun": "U"]
    private static let dayNames = ["monday": "mon", "tuesday": "tue", "wednesday": "wed", "thursday": "thu",
                                   "friday": "fri", "saturday": "sat", "sunday": "sun"]
    private static let disabledKey = "vitals.schedule.disabled"
    private static let autoWakePlist = "/Library/Preferences/SystemConfiguration/com.apple.AutoWake.plist"

    // MARK: - Reading

    func items() throws -> [Item] {
        let result = try Shell.run("/usr/bin/pmset", ["-g", "sched"])
        guard result.status == 0 else {
            throw BridgeError.internalError("pmset -g sched failed: \(result.stderr.trimmingCharacters(in: .whitespacesAndNewlines))")
        }
        var items = PowerSchedule.parse(result.stdout)
        items.append(contentsOf: disabledItems())
        return items
    }

    /// The `Repeating power events:` block of `pmset -g sched`.
    static func parse(_ text: String) -> [Item] {
        var items: [Item] = []
        var inRepeating = false
        var masks: [String: Int]?
        for raw in text.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasSuffix(":") {
                inRepeating = line.lowercased().hasPrefix("repeating power events")
                continue
            }
            guard inRepeating, !line.isEmpty, let at = line.range(of: " at ") else { continue }

            let rawType = String(line[..<at.lowerBound]).lowercased()
            let rest = line[at.upperBound...].split(separator: " ", maxSplits: 1).map(String.init)
            guard let first = rest.first, let time = parseTime(first) else { continue }
            let phrase = rest.count > 1 ? rest[1].lowercased() : "every day"
            let type = rawType == "wakepoweron" ? "wakeorpoweron" : rawType

            var days = daysFromPhrase(phrase)
            if days == nil {
                if masks == nil { masks = readAutoWakeMasks() }
                let key = onTypes.contains(rawType) ? "RepeatingPowerOn" : "RepeatingPowerOff"
                days = masks?[key].map(daysFromMask) ?? []
            }
            let id = "\(type)-\(time.replacingOccurrences(of: ":", with: ""))-\(items.count + 1)"
            items.append(Item(id: id, type: type, days: days ?? [], time: time, enabled: true))
        }
        return items
    }

    // MARK: - Writing

    /// Validates like the Go backend, programs the enabled items and returns the fresh list.
    func set(_ requested: [Item]) throws -> [Item] {
        let items = try PowerSchedule.validate(requested)
        let enabled = items.filter { $0.enabled }
        let onEvents = enabled.filter { PowerSchedule.onTypes.contains($0.type) }
        let offEvents = enabled.filter { !PowerSchedule.onTypes.contains($0.type) }
        guard onEvents.count <= 1, offEvents.count <= 1 else {
            throw BridgeError.invalidParams("macOS keeps one repeating wake and one repeating sleep, shutdown or restart; disable or remove the extra items")
        }

        var arguments = ["/usr/bin/pmset", "repeat"]
        if enabled.isEmpty {
            arguments.append("cancel")
        } else {
            for item in onEvents + offEvents {
                arguments += [item.type, PowerSchedule.letters(item.days), item.time + ":00"]
            }
        }

        do {
            _ = try AdminShell.run(script: arguments.joined(separator: " "),
                                   prompt: "Vitals wants to change the scheduled power events.")
        } catch AdminShellError.cancelled {
            throw BridgeError.notAuthorized("Administrator authorisation was cancelled; the schedule was not changed.")
        } catch AdminShellError.failed(_, let stderr) {
            throw BridgeError.internalError("pmset repeat failed: \(stderr.trimmingCharacters(in: .whitespacesAndNewlines))")
        }

        storeDisabled(items.filter { !$0.enabled })
        return try self.items()
    }

    static func validate(_ items: [Item]) throws -> [Item] {
        var out: [Item] = []
        for (index, raw) in items.enumerated() {
            var item = raw
            item.type = item.type.trimmingCharacters(in: .whitespaces).lowercased()
            guard types.contains(item.type) else {
                throw BridgeError.invalidParams("item \(index + 1): unsupported type \"\(raw.type)\"")
            }
            guard isTime(item.time) else {
                throw BridgeError.invalidParams("item \(index + 1): time must be HH:MM")
            }
            var seen = Set<String>()
            for day in item.days {
                let short = String(day.trimmingCharacters(in: .whitespaces).lowercased().prefix(3))
                guard weekDays.contains(short) else {
                    throw BridgeError.invalidParams("item \(index + 1): unknown day \"\(day)\"")
                }
                seen.insert(short)
            }
            var days = weekDays.filter { seen.contains($0) }
            if days.count == 7 { days = [] }
            item.days = days
            if item.id.isEmpty {
                item.id = "\(item.type)-\(item.time.replacingOccurrences(of: ":", with: ""))-\(index + 1)"
            }
            out.append(item)
        }
        return out
    }

    // MARK: - Disabled items

    private func disabledItems() -> [Item] {
        guard let data = UserDefaults.standard.data(forKey: PowerSchedule.disabledKey),
              let items = try? JSONDecoder().decode([Item].self, from: data) else { return [] }
        return items
    }

    private func storeDisabled(_ items: [Item]) {
        guard !items.isEmpty else {
            UserDefaults.standard.removeObject(forKey: PowerSchedule.disabledKey)
            return
        }
        // Distinct ids from the pmset-derived ones so the UI's remove-by-id cannot collide.
        let parked = items.enumerated().map { index, item -> Item in
            var copy = item
            copy.id = "off-\(item.type)-\(item.time.replacingOccurrences(of: ":", with: ""))-\(index + 1)"
            copy.enabled = false
            return copy
        }
        if let data = try? JSONEncoder().encode(parked) {
            UserDefaults.standard.set(data, forKey: PowerSchedule.disabledKey)
        }
    }

    // MARK: - Formats

    /// pmset prints `7:00AM` / `11:59PM`; `HH:MM` is accepted too.
    static func parseTime(_ text: String) -> String? {
        let upper = text.uppercased()
        guard upper.hasSuffix("AM") || upper.hasSuffix("PM") else { return parse24Hour(text) }
        let afternoon = upper.hasSuffix("PM")
        let pieces = upper.dropLast(2).split(separator: ":")
        guard pieces.count == 2, var hour = Int(pieces[0]), let minute = Int(pieces[1]),
              (0...12).contains(hour), (0..<60).contains(minute) else { return nil }
        if hour == 12 { hour = 0 }
        if afternoon { hour += 12 }
        return String(format: "%02d:%02d", hour, minute)
    }

    private static func parse24Hour(_ text: String) -> String? {
        let pieces = text.split(separator: ":")
        guard pieces.count >= 2, let hour = Int(pieces[0]), let minute = Int(pieces[1]),
              (0..<24).contains(hour), (0..<60).contains(minute) else { return nil }
        return String(format: "%02d:%02d", hour, minute)
    }

    private static func isTime(_ text: String) -> Bool {
        let pieces = text.split(separator: ":", omittingEmptySubsequences: false)
        guard pieces.count == 2, pieces[0].count == 2, pieces[1].count == 2,
              let hour = Int(pieces[0]), let minute = Int(pieces[1]) else { return false }
        return (0..<24).contains(hour) && (0..<60).contains(minute)
    }

    /// `every day` → `[]`, `weekdays only`, `weekends only`, a single day name; `nil` for
    /// "Some days", which only the plist can resolve.
    private static func daysFromPhrase(_ phrase: String) -> [String]? {
        switch phrase {
        case "every day": return []
        case "weekdays only": return ["mon", "tue", "wed", "thu", "fri"]
        case "weekends only": return ["sat", "sun"]
        default:
            if let day = dayNames[phrase] { return [day] }
            return nil
        }
    }

    /// M=1 T=2 W=4 R=8 F=16 S=32 U=64.
    private static func daysFromMask(_ mask: Int) -> [String] {
        var days: [String] = []
        for (index, day) in weekDays.enumerated() where mask & (1 << index) != 0 {
            days.append(day)
        }
        return days.count == 7 ? [] : days
    }

    /// `["mon", "wed"]` → `"MW"`; every day → `"MTWRFSU"`.
    private static func letters(_ days: [String]) -> String {
        let chosen = days.isEmpty ? weekDays : days
        return chosen.compactMap { dayLetters[$0] }.joined()
    }

    /// `RepeatingPowerEvents.RepeatingPowerOn/Off.weekdays` from the AutoWake plist (world-readable).
    private static func readAutoWakeMasks() -> [String: Int] {
        guard let root = NSDictionary(contentsOfFile: autoWakePlist) as? [String: Any],
              let repeating = root["RepeatingPowerEvents"] as? [String: Any] else { return [:] }
        var masks: [String: Int] = [:]
        for key in ["RepeatingPowerOn", "RepeatingPowerOff"] {
            if let event = repeating[key] as? [String: Any], let mask = event["weekdays"] as? Int {
                masks[key] = mask
            }
        }
        return masks
    }
}
