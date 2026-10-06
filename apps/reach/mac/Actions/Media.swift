import AppKit

/// Media keys and volume.
///
/// Play/pause, track skipping, volume steps, mute and brightness are `NX_KEYTYPE_*`
/// system-defined events (`IOKit/hidsystem/ev_keymap.h`) built with `NSEvent.otherEvent`
/// and posted to the HID tap as a down/up pair — the same path a keyboard's media keys take,
/// so they reach whatever app owns Now Playing. Posting needs Accessibility. Absolute volume
/// goes through the StandardAdditions `volume settings` AppleScript, which targets no app and
/// therefore raises no Automation prompt.
enum MediaControl {

    /// `media {action}` → `NX_KEYTYPE_*`.
    static let actionKeys: [String: Int32] = [
        "playPause": 16, "next": 17, "previous": 18, "volumeUp": 0, "volumeDown": 1, "mute": 7,
    ]

    /// DOM key names with no virtual key code that still have a system-defined event.
    static let domKeys: [String: Int32] = [
        "MediaPlayPause": 16, "MediaTrackNext": 17, "MediaTrackPrevious": 18,
        "AudioVolumeUp": 0, "AudioVolumeDown": 1, "AudioVolumeMute": 7,
        "BrightnessUp": 2, "BrightnessDown": 3,
    ]

    private static let systemDefinedSubtype: Int16 = 8

    /// The `NX_KEYTYPE_*` value of a media/audio DOM name, if any.
    static func systemKey(forDOM name: String) -> Int32? {
        domKeys[name]
    }

    // MARK: - Keys

    @MainActor
    static func perform(action: String) throws {
        guard let key = actionKeys[action] else {
            throw ReachError.invalidParams("unknown media action \"\(action)\"")
        }
        try InputInjector.ensureAccessibility()
        press(key)
    }

    @MainActor
    static func press(_ key: Int32) {
        postSystemKey(key, down: true)
        postSystemKey(key, down: false)
    }

    /// One edge of an aux-control key: `data1 = key << 16 | (0x0A down / 0x0B up) << 8`.
    @MainActor
    static func postSystemKey(_ key: Int32, down: Bool) {
        let flags = NSEvent.ModifierFlags(rawValue: down ? 0xA00 : 0xB00)
        let data1 = (Int(key) << 16) | ((down ? 0x0A : 0x0B) << 8)
        guard let event = NSEvent.otherEvent(with: .systemDefined, location: .zero, modifierFlags: flags,
                                             timestamp: 0, windowNumber: 0, context: nil,
                                             subtype: systemDefinedSubtype, data1: data1, data2: -1) else {
            Log.error("media: could not build the system-defined event", category: "media")
            return
        }
        event.cgEvent?.post(tap: .cghidEventTap)
    }

    // MARK: - Volume

    /// `volume.get` → level 0–100 and the mute state, or `not_supported`.
    @MainActor
    static func volume() throws -> (level: Int, muted: Bool) {
        let script = "set v to get volume settings\nreturn {output volume of v, output muted of v}"
        let result = try run(script)
        guard result.numberOfItems >= 2,
              let levelDescriptor = result.atIndex(1),
              let mutedDescriptor = result.atIndex(2) else {
            throw ReachError.notSupported("master volume unavailable")
        }
        let level = min(max(Int(levelDescriptor.int32Value), 0), 100)
        return (level, mutedDescriptor.booleanValue)
    }

    /// `volume.set {level}`; a level above zero also unmutes, like the Go agent.
    @MainActor
    static func setVolume(_ level: Int) throws {
        let clamped = min(max(level, 0), 100)
        var script = "set volume output volume \(clamped)"
        if clamped > 0 {
            script += "\nset volume without output muted"
        }
        _ = try run(script)
    }

    @MainActor
    private static func run(_ source: String) throws -> NSAppleEventDescriptor {
        guard let script = NSAppleScript(source: source) else {
            throw ReachError.notSupported("master volume unavailable: could not build the script")
        }
        var errorInfo: NSDictionary?
        let result = script.executeAndReturnError(&errorInfo)
        if let errorInfo {
            let message = errorInfo[NSAppleScript.errorMessage] as? String ?? "AppleScript error"
            throw ReachError.notSupported("master volume unavailable: \(message)")
        }
        return result
    }
}
