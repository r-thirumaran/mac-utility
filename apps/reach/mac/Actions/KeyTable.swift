import CoreGraphics
import Foundation

/// DOM `code` names → macOS virtual key codes, from the canonical table the build script
/// bundles as `Resources/hid-keymap.json` (`shared/keymap/hid-keymap.json`, the `mac`
/// column). Resolution follows the Go agent's `keys.go`: exact DOM code, then the short
/// aliases (`ctrl`, `esc`, `up`…), then a single printable character on the US layout, then a
/// case-insensitive DOM match. Media and audio names never reach the table — `MediaControl`
/// posts them as system-defined events, like the Go agent uses virtual keys for them.
final class KeyTable {

    static let shared = KeyTable(resource: Bundle.main.resourceURL?.appendingPathComponent("hid-keymap.json"))

    /// Short names accepted next to the DOM codes.
    static let aliases: [String: String] = [
        "ctrl": "ControlLeft", "control": "ControlLeft", "shift": "ShiftLeft", "alt": "AltLeft", "option": "AltLeft",
        "meta": "MetaLeft", "cmd": "MetaLeft", "command": "MetaLeft", "win": "MetaLeft", "super": "MetaLeft",
        "esc": "Escape", "return": "Enter", "del": "Delete", "ins": "Insert", "pgup": "PageUp", "pgdn": "PageDown",
        "pgdown": "PageDown", "up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
        " ": "Space", "spacebar": "Space", "back": "Backspace", "bksp": "Backspace", "menu": "ContextMenu",
        "capslock": "CapsLock",
    ]

    /// `mods` entries → the flag held on the key event. `cmdOrCtrl` is ⌘ on macOS
    /// (`docs/reach-api.md` §2).
    static let modifierFlags: [String: CGEventFlags] = [
        "ctrl": .maskControl, "shift": .maskShift, "alt": .maskAlternate, "meta": .maskCommand, "cmdOrCtrl": .maskCommand,
    ]

    /// Used when the bundled JSON cannot be read; generated from the same table.
    private static let builtIn: [String: Int] = [
        "AltLeft": 58, "AltRight": 61, "ArrowDown": 125, "ArrowLeft": 123, "ArrowRight": 124,
        "ArrowUp": 126, "AudioVolumeDown": 73, "AudioVolumeMute": 74, "AudioVolumeUp": 72,
        "Backquote": 50, "Backslash": 42, "Backspace": 51, "BracketLeft": 33, "BracketRight": 30,
        "CapsLock": 57, "Comma": 43, "ContextMenu": 110, "ControlLeft": 59, "ControlRight": 62,
        "Delete": 117, "Digit0": 29, "Digit1": 18, "Digit2": 19, "Digit3": 20, "Digit4": 21,
        "Digit5": 23, "Digit6": 22, "Digit7": 26, "Digit8": 28, "Digit9": 25, "End": 119,
        "Enter": 36, "Equal": 24, "Escape": 53, "F1": 122, "F10": 109, "F11": 103, "F12": 111,
        "F13": 105, "F14": 107, "F15": 113, "F16": 106, "F17": 64, "F18": 79, "F19": 80, "F2": 120,
        "F20": 90, "F3": 99, "F4": 118, "F5": 96, "F6": 97, "F7": 98, "F8": 100, "F9": 101,
        "Home": 115, "Insert": 114, "IntlBackslash": 10, "IntlRo": 94, "IntlYen": 93, "KeyA": 0,
        "KeyB": 11, "KeyC": 8, "KeyD": 2, "KeyE": 14, "KeyF": 3, "KeyG": 5, "KeyH": 4, "KeyI": 34,
        "KeyJ": 38, "KeyK": 40, "KeyL": 37, "KeyM": 46, "KeyN": 45, "KeyO": 31, "KeyP": 35,
        "KeyQ": 12, "KeyR": 15, "KeyS": 1, "KeyT": 17, "KeyU": 32, "KeyV": 9, "KeyW": 13,
        "KeyX": 7, "KeyY": 16, "KeyZ": 6, "Lang1": 104, "Lang2": 102, "MetaLeft": 55,
        "MetaRight": 54, "Minus": 27, "NumLock": 71, "Numpad0": 82, "Numpad1": 83, "Numpad2": 84,
        "Numpad3": 85, "Numpad4": 86, "Numpad5": 87, "Numpad6": 88, "Numpad7": 89, "Numpad8": 91,
        "Numpad9": 92, "NumpadAdd": 69, "NumpadComma": 95, "NumpadDecimal": 65, "NumpadDivide": 75,
        "NumpadEnter": 76, "NumpadEqual": 81, "NumpadMultiply": 67, "NumpadSubtract": 78,
        "PageDown": 121, "PageUp": 116, "Period": 47, "Quote": 39, "Semicolon": 41,
        "ShiftLeft": 56, "ShiftRight": 60, "Slash": 44, "Space": 49, "Tab": 48,
    ]

    /// Canonical DOM code → virtual key code.
    private let byDOM: [String: Int]
    /// Lowercased DOM code → canonical spelling.
    private let byLowercaseDOM: [String: String]
    /// `true` when the bundled JSON was used.
    let loadedFromBundle: Bool

    init(resource: URL?) {
        var table: [String: Int] = [:]
        if let resource, let loaded = KeyTable.load(resource) {
            table = loaded
        }
        if table.isEmpty {
            table = KeyTable.builtIn
            loadedFromBundle = false
            Log.error("keymap: hid-keymap.json unavailable; using the built-in table", category: "keys")
        } else {
            loadedFromBundle = true
        }
        byDOM = table
        var lower: [String: String] = [:]
        for name in table.keys {
            lower[name.lowercased()] = name
        }
        byLowercaseDOM = lower
    }

    /// Reads `{"keys": [{"dom": "KeyA", "mac": 0, …}, …]}`; rows without a DOM name or
    /// without a macOS code (`-1`) are skipped.
    private static func load(_ url: URL) -> [String: Int]? {
        guard let data = try? Data(contentsOf: url),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let rows = object["keys"] as? [[String: Any]] else { return nil }
        var table: [String: Int] = [:]
        for row in rows {
            guard let dom = row["dom"] as? String, !dom.isEmpty,
                  let mac = (row["mac"] as? NSNumber)?.intValue, mac >= 0, mac <= Int(UInt16.max) else { continue }
            if table[dom] == nil {
                table[dom] = mac
            }
        }
        return table
    }

    // MARK: - Lookup

    /// The canonical DOM code and virtual key for `name`, or `nil` when unknown.
    func resolve(_ name: String) -> (dom: String, code: CGKeyCode)? {
        guard !name.isEmpty else { return nil }
        if let code = byDOM[name] {
            return (name, CGKeyCode(code))
        }
        if let alias = KeyTable.aliases[name.lowercased()], alias != name {
            return resolve(alias)
        }
        if name.count == 1, let character = name.first, let dom = KeyTable.domCode(for: character), dom != name {
            return resolve(dom)
        }
        if let canonical = byLowercaseDOM[name.lowercased()], let code = byDOM[canonical] {
            return (canonical, CGKeyCode(code))
        }
        return nil
    }

    /// The DOM code of the US-layout key that types `character`.
    static func domCode(for character: Character) -> String? {
        guard character.unicodeScalars.count == 1, let scalar = character.unicodeScalars.first,
              scalar.isASCII else { return nil }
        if character.isLetter {
            return "Key" + String(character).uppercased()
        }
        if character.isNumber {
            return "Digit" + String(character)
        }
        switch character {
        case " ": return "Space"
        case "-", "_": return "Minus"
        case "=", "+": return "Equal"
        case "[", "{": return "BracketLeft"
        case "]", "}": return "BracketRight"
        case "\\", "|": return "Backslash"
        case ";", ":": return "Semicolon"
        case "'", "\"": return "Quote"
        case "`", "~": return "Backquote"
        case ",", "<": return "Comma"
        case ".", ">": return "Period"
        case "/", "?": return "Slash"
        case "\n": return "Enter"
        case "\t": return "Tab"
        default: return nil
        }
    }

    /// The flags for `mods`, or `nil` when a name is not in the contract's vocabulary.
    static func flags(for mods: [String]) -> CGEventFlags? {
        var flags: CGEventFlags = []
        for mod in mods {
            guard let flag = modifierFlags[mod] else { return nil }
            flags.insert(flag)
        }
        return flags
    }
}
