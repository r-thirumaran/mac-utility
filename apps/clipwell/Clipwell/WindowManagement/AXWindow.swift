import AppKit
import ApplicationServices

/// A hashable handle for an `AXUIElement`, so windows can key a dictionary.
///
/// `AXUIElement` is a CoreFoundation type that implements `CFEqual`/`CFHash` correctly: two
/// elements obtained separately for the same window compare equal. That lets us key the
/// "previous frame" store off the element itself and stay on public API — the alternative,
/// `_AXUIElementGetWindow`, is a private symbol that would tie the app to an SPI Apple can
/// remove without notice.
struct AXWindowKey: Hashable {
    let element: AXUIElement

    static func == (lhs: AXWindowKey, rhs: AXWindowKey) -> Bool {
        CFEqual(lhs.element, rhs.element)
    }

    func hash(into hasher: inout Hasher) {
        hasher.combine(CFHash(element))
    }
}

/// Thin, readable wrapper over the Accessibility attributes of one window.
///
/// Every accessor returns an optional rather than trapping: AX calls fail routinely and
/// legitimately — the app quit mid-call, the window is a sheet that vanished, the process is
/// not AX-aware at all. A snap that silently does nothing beats a crash.
struct AXWindow {

    let element: AXUIElement

    var key: AXWindowKey { AXWindowKey(element: element) }

    // MARK: - Discovery

    /// The focused window of the frontmost application.
    ///
    /// Deliberately goes through `NSWorkspace.frontmostApplication` rather than the system-wide
    /// AX element: it is the app the *user* considers active, and it skips over our own
    /// non-activating panel.
    static func focused() -> AXWindow? {
        guard let app = NSWorkspace.shared.frontmostApplication else { return nil }
        return focused(of: app)
    }

    static func focused(of app: NSRunningApplication) -> AXWindow? {
        let appElement = AXUIElementCreateApplication(app.processIdentifier)
        var value: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(appElement,
                                                kAXFocusedWindowAttribute as CFString,
                                                &value)
        guard err == .success,
              let value,
              CFGetTypeID(value) == AXUIElementGetTypeID() else { return nil }
        // Checked against the real type ID above, so this cast cannot fail.
        return AXWindow(element: value as! AXUIElement)
    }

    // MARK: - Attribute plumbing

    private func copyValue(_ attribute: String) -> CFTypeRef? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success
        else { return nil }
        return value
    }

    private func axValue(_ attribute: String, type: AXValueType) -> AXValue? {
        guard let value = copyValue(attribute),
              CFGetTypeID(value) == AXValueGetTypeID() else { return nil }
        let axv = value as! AXValue
        return AXValueGetType(axv) == type ? axv : nil
    }

    func isSettable(_ attribute: String) -> Bool {
        var settable: DarwinBoolean = false
        guard AXUIElementIsAttributeSettable(element,
                                             attribute as CFString,
                                             &settable) == .success else { return false }
        return settable.boolValue
    }

    /// `true` once the underlying window is gone; used to sweep the frame store.
    var isStale: Bool {
        var value: CFTypeRef?
        let err = AXUIElementCopyAttributeValue(element, kAXRoleAttribute as CFString, &value)
        return err == .invalidUIElement || err == .cannotComplete
    }

    // MARK: - Geometry (Accessibility space, top-left origin)

    var axPosition: CGPoint? {
        guard let value = axValue(kAXPositionAttribute, type: .cgPoint) else { return nil }
        var point = CGPoint.zero
        return AXValueGetValue(value, .cgPoint, &point) ? point : nil
    }

    var axSize: CGSize? {
        guard let value = axValue(kAXSizeAttribute, type: .cgSize) else { return nil }
        var size = CGSize.zero
        return AXValueGetValue(value, .cgSize, &size) ? size : nil
    }

    @discardableResult
    func setAXPosition(_ point: CGPoint) -> Bool {
        var point = point
        guard let value = AXValueCreate(.cgPoint, &point) else { return false }
        return AXUIElementSetAttributeValue(element,
                                            kAXPositionAttribute as CFString,
                                            value) == .success
    }

    @discardableResult
    func setAXSize(_ size: CGSize) -> Bool {
        var size = size
        guard let value = AXValueCreate(.cgSize, &size) else { return false }
        return AXUIElementSetAttributeValue(element,
                                            kAXSizeAttribute as CFString,
                                            value) == .success
    }

    // MARK: - Geometry (Cocoa space, bottom-left origin)

    var cocoaFrame: CGRect? {
        guard let position = axPosition, let size = axSize else { return nil }
        return ScreenGeometry.cocoaRect(fromAX: CGRect(origin: position, size: size))
    }

    /// Moves and resizes the window, given a rect in **Cocoa** coordinates.
    ///
    /// The size → position → size dance is not superstition. Apps enforce their own min/max
    /// sizes and some (Terminal, iTerm, older Electron builds) quantise height to a row grid.
    /// Setting the size first lets the app clamp while still at the old origin; the position
    /// then lands correctly; the second size call re-applies the target now that the window is
    /// on the destination display, where a different backing scale may permit a different
    /// clamp. Windows that fully refuse still end up at the right *origin*, which is the part
    /// a user notices.
    @discardableResult
    func setCocoaFrame(_ rect: CGRect) -> Bool {
        let ax = ScreenGeometry.axRect(fromCocoa: rect)
        setAXSize(ax.size)
        let moved = setAXPosition(ax.origin)
        setAXSize(ax.size)
        return moved
    }

    // MARK: - Minimize

    var isMinimized: Bool {
        (copyValue(kAXMinimizedAttribute) as? Bool) ?? false
    }

    @discardableResult
    func setMinimized(_ minimized: Bool) -> Bool {
        AXUIElementSetAttributeValue(element,
                                     kAXMinimizedAttribute as CFString,
                                     minimized as CFBoolean) == .success
    }

    // MARK: - Eligibility

    var subrole: String? {
        copyValue(kAXSubroleAttribute) as? String
    }

    /// Whether it makes sense to snap this window.
    ///
    /// Sheets, popovers and system dialogs are attached to a parent window; moving them is
    /// visually broken. A missing subrole is treated as eligible — plenty of non-native apps
    /// simply do not publish one, and refusing them would exclude much of Electron.
    var isSnappable: Bool {
        switch subrole {
        case kAXStandardWindowSubrole, nil:
            return isSettable(kAXPositionAttribute)
        default:
            return false
        }
    }
}
