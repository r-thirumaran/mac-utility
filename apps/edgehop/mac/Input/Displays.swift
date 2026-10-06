import AppKit
import CoreGraphics
import Foundation

/// The display list in protocol coordinates and a watcher for changes.
///
/// `CGDisplayBounds` is already in the global display space (origin at the top-left of the
/// primary display, y down, points), which is the coordinate space of `docs/edgehop-protocol.md`
/// §6 and of `CGEvent.location`, so no AppKit flipping is needed. The scale comes from the
/// matching `NSScreen`.
@MainActor
final class Displays {

    /// Fired on the main actor after `NSApplication.didChangeScreenParametersNotification`.
    var onChange: (() -> Void)?

    private var observer: NSObjectProtocol?

    init() {
        observer = NotificationCenter.default.addObserver(forName: NSApplication.didChangeScreenParametersNotification,
                                                          object: nil,
                                                          queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.onChange?()
            }
        }
    }

    // MARK: - Enumeration

    static func current() -> [Display] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        guard CGGetActiveDisplayList(count, &ids, &count) == .success else { return [] }
        let scales = backingScales()
        return ids.prefix(Int(count)).map { id in
            let bounds = CGDisplayBounds(id)
            return Display(id: String(id),
                           x: Int(bounds.origin.x.rounded()),
                           y: Int(bounds.origin.y.rounded()),
                           w: Int(bounds.size.width.rounded()),
                           h: Int(bounds.size.height.rounded()),
                           scale: scales[id] ?? 1,
                           primary: CGDisplayIsMain(id) != 0)
        }
    }

    private static func backingScales() -> [CGDirectDisplayID: Double] {
        var scales: [CGDirectDisplayID: Double] = [:]
        for screen in NSScreen.screens {
            guard let number = screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber else {
                continue
            }
            scales[number.uint32Value] = Double(screen.backingScaleFactor)
        }
        return scales
    }
}
