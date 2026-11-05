import Foundation

/// Spec §6: "shake the pointer to find it". Fed with the RAW horizontal deltas of the physical
/// mouse (the same `dx` the event tap reports), it fires when the user wobbles the pointer
/// quickly from side to side.
///
/// The rule: inside a sliding 600 ms window there must be at least 4 reversals of horizontal
/// direction, where each stroke between two reversals travelled at least 40 units. After a
/// hit, 1500 ms of cooldown. Detection happens on the machine holding the physical mouse,
/// whether or not it is forwarding.
///
/// Pure logic — no timers and no clock of its own, `feed` takes the event's timestamp — so it
/// can be exercised without an event tap.
///
/// Worked examples (strokes of ±60 units, 50 ms apart, dead zone-free):
/// * `+60, −60, +60, −60, +60` → 4 reversals inside 200 ms → fires on the last one.
/// * `+60, −60, +60` → 2 reversals → no fire.
/// * `+60, −60, +60, −60, +60` spread 200 ms apart → the first reversals fall out of the
///   600 ms window before the fourth arrives → no fire.
/// * `+20, −20, +20, −20, +20` → every stroke is below 40 units → no fire.
struct ShakeDetector {

    /// Reversals must all fall inside this window.
    static let window: TimeInterval = 0.6
    /// A stroke shorter than this does not count as a direction change.
    static let minStroke = 40
    static let reversalsNeeded = 4
    static let cooldown: TimeInterval = 1.5

    /// Sign of the stroke being accumulated (0 = nothing yet).
    private var direction = 0
    /// Units travelled in `direction` since the last counted reversal.
    private var travelled = 0
    /// Timestamps of the reversals still inside the window.
    private var reversals: [Date] = []
    private var firedAt = Date.distantPast

    init() {}

    /// Feeds one raw horizontal delta. Returns `true` exactly once per detected shake.
    mutating func feed(dx: Int, at now: Date) -> Bool {
        guard dx != 0 else { return false }
        let sign = dx > 0 ? 1 : -1
        if direction == 0 {
            direction = sign
            travelled = abs(dx)
            return false
        }
        if sign == direction {
            travelled += abs(dx)
            return false
        }
        // A turn: it only counts when the stroke that just ended was long enough.
        let longEnough = travelled >= ShakeDetector.minStroke
        direction = sign
        travelled = abs(dx)
        guard longEnough else {
            reversals.removeAll()
            return false
        }
        reversals.append(now)
        reversals.removeAll { now.timeIntervalSince($0) > ShakeDetector.window }
        guard reversals.count >= ShakeDetector.reversalsNeeded else { return false }
        guard now.timeIntervalSince(firedAt) >= ShakeDetector.cooldown else {
            reversals.removeAll()
            return false
        }
        firedAt = now
        reversals.removeAll()
        return true
    }

    /// Forgets the strokes seen so far (the pointer went somewhere else entirely).
    mutating func reset() {
        direction = 0
        travelled = 0
        reversals.removeAll()
    }
}
