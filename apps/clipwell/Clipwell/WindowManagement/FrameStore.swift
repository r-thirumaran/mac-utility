import AppKit

/// Remembers each window's frame from *before* Clipwell first moved it, so
/// "maximize → restore" puts it back exactly where the user had it.
///
/// Keyed by `AXWindowKey` (the `AXUIElement` itself). Entries are only written when a window
/// moves from a frame we do **not** recognise as one of ours; that way a
/// snap-left → snap-right → maximize sequence still restores to the window's original
/// user-chosen frame rather than to an intermediate half.
final class FrameStore {

    /// Windows are cheap to forget and expensive to leak (each key retains an AXUIElement).
    /// Nobody has a thousand windows; if they do, the oldest entries are the least useful.
    private let capacity = 200

    private var frames: [AXWindowKey: (rect: CGRect, stamp: Date)] = [:]

    func remember(_ rect: CGRect, for window: AXWindow) {
        frames[window.key] = (rect, Date())
        if frames.count > capacity { evictOldest() }
    }

    func frame(for window: AXWindow) -> CGRect? {
        frames[window.key]?.rect
    }

    func forget(_ window: AXWindow) {
        frames.removeValue(forKey: window.key)
    }

    /// Drops entries whose window has since closed. Called opportunistically, not on a timer —
    /// a stale entry costs one retained element and nothing else.
    func sweep() {
        for (key, _) in frames where AXWindow(element: key.element).isStale {
            frames.removeValue(forKey: key)
        }
    }

    private func evictOldest() {
        let sorted = frames.sorted { $0.value.stamp < $1.value.stamp }
        for (key, _) in sorted.prefix(frames.count - capacity) {
            frames.removeValue(forKey: key)
        }
    }
}
