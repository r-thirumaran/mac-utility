import AppKit
import Carbon.HIToolbox

/// The things a hot key can trigger.
enum HotKeyAction: String, CaseIterable, Codable {
    case snapLeft
    case snapRight
    case maximize
    case restoreOrMinimize
    case showClipboard

    /// Secondary bindings installed only when "Cmd+Arrow mode" is enabled. They map onto the
    /// same behaviour as `.snapLeft` / `.snapRight` but need their own hot key IDs.
    case cmdArrowLeft
    case cmdArrowRight

    var title: String {
        switch self {
        case .snapLeft:          return "Snap Left Half"
        case .snapRight:         return "Snap Right Half"
        case .maximize:          return "Maximize"
        case .restoreOrMinimize: return "Restore / Minimize"
        case .showClipboard:     return "Clipboard History"
        case .cmdArrowLeft:      return "Snap Left (⌘←)"
        case .cmdArrowRight:     return "Snap Right (⌘→)"
        }
    }
}

/// Wraps Carbon's `RegisterEventHotKey`.
///
/// Why Carbon and not a `CGEventTap` or `NSEvent` global monitor:
///   * `RegisterEventHotKey` genuinely *consumes* the keystroke, so ⌃⌘← never leaks through
///     to the focused app. Global `NSEvent` monitors are observe-only.
///   * It needs no Accessibility permission of its own, so hot keys keep working even while
///     the user is still in System Settings granting us the window-management permission.
///
/// The Carbon callback is a bare C function pointer and cannot capture context, so dispatch
/// goes through the shared singleton. That is the whole reason this type is a singleton.
final class HotKeyManager {

    static let shared = HotKeyManager()

    /// Four-char code identifying our hot keys to Carbon: 'SNAP'.
    private let signature: OSType = 0x534E4150

    private var registered: [UInt32: EventHotKeyRef] = [:]
    private var handlers: [UInt32: () -> Void] = [:]
    private var eventHandler: EventHandlerRef?

    /// Retained so `resume()` can put the exact same set back after recording.
    private var lastBindings: [HotKeyAction: KeyCombo] = [:]
    private var lastHandler: ((HotKeyAction) -> Void)?

    private init() {}

    // MARK: - Event handler installation

    /// Installs the single application-level Carbon handler. Idempotent.
    private func installHandlerIfNeeded() {
        guard eventHandler == nil else { return }

        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                                 eventKind: UInt32(kEventHotKeyPressed))

        let status = InstallEventHandler(
            GetApplicationEventTarget(),
            { _, event, _ -> OSStatus in
                guard let event else { return OSStatus(eventNotHandledErr) }
                var hotKeyID = EventHotKeyID()
                let err = GetEventParameter(event,
                                            EventParamName(kEventParamDirectObject),
                                            EventParamType(typeEventHotKeyID),
                                            nil,
                                            MemoryLayout<EventHotKeyID>.size,
                                            nil,
                                            &hotKeyID)
                guard err == noErr else { return err }
                // Hop off the Carbon callback before touching AppKit / the AX API.
                DispatchQueue.main.async {
                    HotKeyManager.shared.fire(id: hotKeyID.id)
                }
                return noErr
            },
            1, &spec, nil, &eventHandler)

        if status != noErr {
            NSLog("Clipwell: InstallEventHandler failed (\(status))")
        }
    }

    private func fire(id: UInt32) {
        handlers[id]?()
    }

    // MARK: - Registration

    /// Replaces the entire hot key set. Called at launch and whenever preferences change —
    /// re-registering wholesale is simpler and cheaper than diffing, and there are only a
    /// handful of keys.
    ///
    /// - Returns: the actions that could **not** be registered, almost always because
    ///   another app (or macOS itself) already owns that combination.
    @discardableResult
    func register(_ bindings: [HotKeyAction: KeyCombo],
                  handler: @escaping (HotKeyAction) -> Void) -> [HotKeyAction] {
        installHandlerIfNeeded()
        unregisterAll()

        lastBindings = bindings
        lastHandler = handler

        var failures: [HotKeyAction] = []

        for (action, combo) in bindings {
            guard combo.isValid else {
                failures.append(action)
                continue
            }
            // Hot key IDs must be unique per registration; the action's position in the
            // enum gives us a stable, collision-free integer.
            guard let index = HotKeyAction.allCases.firstIndex(of: action) else { continue }
            let id = UInt32(index + 1)

            var ref: EventHotKeyRef?
            let hotKeyID = EventHotKeyID(signature: signature, id: id)
            let status = RegisterEventHotKey(combo.keyCode,
                                             combo.carbonModifiers,
                                             hotKeyID,
                                             GetApplicationEventTarget(),
                                             0,
                                             &ref)

            if status == noErr, let ref {
                registered[id] = ref
                handlers[id] = { handler(action) }
            } else {
                NSLog("Clipwell: could not register \(action.rawValue) "
                      + "(\(combo.displayString)) — status \(status)")
                failures.append(action)
            }
        }

        return failures
    }

    func unregisterAll() {
        for (_, ref) in registered {
            UnregisterEventHotKey(ref)
        }
        registered.removeAll()
        handlers.removeAll()
    }

    /// Temporarily releases every hot key.
    ///
    /// Essential while a shortcut recorder is open: `RegisterEventHotKey` *consumes* the
    /// keystroke system-wide, so trying to record ⌘⇧V while ⌘⇧V is registered would swallow
    /// the very combination the user is pressing and the recorder would never see it.
    func suspend() {
        for (_, ref) in registered {
            UnregisterEventHotKey(ref)
        }
        registered.removeAll()
        handlers.removeAll()
    }

    func resume() {
        guard let handler = lastHandler, !lastBindings.isEmpty else { return }
        register(lastBindings, handler: handler)
    }
}
