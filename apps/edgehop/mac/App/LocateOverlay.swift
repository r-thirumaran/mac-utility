import AppKit

/// Spec §6: a ring that flashes where the pointer actually is, for about a second, after the
/// user shakes the mouse on the machine that is driving this one.
///
/// A borderless, click-through window above everything (including full-screen apps and every
/// Space), covering the screen the cursor is on. The ring shrinks from radius 140 to 28 while
/// following the cursor, then fades out. One overlay at a time: a second `show()` restarts it.
@MainActor
final class LocateOverlay {

    private static let startRadius: CGFloat = 140
    private static let endRadius: CGFloat = 28
    private static let shrink: TimeInterval = 1.0
    private static let fade: TimeInterval = 0.25
    private static let frameInterval: TimeInterval = 1.0 / 60

    private var window: NSWindow?
    private var view: RingView?
    private var timer: DispatchSourceTimer?
    private var startedAt = Date.distantPast

    /// Shows (or restarts) the ring at the current pointer position.
    func show() {
        guard let screen = LocateOverlay.screenUnderCursor() else { return }
        let panel = window ?? makeWindow()
        panel.setFrame(screen.frame, display: false)
        panel.alphaValue = 1
        panel.orderFrontRegardless()
        startedAt = Date()
        tick()
        startTimer()
    }

    /// Hides the ring immediately.
    func hide() {
        timer?.cancel()
        timer = nil
        window?.orderOut(nil)
    }

    // MARK: - Window

    private func makeWindow() -> NSWindow {
        let panel = NSPanel(contentRect: .zero,
                            styleMask: [.borderless, .nonactivatingPanel],
                            backing: .buffered,
                            defer: false)
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = false
        panel.ignoresMouseEvents = true
        panel.level = .screenSaver
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false
        let ring = RingView(frame: .zero)
        panel.contentView = ring
        window = panel
        view = ring
        return panel
    }

    private func startTimer() {
        timer?.cancel()
        let source = DispatchSource.makeTimerSource(queue: .main)
        source.schedule(deadline: .now() + LocateOverlay.frameInterval,
                        repeating: LocateOverlay.frameInterval,
                        leeway: .milliseconds(5))
        source.setEventHandler { [weak self] in
            MainActor.assumeIsolated { self?.tick() }
        }
        source.resume()
        timer = source
    }

    /// One animation frame: move the ring to the cursor and shrink it; fade at the end.
    private func tick() {
        guard let window, let view else { return }
        let elapsed = Date().timeIntervalSince(startedAt)
        if elapsed >= LocateOverlay.shrink + LocateOverlay.fade {
            hide()
            return
        }
        // Keep covering the screen the pointer moved to.
        if let screen = LocateOverlay.screenUnderCursor(), window.frame != screen.frame {
            window.setFrame(screen.frame, display: false)
        }
        let progress = min(max(elapsed / LocateOverlay.shrink, 0), 1)
        let radius = LocateOverlay.startRadius
            + (LocateOverlay.endRadius - LocateOverlay.startRadius) * CGFloat(progress)
        // `NSEvent.mouseLocation` is in screen coordinates (y up); the window's content view
        // wants its own, so subtract the window origin.
        let mouse = NSEvent.mouseLocation
        view.centre = CGPoint(x: mouse.x - window.frame.minX, y: mouse.y - window.frame.minY)
        view.radius = radius
        view.needsDisplay = true
        if elapsed > LocateOverlay.shrink {
            window.alphaValue = CGFloat(1 - min((elapsed - LocateOverlay.shrink) / LocateOverlay.fade, 1))
        }
    }

    private static func screenUnderCursor() -> NSScreen? {
        let mouse = NSEvent.mouseLocation
        return NSScreen.screens.first(where: { $0.frame.contains(mouse) }) ?? NSScreen.main
    }

    // MARK: - Drawing

    /// The ring itself: a thick accent stroke with a thin white outline on either side, so it
    /// stays visible on any wallpaper.
    private final class RingView: NSView {

        var centre: CGPoint = .zero
        var radius: CGFloat = LocateOverlay.startRadius

        override var isOpaque: Bool { false }

        override func draw(_ dirtyRect: NSRect) {
            guard radius > 1 else { return }
            let ring = NSBezierPath(ovalIn: NSRect(x: centre.x - radius, y: centre.y - radius,
                                                   width: radius * 2, height: radius * 2))
            NSColor.white.withAlphaComponent(0.9).setStroke()
            ring.lineWidth = 10
            ring.stroke()
            NSColor.controlAccentColor.setStroke()
            ring.lineWidth = 6
            ring.stroke()
        }
    }
}
