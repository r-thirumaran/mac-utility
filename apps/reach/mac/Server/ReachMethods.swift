import AppKit

/// Every method of `docs/reach-api.md` §2 and §5 with the parameter rules, result shapes and
/// error codes of the Go agent's `bridge.go`, served to phones through `WSSession` and to the
/// agent's own settings page through `ReachBridge`. The `reach.*` methods answer
/// `not_authorized` unless the call is local.
///
/// Fast handlers answer on the main actor; screen captures, the app scan and `pmset` run on
/// background queues and complete asynchronously. `completion` may be called on any queue.
@MainActor
final class ReachMethods {

    static let remoteMethods: [String] = [
        "info", "ping",
        "screen.displays", "screen.frame", "screen.stream.start", "screen.stream.stop",
        "mouse.move", "mouse.moveTo", "mouse.click", "mouse.down", "mouse.up", "mouse.scroll",
        "key.text", "key.press", "key.down", "key.up",
        "media", "volume.get", "volume.set",
        "apps.list", "apps.running", "apps.launch", "apps.focus", "apps.quit",
        "windows.list", "windows.focus",
        "power", "clipboard.get", "clipboard.set", "open.url",
    ]

    static let localMethods: [String] = [
        "reach.state", "reach.settings.set", "reach.pairings.list", "reach.pairings.revoke", "reach.pair.showCode",
    ]

    static var allMethods: [String] { remoteMethods + localMethods }

    private static let empty: [String: Any] = [:]

    let apps = AppsController()

    private unowned let api: ReachAPI
    private let input: InputInjector
    private let captureQueue = DispatchQueue(label: "reach.capture", qos: .userInitiated)
    private let workQueue = DispatchQueue(label: "reach.work", qos: .userInitiated)

    init(api: ReachAPI) {
        self.api = api
        input = InputInjector(keys: KeyTable.shared)
    }

    // MARK: - Dispatch

    func call(_ method: String, params: [String: Any], session: WSSession?, local: Bool,
              completion: @escaping (Result<Any, Error>) -> Void) {
        if method.hasPrefix("reach."), !local {
            completion(.failure(ReachError.notAuthorized("\(method) is only available to the agent's own settings page")))
            return
        }
        do {
            try dispatch(method, params: params, session: session, completion: completion)
        } catch {
            completion(.failure(error))
        }
    }

    // swiftlint:disable:next cyclomatic_complexity function_body_length
    private func dispatch(_ method: String, params: [String: Any], session: WSSession?,
                          completion: @escaping (Result<Any, Error>) -> Void) throws {
        switch method {
        case "info":
            completion(.success(api.infoFull()))
        case "ping":
            let pong: [String: Any] = ["t": Int64(Date().timeIntervalSince1970 * 1000)]
            completion(.success(pong))

        // screen
        case "screen.displays":
            completion(.success(ScreenCapture.displayRows()))
        case "screen.frame":
            let stream = ReachMethods.streamParams(params)
            _ = try ScreenCapture.display(at: stream.display)
            try ScreenCapture.ensurePermission()
            captureQueue.async {
                do {
                    let frame = try ScreenCapture.capture(index: stream.display, maxWidth: stream.maxWidth, quality: stream.quality)
                    let result: [String: Any] = [
                        "display": stream.display, "w": frame.width, "h": frame.height,
                        "jpegB64": frame.jpeg.base64EncodedString(),
                    ]
                    completion(.success(result))
                } catch {
                    completion(.failure(error))
                }
            }
        case "screen.stream.start":
            guard let session else {
                throw ReachError.notSupported("screen streams need the WebSocket transport")
            }
            let stream = ReachMethods.streamParams(params)
            _ = try ScreenCapture.display(at: stream.display)
            try ScreenCapture.ensurePermission()
            session.startStream(display: stream.display, maxWidth: stream.maxWidth, quality: stream.quality, fps: stream.fps)
            Log.debug("stream: started display \(stream.display) ≤\(stream.maxWidth)px q\(stream.quality) \(stream.fps)fps", category: "screen")
            completion(.success(ReachMethods.empty))
        case "screen.stream.stop":
            session?.stopStream()
            completion(.success(ReachMethods.empty))

        // mouse
        case "mouse.move":
            try input.move(dx: ReachMethods.number(params, "dx") ?? 0, dy: ReachMethods.number(params, "dy") ?? 0)
            completion(.success(ReachMethods.empty))
        case "mouse.moveTo":
            guard let x = ReachMethods.number(params, "x"), let y = ReachMethods.number(params, "y") else {
                throw ReachError.invalidParams("x and y are required")
            }
            try input.moveTo(display: ReachMethods.int(params, "display") ?? 0, x: x, y: y)
            completion(.success(ReachMethods.empty))
        case "mouse.click":
            let button = ReachMethods.string(params, "button") ?? "left"
            let count = ReachMethods.int(params, "count") ?? 1
            guard count == 1 || count == 2 else {
                throw ReachError.invalidParams("count must be 1 or 2")
            }
            var target: CGPoint?
            if let x = ReachMethods.number(params, "x"), let y = ReachMethods.number(params, "y") {
                target = try input.moveTo(display: ReachMethods.int(params, "display") ?? 0, x: x, y: y)
            }
            try input.click(button: button, count: count, at: target)
            completion(.success(ReachMethods.empty))
        case "mouse.down", "mouse.up":
            try input.button(ReachMethods.string(params, "button") ?? "left", down: method == "mouse.down")
            completion(.success(ReachMethods.empty))
        case "mouse.scroll":
            try input.scroll(dx: ReachMethods.number(params, "dx") ?? 0, dy: ReachMethods.number(params, "dy") ?? 0)
            completion(.success(ReachMethods.empty))

        // keyboard
        case "key.text":
            let text = ReachMethods.string(params, "text") ?? ""
            if !text.isEmpty {
                try input.typeText(text)
            }
            completion(.success(ReachMethods.empty))
        case "key.press":
            let mods = try ReachMethods.mods(params)
            try input.key(name: ReachMethods.string(params, "key") ?? "", mods: mods, down: true, up: true)
            completion(.success(ReachMethods.empty))
        case "key.down", "key.up":
            try input.key(name: ReachMethods.string(params, "key") ?? "", mods: [],
                          down: method == "key.down", up: method == "key.up")
            completion(.success(ReachMethods.empty))

        // media & volume
        case "media":
            try MediaControl.perform(action: ReachMethods.string(params, "action") ?? "")
            completion(.success(ReachMethods.empty))
        case "volume.get":
            let volume = try MediaControl.volume()
            let result: [String: Any] = ["level": volume.level, "muted": volume.muted]
            completion(.success(result))
        case "volume.set":
            guard let level = ReachMethods.int(params, "level"), level >= 0, level <= 100 else {
                throw ReachError.invalidParams("level must be 0..100")
            }
            try MediaControl.setVolume(level)
            completion(.success(ReachMethods.empty))

        // apps & windows
        case "apps.list":
            let icons = ReachMethods.bool(params, "icons") ?? false
            let apps = self.apps
            workQueue.async {
                completion(.success(apps.installed(icons: icons)))
            }
        case "apps.running":
            completion(.success(apps.running()))
        case "apps.launch":
            let id = ReachMethods.string(params, "id") ?? ""
            guard !id.isEmpty else { throw ReachError.invalidParams("id is required") }
            apps.launch(id: id) { error in
                if let error {
                    completion(.failure(error))
                } else {
                    completion(.success(ReachMethods.empty))
                }
            }
        case "apps.focus":
            let id = ReachMethods.string(params, "id") ?? ""
            let pid = ReachMethods.int(params, "pid") ?? 0
            guard !id.isEmpty || pid != 0 else { throw ReachError.invalidParams("id or pid is required") }
            try apps.focus(id: id, pid: pid)
            completion(.success(ReachMethods.empty))
        case "apps.quit":
            let pid = ReachMethods.int(params, "pid") ?? 0
            guard pid > 0, pid <= Int(Int32.max) else { throw ReachError.invalidParams("pid is required") }
            try apps.quit(pid: pid, force: ReachMethods.bool(params, "force") ?? false)
            completion(.success(ReachMethods.empty))
        case "windows.list":
            completion(.success(WindowControl.list()))
        case "windows.focus":
            let id = ReachMethods.string(params, "id") ?? ""
            guard !id.isEmpty else { throw ReachError.invalidParams("id is required") }
            try WindowControl.focus(id: id)
            completion(.success(ReachMethods.empty))

        // power, clipboard, URLs
        case "power":
            let action = ReachMethods.string(params, "action") ?? ""
            guard PowerControl.actions.contains(action) else {
                throw ReachError.invalidParams("unknown power action \"\(action)\"")
            }
            Log.info("power: \(action)", category: "power")
            PowerControl.perform(action) { error in
                if let error {
                    completion(.failure(error))
                } else {
                    completion(.success(ReachMethods.empty))
                }
            }
        case "clipboard.get":
            let result: [String: Any] = ["text": ClipboardAccess.text()]
            completion(.success(result))
        case "clipboard.set":
            try ClipboardAccess.setText(ReachMethods.string(params, "text") ?? "")
            completion(.success(ReachMethods.empty))
        case "open.url":
            try URLOpener.open(ReachMethods.string(params, "url") ?? "")
            completion(.success(ReachMethods.empty))

        // local settings page (docs/reach-api.md §5)
        case "reach.state":
            completion(.success(api.state()))
        case "reach.settings.set":
            let state = try api.applySettings(params)
            completion(.success(state))
        case "reach.pairings.list":
            completion(.success(api.pairings.publicPairings))
        case "reach.pairings.revoke":
            let remaining = try api.revoke(device: ReachMethods.string(params, "device") ?? "")
            completion(.success(remaining))
        case "reach.pair.showCode":
            let issued = try api.showCodeLocally()
            completion(.success(issued))

        default:
            throw ReachError.notFound("unknown method \"\(method)\"")
        }
    }

    // MARK: - Parameters

    private static func number(_ params: [String: Any], _ key: String) -> Double? {
        if let value = params[key] as? NSNumber { return value.doubleValue }
        if let value = params[key] as? Double { return value }
        if let value = params[key] as? Int { return Double(value) }
        return nil
    }

    private static func int(_ params: [String: Any], _ key: String) -> Int? {
        if let value = params[key] as? NSNumber { return value.intValue }
        if let value = params[key] as? Int { return value }
        if let value = params[key] as? Double { return Int(value) }
        return nil
    }

    private static func bool(_ params: [String: Any], _ key: String) -> Bool? {
        if let value = params[key] as? Bool { return value }
        if let value = params[key] as? NSNumber { return value.boolValue }
        return nil
    }

    private static func string(_ params: [String: Any], _ key: String) -> String? {
        params[key] as? String
    }

    /// `mods` as an array of names (absent → none; anything else → `invalid_params`).
    private static func mods(_ params: [String: Any]) throws -> [String] {
        guard let raw = params["mods"], !(raw is NSNull) else { return [] }
        guard let mods = raw as? [String] else {
            throw ReachError.invalidParams("mods must be an array of modifier names")
        }
        for mod in mods where KeyTable.modifierFlags[mod] == nil {
            throw ReachError.invalidParams("unknown modifier \"\(mod)\"")
        }
        return mods
    }

    private static func streamParams(_ params: [String: Any]) -> (display: Int, maxWidth: Int, quality: Double, fps: Int) {
        let normalized = ScreenCapture.normalize(maxWidth: int(params, "maxWidth") ?? 0,
                                                 quality: number(params, "quality") ?? 0,
                                                 fps: int(params, "fps") ?? 0)
        return (int(params, "display") ?? 0, normalized.maxWidth, normalized.quality, normalized.fps)
    }
}
