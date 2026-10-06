import AppKit
import ApplicationServices

/// `windows.list` / `windows.focus`.
///
/// The list is `CGWindowListCopyWindowInfo` filtered to normal, visible windows; titles are
/// only present with the Screen Recording grant (an undocumented gate), so without it the
/// owning app's name stands in. Focus matches the Accessibility window of the owning app by
/// frame (and title when known) — public API only, no `_AXUIElementGetWindow` — then
/// de-minimizes, raises it and activates the app.
enum WindowControl {

    /// One on-screen window.
    struct Row {
        let id: CGWindowID
        let pid: pid_t
        let app: String
        let title: String
        let bounds: CGRect
    }

    private static let minimumSide: CGFloat = 50

    // MARK: - Listing

    /// Normal-layer, visible, reasonably sized windows of other processes.
    @MainActor
    static func onScreenWindows() -> [Row] {
        let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
        guard let info = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else { return [] }
        let own = ProcessInfo.processInfo.processIdentifier
        var rows: [Row] = []
        for entry in info {
            guard (entry[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
                  let number = (entry[kCGWindowNumber as String] as? NSNumber)?.uint32Value,
                  let pid = (entry[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value, pid != own,
                  let boundsDictionary = entry[kCGWindowBounds as String] as? NSDictionary,
                  let bounds = CGRect(dictionaryRepresentation: boundsDictionary as CFDictionary) else { continue }
            if let alpha = (entry[kCGWindowAlpha as String] as? NSNumber)?.doubleValue, alpha <= 0 { continue }
            guard bounds.width >= minimumSide, bounds.height >= minimumSide else { continue }
            let owner = entry[kCGWindowOwnerName as String] as? String
                ?? NSRunningApplication(processIdentifier: pid)?.localizedName
                ?? ""
            let title = entry[kCGWindowName as String] as? String ?? ""
            rows.append(Row(id: CGWindowID(number), pid: pid, app: owner, title: title, bounds: bounds))
        }
        return rows
    }

    /// `windows.list` rows: `{id, title, app, pid, display}`.
    @MainActor
    static func list() -> [[String: Any]] {
        let displays = ScreenCapture.displays()
        return onScreenWindows().compactMap { row -> [String: Any]? in
            let title = row.title.isEmpty ? row.app : row.title
            guard !title.isEmpty else { return nil }
            let center = CGPoint(x: row.bounds.midX, y: row.bounds.midY)
            let display = displays.first { $0.bounds.contains(center) }?.index ?? 0
            return [
                "id": String(row.id),
                "title": title,
                "app": row.app,
                "pid": Int(row.pid),
                "display": display,
            ]
        }
    }

    // MARK: - Focus

    /// `windows.focus {id}`.
    @MainActor
    static func focus(id: String) throws {
        guard let number = UInt32(id) else {
            throw ReachError.invalidParams("bad window id \"\(id)\"")
        }
        guard let row = onScreenWindows().first(where: { $0.id == CGWindowID(number) }) else {
            throw ReachError.notFound("no window \(id)")
        }
        try InputInjector.ensureAccessibility()
        guard let app = NSRunningApplication(processIdentifier: row.pid) else {
            throw ReachError.notFound("the window's app is gone")
        }
        raise(row)
        AppsController.activate(app)
    }

    /// De-minimizes and raises the AX window matching `row` (best effort).
    private static func raise(_ row: Row) {
        let application = AXUIElementCreateApplication(row.pid)
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(application, kAXWindowsAttribute as CFString, &value) == .success,
              let windows = value as? [AXUIElement] else { return }
        for window in windows where matches(window, row) {
            AXUIElementSetAttributeValue(window, kAXMinimizedAttribute as CFString, kCFBooleanFalse)
            AXUIElementSetAttributeValue(window, kAXMainAttribute as CFString, kCFBooleanTrue)
            AXUIElementPerformAction(window, kAXRaiseAction as CFString)
            return
        }
    }

    private static func matches(_ window: AXUIElement, _ row: Row) -> Bool {
        guard let frame = axFrame(of: window) else { return false }
        let tolerance: CGFloat = 2
        guard abs(frame.minX - row.bounds.minX) <= tolerance,
              abs(frame.minY - row.bounds.minY) <= tolerance,
              abs(frame.width - row.bounds.width) <= tolerance,
              abs(frame.height - row.bounds.height) <= tolerance else { return false }
        if !row.title.isEmpty, let title = axTitle(of: window), !title.isEmpty, title != row.title {
            return false
        }
        return true
    }

    private static func axTitle(of window: AXUIElement) -> String? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(window, kAXTitleAttribute as CFString, &value) == .success else {
            return nil
        }
        return value as? String
    }

    /// Position and size in Accessibility space (top-left origin, same as `kCGWindowBounds`).
    private static func axFrame(of window: AXUIElement) -> CGRect? {
        guard let position = axValue(of: window, attribute: kAXPositionAttribute, type: .cgPoint),
              let size = axValue(of: window, attribute: kAXSizeAttribute, type: .cgSize) else { return nil }
        var point = CGPoint.zero
        var dimensions = CGSize.zero
        guard AXValueGetValue(position, .cgPoint, &point), AXValueGetValue(size, .cgSize, &dimensions) else {
            return nil
        }
        return CGRect(origin: point, size: dimensions)
    }

    private static func axValue(of window: AXUIElement, attribute: String, type: AXValueType) -> AXValue? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(window, attribute as CFString, &value) == .success,
              let value, CFGetTypeID(value) == AXValueGetTypeID() else { return nil }
        // Checked against the real type ID above, so this cast cannot fail.
        let axValue = value as! AXValue
        return AXValueGetType(axValue) == type ? axValue : nil
    }
}
