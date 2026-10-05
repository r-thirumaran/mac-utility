import AppKit

/// Screen maths and — critically — the conversion between the two coordinate spaces this app
/// straddles.
///
/// **The one gotcha that matters.** Cocoa (`NSScreen`, `NSWindow`) puts the origin at the
/// *bottom-left* of the primary screen with +y going up. The Accessibility API puts it at the
/// *top-left* of the primary screen with +y going down. Both are global spaces spanning every
/// display, so on a multi-monitor rig getting this wrong doesn't nudge a window a few points —
/// it throws it onto the wrong physical display entirely.
///
/// The conversion is its own inverse:
///
///     axY = primary.maxY - cocoaRect.maxY
///
/// which is why `axRect(fromCocoa:)` and `cocoaRect(fromAX:)` are the same arithmetic.
/// "Primary" always means `NSScreen.screens[0]` — the screen with the menu bar, which anchors
/// both coordinate systems. It is *not* `NSScreen.main`, which is merely the screen with the
/// key window and moves around.
enum ScreenGeometry {

    /// Frame of the coordinate-anchoring screen. Never use `NSScreen.main` here.
    static var primaryFrame: CGRect {
        NSScreen.screens.first?.frame ?? .zero
    }

    static func axRect(fromCocoa rect: CGRect) -> CGRect {
        CGRect(x: rect.minX,
               y: primaryFrame.maxY - rect.maxY,
               width: rect.width,
               height: rect.height)
    }

    static func cocoaRect(fromAX rect: CGRect) -> CGRect {
        CGRect(x: rect.minX,
               y: primaryFrame.maxY - rect.maxY,
               width: rect.width,
               height: rect.height)
    }

    // MARK: - Screen lookup

    /// The screen a window "lives on": the one containing its centre, falling back to the
    /// screen it overlaps most (a window straddling a bezel), then to the main screen.
    static func screen(containing rect: CGRect) -> NSScreen {
        let centre = CGPoint(x: rect.midX, y: rect.midY)
        if let hit = NSScreen.screens.first(where: { $0.frame.contains(centre) }) {
            return hit
        }
        let best = NSScreen.screens.max { a, b in
            a.frame.intersection(rect).area < b.frame.intersection(rect).area
        }
        return best ?? NSScreen.main ?? NSScreen.screens[0]
    }

    /// Displays ordered by physical left-to-right position, which is what "next display"
    /// means to a person looking at their desk.
    static var displaysLeftToRight: [NSScreen] {
        NSScreen.screens.sorted { $0.frame.minX < $1.frame.minX }
    }

    /// Locates a screen within the ordered list.
    ///
    /// Matches on `frame` rather than object identity: `NSScreen.screens` hands back fresh
    /// instances after a display reconfiguration, and an identity miss here would silently
    /// mean "no display in that direction" — i.e. a window that refuses to cross monitors.
    /// Two distinct screens cannot share a frame, so this is exact.
    private static func index(of screen: NSScreen, in ordered: [NSScreen]) -> Int? {
        ordered.firstIndex { $0 === screen } ?? ordered.firstIndex { $0.frame == screen.frame }
    }

    /// The display immediately to the left of `screen`, or `nil` at the left edge
    /// (unless wrapping is enabled).
    static func display(leftOf screen: NSScreen, wrap: Bool) -> NSScreen? {
        let ordered = displaysLeftToRight
        guard ordered.count > 1, let index = index(of: screen, in: ordered) else { return nil }
        if index > 0 { return ordered[index - 1] }
        return wrap ? ordered.last : nil
    }

    static func display(rightOf screen: NSScreen, wrap: Bool) -> NSScreen? {
        let ordered = displaysLeftToRight
        guard ordered.count > 1, let index = index(of: screen, in: ordered) else { return nil }
        if index < ordered.count - 1 { return ordered[index + 1] }
        return wrap ? ordered.first : nil
    }

    // MARK: - Target rects
    //
    // All targets derive from `visibleFrame`, which already excludes the menu bar and the
    // Dock. Using `frame` instead would tuck windows underneath both.

    static func leftHalf(of screen: NSScreen) -> CGRect {
        let v = screen.visibleFrame
        return CGRect(x: v.minX, y: v.minY, width: (v.width / 2).rounded(.down), height: v.height)
    }

    /// Computed from `maxX` rather than as `minX + width` so that on an odd-width display the
    /// two halves still meet exactly, with no one-point seam or overlap.
    static func rightHalf(of screen: NSScreen) -> CGRect {
        let v = screen.visibleFrame
        let width = (v.width / 2).rounded(.down)
        return CGRect(x: v.maxX - width, y: v.minY, width: width, height: v.height)
    }

    static func maximized(of screen: NSScreen) -> CGRect {
        screen.visibleFrame
    }

    /// Fallback "restored" size when a window is maximized but we have no remembered frame
    /// (it was already maximized before Clipwell launched).
    static func defaultRestored(on screen: NSScreen) -> CGRect {
        let v = screen.visibleFrame
        let size = CGSize(width: (v.width * 0.7).rounded(), height: (v.height * 0.7).rounded())
        return CGRect(x: v.midX - size.width / 2,
                      y: v.midY - size.height / 2,
                      width: size.width,
                      height: size.height)
    }
}

extension CGRect {
    var area: CGFloat { isNull || isInfinite ? 0 : width * height }

    /// Windows never land on their target to the point — apps round, and some clamp to a
    /// character grid (Terminal) or an integral scale. A few points of slop keeps the
    /// "is this window already snapped left?" test honest.
    func isApproximately(_ other: CGRect, tolerance: CGFloat = 6) -> Bool {
        abs(minX - other.minX) <= tolerance &&
        abs(minY - other.minY) <= tolerance &&
        abs(width - other.width) <= tolerance &&
        abs(height - other.height) <= tolerance
    }
}
