import Foundation

/// Magic Trackpad gestures for the machine in control.
///
/// A trackpad gesture never reaches the wire as itself: macOS resolves a three-finger swipe or
/// a pinch inside the window server, and the remote machine has no trackpad to replay it on.
/// So the machine holding the trackpad recognises the gesture, drops it locally (the tap
/// swallows it, or the local Mac would switch desktops under the user's hands) and sends the
/// keyboard shortcut — or the Ctrl+wheel notch — that does the same thing on the machine in
/// control. Only while forwarding: when this Mac is the active machine every gesture passes
/// through untouched and macOS handles it.
///
/// This file is pure logic in three layers, so it can be exercised without a trackpad:
///   `GestureReading` — one raw window-server gesture event, already decoded by `EventTap`.
///   `GestureRecognizer` — accumulates readings into one `TrackpadGesture` per completed gesture.
///   `GestureTranslator` — gesture + the target machine's platform → the chord to send.

// MARK: - Gestures

/// One completed gesture, platform-independent.
enum TrackpadGesture: Equatable {
    /// Three- or four-finger swipe sideways (macOS "move between full-screen apps/spaces").
    case swipeLeft
    case swipeRight
    /// Three- or four-finger swipe up (Mission Control).
    case swipeUp
    /// Three- or four-finger swipe down (App Exposé).
    case swipeDown
    /// Pinch open / closed, one per 0.25 of accumulated magnification.
    case zoomIn
    case zoomOut
    /// Force Touch: the click went through to stage 2.
    case forceClick
    /// Thumb-and-three-fingers spread (Show Desktop). Not currently delivered to a
    /// `CGEventTap` in a form this code can tell apart — see `GestureRecognizer`.
    case showDesktop

    /// The only gesture text that is ever logged.
    var name: String {
        switch self {
        case .swipeLeft: return "swipe left"
        case .swipeRight: return "swipe right"
        case .swipeUp: return "swipe up"
        case .swipeDown: return "swipe down"
        case .zoomIn: return "pinch open"
        case .zoomOut: return "pinch closed"
        case .forceClick: return "force click"
        case .showDesktop: return "show desktop"
        }
    }
}

// MARK: - Readings

/// What `EventTap` could read out of one window-server gesture event. Raw types 29 (the
/// `NSEvent` gesture family) and 30 (Dock control) carry their payload in private `CGEvent`
/// fields; the tap reads those and fills this in, so nothing below needs a `CGEvent`.
struct GestureReading: Equatable {

    enum Source: Equatable {
        /// Dock control (raw type 30): the desktop swipes, Mission Control, App Exposé.
        case dockSwipe
        /// Pinch (gesture HID type 8 / `NSEvent` `.magnify`).
        case magnify
        /// Force Touch click stages (`NSEvent` `.pressure`).
        case pressure
    }

    /// Phase values of field 132, as `NSEvent.Phase` also reports them.
    static let phaseBegan = 1
    static let phaseChanged = 2
    static let phaseEnded = 4
    static let phaseCancelled = 8

    /// Dock swipe axis (field 123): 1 horizontal, 2 vertical.
    static let axisHorizontal = 1
    static let axisVertical = 2

    var source: Source
    /// `GestureReading.axisHorizontal` / `.axisVertical` for a dock swipe, 0 otherwise.
    var axis = 0
    /// Signed progress (dock swipe) or magnification (pinch) contributed by this one event.
    var delta = 0.0
    /// `phaseBegan` / `phaseChanged` / `phaseEnded` / `phaseCancelled`, 0 when the event
    /// carries no phase.
    var phase = 0
    /// Force Touch stage: 0 none, 1 click, 2 force click.
    var stage = 0

    init(source: Source) {
        self.source = source
    }
}

// MARK: - Recognition

/// Turns a stream of `GestureReading`s into at most one `TrackpadGesture` per gesture.
///
/// Swipes fire once, on the ending phase, from the accumulated direction — a desktop swipe
/// arrives as dozens of progress frames and must not send dozens of Ctrl+→. A pinch is the one
/// exception to "one per gesture": zooming wants to keep going while the fingers move, so it
/// fires one step per `zoomStep` of accumulated magnification and keeps the remainder.
struct GestureRecognizer {

    /// Accumulated |progress| a dock swipe needs before it counts (a full desktop swipe
    /// reaches about 1.0; a flick that changes nothing stays well under this).
    static let swipeThreshold = 0.25
    /// Accumulated magnification per zoom step.
    static let zoomStep = 0.25

    /// `true` when a positive accumulated dock-swipe progress means the fingers went right
    /// (horizontal) or up (vertical). macOS does not document the sign; flip this one constant
    /// if the hardware disagrees — the debug line `gesture: swipe left → …` says which way the
    /// recognizer read it.
    static let positiveProgressIsRightOrUp = true

    private var swipeAxis = 0
    private var swipeProgress = 0.0
    private var zoomProgress = 0.0
    private var stage = 0

    init() {}

    /// Forgets a gesture in flight (control moved, or the setting was turned off mid-swipe).
    mutating func reset() {
        swipeAxis = 0
        swipeProgress = 0
        zoomProgress = 0
        stage = 0
    }

    /// Feeds one reading; returns a gesture when this reading completed one.
    ///
    /// Worked example — a three-finger swipe to the left over six events:
    ///   began 0.00, changed −0.21, −0.33, −0.28, −0.19, ended 0.00
    ///   → progress −1.01, |progress| ≥ 0.25, negative on the horizontal axis → `.swipeLeft`.
    /// Worked example — a pinch open:
    ///   began 0.00, changed +0.08, +0.11, +0.09 (0.28 ≥ 0.25 → `.zoomIn`, 0.03 kept), …
    mutating func feed(_ reading: GestureReading) -> TrackpadGesture? {
        switch reading.source {
        case .dockSwipe:
            return feedDockSwipe(reading)
        case .magnify:
            return feedMagnify(reading)
        case .pressure:
            return feedPressure(reading)
        }
    }

    private mutating func feedDockSwipe(_ reading: GestureReading) -> TrackpadGesture? {
        if reading.phase == GestureReading.phaseBegan {
            swipeAxis = reading.axis
            swipeProgress = 0
        }
        if reading.axis != 0 {
            swipeAxis = reading.axis
        }
        swipeProgress += reading.delta
        guard reading.phase == GestureReading.phaseEnded else {
            if reading.phase == GestureReading.phaseCancelled {
                swipeProgress = 0
            }
            return nil
        }
        let progress = swipeProgress
        let axis = swipeAxis
        swipeProgress = 0
        swipeAxis = 0
        guard abs(progress) >= GestureRecognizer.swipeThreshold else { return nil }
        let forward = GestureRecognizer.positiveProgressIsRightOrUp ? progress > 0 : progress < 0
        switch axis {
        case GestureReading.axisHorizontal:
            return forward ? .swipeRight : .swipeLeft
        case GestureReading.axisVertical:
            return forward ? .swipeUp : .swipeDown
        default:
            // Dock control also reports axes this code cannot name (the thumb-and-fingers
            // spread may be one of them); better nothing than the wrong desktop.
            return nil
        }
    }

    private mutating func feedMagnify(_ reading: GestureReading) -> TrackpadGesture? {
        if reading.phase == GestureReading.phaseBegan {
            zoomProgress = 0
        }
        if reading.phase == GestureReading.phaseEnded || reading.phase == GestureReading.phaseCancelled {
            zoomProgress = 0
            return nil
        }
        zoomProgress += reading.delta
        guard abs(zoomProgress) >= GestureRecognizer.zoomStep else { return nil }
        let zoomIn = zoomProgress > 0
        zoomProgress -= (zoomIn ? GestureRecognizer.zoomStep : -GestureRecognizer.zoomStep)
        return zoomIn ? .zoomIn : .zoomOut
    }

    private mutating func feedPressure(_ reading: GestureReading) -> TrackpadGesture? {
        let was = stage
        stage = reading.stage
        return was < 2 && reading.stage >= 2 ? .forceClick : nil
    }
}

// MARK: - Translation

/// The platform of the machine in control (`plat` in the peer's `info`).
enum GestureTarget: Equatable {
    case macOS
    case windows
    case unknown

    init(plat: String) {
        switch plat.lowercased() {
        case "macos", "darwin": self = .macOS
        case "windows": self = .windows
        default: self = .unknown
        }
    }
}

/// What to send for one gesture: modifiers to hold, then a key tap or a wheel notch.
///
/// Usages are HID (`shared/keymap/hid-keymap.json` wire values) so they go straight into a
/// `key` frame. They name the chord as the *target* should receive it; the ⌘/Ctrl swap the
/// receiving machine applies to cross-platform input is undone by the caller, not here.
struct GestureChord: Equatable {

    /// HID usages of the left-hand modifiers: 0xE0 Ctrl, 0xE2 Alt, 0xE3 ⌘/Win.
    var mods: [Int]
    /// HID usage of the key to tap while they are held, or `nil` for a wheel-only chord.
    var key: Int?
    /// Wheel notches to send while they are held: +1 up, −1 down, 0 none.
    var wheel: Int
    /// Human name of the chord, the only chord text that is ever logged.
    var name: String

    init(mods: [Int], key: Int? = nil, wheel: Int = 0, name: String) {
        self.mods = mods
        self.key = key
        self.wheel = wheel
        self.name = name
    }
}

/// Gesture + target platform → chord. Pure; no state, no side effects.
enum GestureTranslator {

    /// HID usages used by the chords.
    private enum HID {
        static let ctrl = 0xE0
        static let gui = 0xE3
        static let keyD = 0x07
        static let tab = 0x2B
        static let minus = 0x2D
        static let equal = 0x2E
        static let f11 = 0x44
        static let right = 0x4F
        static let left = 0x50
        static let down = 0x51
        static let up = 0x52
    }

    /// The chord for `gesture` on `target`, or `nil` when that platform has no equivalent.
    ///
    /// Worked examples:
    ///   `.swipeRight` to a Mac     → Ctrl+→          (next desktop)
    ///   `.swipeRight` to Windows   → Win+Ctrl+→      (next virtual desktop)
    ///   `.swipeUp` to a Mac        → Ctrl+↑          (Mission Control)
    ///   `.swipeUp` to Windows      → Win+Tab         (Task View)
    ///   `.swipeDown` to Windows    → nil             (no App Exposé)
    ///   `.zoomIn` to a Mac         → ⌘=              (zoom in)
    ///   `.zoomIn` to Windows       → Ctrl + wheel +1 (zoom in)
    ///   `.forceClick` to a Mac     → Ctrl+⌘+D        (look up)
    ///   `.forceClick` to Windows   → nil
    ///   anything to `.unknown`     → nil             (platform not announced yet)
    static func chord(for gesture: TrackpadGesture, target: GestureTarget) -> GestureChord? {
        switch target {
        case .macOS: return macChord(gesture)
        case .windows: return windowsChord(gesture)
        case .unknown: return nil
        }
    }

    private static func macChord(_ gesture: TrackpadGesture) -> GestureChord? {
        switch gesture {
        case .swipeLeft:
            return GestureChord(mods: [HID.ctrl], key: HID.left, name: "Ctrl+Left")
        case .swipeRight:
            return GestureChord(mods: [HID.ctrl], key: HID.right, name: "Ctrl+Right")
        case .swipeUp:
            return GestureChord(mods: [HID.ctrl], key: HID.up, name: "Ctrl+Up")
        case .swipeDown:
            return GestureChord(mods: [HID.ctrl], key: HID.down, name: "Ctrl+Down")
        case .zoomIn:
            return GestureChord(mods: [HID.gui], key: HID.equal, name: "Cmd+Equal")
        case .zoomOut:
            return GestureChord(mods: [HID.gui], key: HID.minus, name: "Cmd+Minus")
        case .forceClick:
            return GestureChord(mods: [HID.ctrl, HID.gui], key: HID.keyD, name: "Ctrl+Cmd+D")
        case .showDesktop:
            return GestureChord(mods: [], key: HID.f11, name: "F11")
        }
    }

    private static func windowsChord(_ gesture: TrackpadGesture) -> GestureChord? {
        switch gesture {
        case .swipeLeft:
            return GestureChord(mods: [HID.gui, HID.ctrl], key: HID.left, name: "Win+Ctrl+Left")
        case .swipeRight:
            return GestureChord(mods: [HID.gui, HID.ctrl], key: HID.right, name: "Win+Ctrl+Right")
        case .swipeUp:
            return GestureChord(mods: [HID.gui], key: HID.tab, name: "Win+Tab")
        case .swipeDown:
            // Windows has no App Exposé; Win+Tab is already taken by the swipe up.
            return nil
        case .zoomIn:
            return GestureChord(mods: [HID.ctrl], wheel: 1, name: "Ctrl+wheel up")
        case .zoomOut:
            return GestureChord(mods: [HID.ctrl], wheel: -1, name: "Ctrl+wheel down")
        case .forceClick:
            // Force Touch has no Windows equivalent.
            return nil
        case .showDesktop:
            return GestureChord(mods: [HID.gui], key: HID.keyD, name: "Win+D")
        }
    }
}
