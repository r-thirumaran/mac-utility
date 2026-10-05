import AppKit

enum SnapAction {
    case left
    case right
    case maximize
    case restoreOrMinimize
}

/// Applies Windows-style snapping to the focused window.
///
/// ## The cycling state machine
///
/// Windows 11 treats Win+←/→ as a walk across a chain of half-slots spanning every display,
/// left to right. Clipwell implements the same idea:
///
///     press ←  |  window is left-half of S   →  right-half of the display left of S
///              |  window is right-half of S  →  left-half of S
///              |  anything else              →  left-half of the display it is on
///
///     press →  |  mirror image of the above
///
/// On a single display this collapses to a left/right toggle. At the outermost display the
/// window stays put and the machine beeps, unless "wrap around" is enabled in Settings —
/// Windows does not wrap, so that defaults off.
final class WindowManager {

    private let frameStore = FrameStore()
    private let preferences: Preferences

    init(preferences: Preferences = .shared) {
        self.preferences = preferences
    }

    /// How a window's current frame relates to the slots Clipwell manages.
    private enum Slot {
        case leftHalf(NSScreen)
        case rightHalf(NSScreen)
        case maximized(NSScreen)
    }

    // MARK: - Entry point

    func perform(_ action: SnapAction) {
        guard AccessibilityPermission.isTrusted else {
            // Nothing we can do without the permission; nudge rather than fail silently.
            NotificationCenter.default.post(name: .clipWellNeedsAccessibility, object: nil)
            return
        }
        guard let window = AXWindow.focused(), window.isSnappable else {
            NSSound.beep()
            return
        }
        guard let frame = window.cocoaFrame else {
            NSSound.beep()
            return
        }

        // A minimized window cannot be measured meaningfully; un-minimize first so the
        // snap has something real to act on.
        if window.isMinimized { window.setMinimized(false) }

        switch action {
        case .left:              cycle(window, from: frame, towards: .left)
        case .right:             cycle(window, from: frame, towards: .right)
        case .maximize:          maximize(window, from: frame)
        case .restoreOrMinimize: restoreOrMinimize(window, from: frame)
        }

        frameStore.sweep()
    }

    // MARK: - Halves and display cycling

    private enum Direction { case left, right }

    private func cycle(_ window: AXWindow, from frame: CGRect, towards direction: Direction) {
        let wrap = preferences.wrapDisplays
        let currentScreen = ScreenGeometry.screen(containing: frame)
        let target: CGRect?

        switch (classify(frame), direction) {

        // Already parked on the leading edge: step to the neighbouring display and land on
        // its opposite half, so the window keeps travelling in the direction pressed.
        case (.leftHalf(let screen), .left):
            target = ScreenGeometry.display(leftOf: screen, wrap: wrap)
                .map { ScreenGeometry.rightHalf(of: $0) }

        case (.rightHalf(let screen), .right):
            target = ScreenGeometry.display(rightOf: screen, wrap: wrap)
                .map { ScreenGeometry.leftHalf(of: $0) }

        // Snapped to the far half: come back across this display.
        case (.rightHalf(let screen), .left):
            target = ScreenGeometry.leftHalf(of: screen)

        case (.leftHalf(let screen), .right):
            target = ScreenGeometry.rightHalf(of: screen)

        // Maximized or free-floating: take the half on the display it already occupies.
        case (.maximized(let screen), .left):
            target = ScreenGeometry.leftHalf(of: screen)

        case (.maximized(let screen), .right):
            target = ScreenGeometry.rightHalf(of: screen)

        case (nil, .left):
            target = ScreenGeometry.leftHalf(of: currentScreen)

        case (nil, .right):
            target = ScreenGeometry.rightHalf(of: currentScreen)
        }

        guard let target else {
            NSSound.beep()   // no further display in that direction
            return
        }
        apply(target, to: window, currentFrame: frame)
    }

    // MARK: - Maximize / restore / minimize

    private func maximize(_ window: AXWindow, from frame: CGRect) {
        let screen = ScreenGeometry.screen(containing: frame)
        apply(ScreenGeometry.maximized(of: screen), to: window, currentFrame: frame)
    }

    /// Win+Down semantics: collapse a maximized window back to its previous frame; from any
    /// other state, minimize to the Dock.
    private func restoreOrMinimize(_ window: AXWindow, from frame: CGRect) {
        guard case .maximized(let screen)? = classify(frame) else {
            window.setMinimized(true)
            return
        }

        if let saved = frameStore.frame(for: window) {
            window.setCocoaFrame(saved)
            frameStore.forget(window)
        } else {
            // Maximized before Clipwell ever saw it, so there is nothing to restore *to*.
            // A centred 70% rect is the closest thing to what Windows would do with its
            // own remembered restore rect.
            window.setCocoaFrame(ScreenGeometry.defaultRestored(on: screen))
        }
    }

    // MARK: - Helpers

    /// Saves the pre-move frame (only when moving out of an unmanaged position) and applies
    /// the new one.
    private func apply(_ target: CGRect, to window: AXWindow, currentFrame: CGRect) {
        if classify(currentFrame) == nil {
            frameStore.remember(currentFrame, for: window)
        }
        window.setCocoaFrame(target)
    }

    /// Recognises whether a frame is currently sitting in one of the slots Clipwell manages,
    /// on any display. Returns `nil` for a window the user has placed freely.
    private func classify(_ frame: CGRect) -> Slot? {
        for screen in NSScreen.screens {
            // Maximized is checked first: on a very narrow display a half could otherwise
            // be mistaken for the full visible frame.
            if frame.isApproximately(ScreenGeometry.maximized(of: screen)) {
                return .maximized(screen)
            }
            if frame.isApproximately(ScreenGeometry.leftHalf(of: screen)) {
                return .leftHalf(screen)
            }
            if frame.isApproximately(ScreenGeometry.rightHalf(of: screen)) {
                return .rightHalf(screen)
            }
        }
        return nil
    }
}

extension Notification.Name {
    /// Raised when an action needed Accessibility and did not have it.
    static let clipWellNeedsAccessibility = Notification.Name("ClipwellNeedsAccessibility")
}
