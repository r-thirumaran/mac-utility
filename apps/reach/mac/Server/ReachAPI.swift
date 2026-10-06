import AppKit

/// The agent: the LAN `HTTPServer` (PWA + `/api/*` + `/ws`, `docs/reach-api.md` §1), the
/// pairing flow, the paired sockets, the pushed events, the `info` / `reach.state` shapes and
/// the settings the local page may change. `AppDelegate` wires the callbacks that reach the
/// WKWebView bridge and the notification centre.
@MainActor
final class ReachAPI {

    static let caps = ["screen", "apps", "media", "power", "clipboard"]
    static let platform = "macos"
    static let cookieName = "mu_token"
    static let maxBody = 4 << 10

    let pairings: Pairings
    let pairer = Pairer()
    let version: String
    let agentID: String
    /// The port the server listens on this run (`reach.state.port`); a changed setting only
    /// applies after a restart, like the Go agent.
    let port: Int

    private(set) lazy var methods = ReachMethods(api: self)

    /// Events for the agent's own settings page only: `reach.pairCode`, `reach.paired`,
    /// `reach.state`, `info`, `apps.running`.
    var onLocalEvent: ((String, Any) -> Void)?
    /// Desktop notifications: `(title, body)`.
    var onNotify: ((String, String) -> Void)?
    /// The login item was toggled from the settings page.
    var onLaunchAtLoginChanged: (() -> Void)?
    /// `true` while the settings window is open, so `apps.running` pushes reach it.
    var localListening = false

    private let peers: PeerBrowser
    private var server: HTTPServer?
    private var sessions: [WSSession] = []
    private var pendingAccept: (device: String, token: String)?
    private var runningTimer: DispatchSourceTimer?
    private var lastRunning: Data?
    private var permissionTimer: DispatchSourceTimer?
    private var lastPermissions: [String: Bool] = [:]
    private let iso8601 = ISO8601DateFormatter()

    init(pairings: Pairings, version: String) {
        self.pairings = pairings
        self.version = version
        let snapshot = pairings.snapshot
        agentID = snapshot.agentId
        port = snapshot.port
        peers = PeerBrowser(selfID: snapshot.agentId)
    }

    // MARK: - Lifecycle

    func start() {
        startServer()
        peers.onChange = { [weak self] in
            guard let self else { return }
            self.emitLocal("reach.state", self.state())
        }
        peers.start()
        startRunningWatcher()
        startPermissionWatcher()
    }

    func stop() {
        runningTimer?.cancel()
        runningTimer = nil
        permissionTimer?.cancel()
        permissionTimer = nil
        peers.stop()
        closeAllSessions()
        server?.stop()
        server = nil
    }

    private func startServer() {
        let name = pairings.snapshot.name
        let txt = ["v": "1", "id": agentID, "name": name, "plat": ReachAPI.platform, "port": String(port)]
        let server = HTTPServer(port: UInt16(port),
                                bonjour: (type: PeerBrowser.serviceType, name: name, txt: txt),
                                allowLAN: true)
        server.staticRoot = Bundle.main.resourceURL?.appendingPathComponent("ui")
        server.auth = { [weak self] request in
            self?.authorize(request) ?? false
        }
        server.onWebSocket = { [weak self] socket in
            self?.accept(socket)
        }
        server.onReady = { port in
            Log.info("listening on port \(port) as \"\(name)\"", category: "net")
        }
        server.onError = { [weak self] error in
            Log.error("server failed on port \(self?.port ?? 0): \(error)", category: "net")
            self?.onNotify?("Reach", "Could not listen on port \(self?.port ?? 0). Is another Reach running?")
        }
        registerRoutes(on: server)
        do {
            try server.start()
        } catch {
            Log.error("server start failed: \(error)", category: "net")
            onNotify?("Reach", "Could not listen on port \(port): \(error.localizedDescription)")
        }
        self.server = server
    }

    /// Re-advertises under the current name (Bonjour rides on the listener). Phones
    /// reconnect by themselves.
    private func restartServer() {
        closeAllSessions()
        server?.stop()
        server = nil
        startServer()
    }

    private func closeAllSessions() {
        let open = sessions
        sessions.removeAll()
        for session in open {
            session.close()
        }
    }

    // MARK: - Routes

    private func registerRoutes(on server: HTTPServer) {
        for path in ["/api/info", "/api/peers", "/api/pair/request", "/api/pair"] {
            server.route("OPTIONS", path) { _ in
                HTTPResponse.noContent().allowingAnyOrigin().withHeader("Access-Control-Max-Age", "600")
            }
        }
        server.route("GET", "/api/info") { [weak self] _ in
            guard let self else { return HTTPResponse.internalError() }
            return ReachAPI.json(self.infoPublic())
        }
        server.route("GET", "/api/peers") { [weak self] _ in
            ReachAPI.json(self?.peers.peers ?? [])
        }
        server.route("POST", "/api/pair/request") { [weak self] request in
            self?.handlePairRequest(request) ?? HTTPResponse.internalError()
        }
        server.route("POST", "/api/pair") { [weak self] request in
            self?.handlePair(request) ?? HTTPResponse.internalError()
        }
        server.route("POST", "/api/info") { _ in ReachAPI.methodNotAllowed("GET") }
        server.route("POST", "/api/peers") { _ in ReachAPI.methodNotAllowed("GET") }
        server.route("GET", "/api/pair/request") { _ in ReachAPI.methodNotAllowed("POST") }
        server.route("GET", "/api/pair") { _ in ReachAPI.methodNotAllowed("POST") }
    }

    private static func json(_ object: Any, status: Int = 200) -> HTTPResponse {
        HTTPResponse.json(object, status: status).allowingAnyOrigin().withHeader("Cache-Control", "no-store")
    }

    private static func methodNotAllowed(_ allow: String) -> HTTPResponse {
        HTTPResponse.methodNotAllowed().withHeader("Allow", allow).allowingAnyOrigin()
    }

    private static func badRequest() -> HTTPResponse {
        json(["error": "bad_request"], status: 400)
    }

    /// The JSON object body, or `nil` when missing, oversized or not an object.
    private func body(of request: HTTPRequest) -> [String: Any]? {
        guard !request.body.isEmpty, request.body.count <= ReachAPI.maxBody else { return nil }
        return (try? JSONSerialization.jsonObject(with: request.body)) as? [String: Any]
    }

    // MARK: - Pairing endpoints

    /// `POST /api/pair/request {device}` → 204, or 403 `locked`.
    private func handlePairRequest(_ request: HTTPRequest) -> HTTPResponse {
        guard let body = body(of: request) else { return ReachAPI.badRequest() }
        let device = ReachAPI.cleanDevice(body["device"] as? String ?? "")
        guard !device.isEmpty else { return ReachAPI.badRequest() }
        guard let issued = pairer.request(device: device) else {
            return ReachAPI.json(["error": "locked", "retryAfterSec": retryAfterSec()], status: 403)
        }
        Log.info("pairing requested by \"\(device)\"", category: "pair")
        showCode(issued.code, expires: issued.expiresAt, device: device)
        return HTTPResponse.noContent().allowingAnyOrigin()
    }

    /// `POST /api/pair {device, code}` → `{token}`, 403 `bad_code` or 403 `locked`.
    private func handlePair(_ request: HTTPRequest) -> HTTPResponse {
        guard let body = body(of: request) else { return ReachAPI.badRequest() }
        let device = ReachAPI.cleanDevice(body["device"] as? String ?? "")
        guard !device.isEmpty else { return ReachAPI.badRequest() }
        let code = (body["code"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        switch pairer.redeem(code) {
        case .locked:
            Log.info("pairing locked after repeated wrong codes (device \"\(device)\")", category: "pair")
            return ReachAPI.json(["error": "locked", "retryAfterSec": retryAfterSec()], status: 403)
        case .badCode:
            Log.info("wrong pairing code from \"\(device)\"", category: "pair")
            return ReachAPI.json(["error": "bad_code"], status: 403)
        case .ok:
            let token = pairings.addPairing(device: device)
            Log.info("paired \"\(device)\"", category: "pair")
            emitLocal("reach.paired", ["device": device])
            emitLocal("reach.state", state())
            onNotify?("Reach", "\(device) is now paired")
            return ReachAPI.json(["token": token])
        }
    }

    private func retryAfterSec() -> Int {
        guard let until = pairer.lockedUntilDate else { return 0 }
        return max(0, Int(until.timeIntervalSinceNow.rounded(.up)))
    }

    /// Trims, strips control characters and bounds a device name.
    static func cleanDevice(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        var scalars = String.UnicodeScalarView()
        for scalar in trimmed.unicodeScalars where scalar.value >= 0x20 && scalar.value != 0x7F {
            scalars.append(scalar)
        }
        let cleaned = String(scalars)
        return cleaned.count > Pairings.maxDeviceName ? String(cleaned.prefix(Pairings.maxDeviceName)) : cleaned
    }

    // MARK: - Pairing code (local)

    /// "Show pairing code" from the menu or the settings page: the outstanding code while it
    /// is valid, otherwise a fresh one; shown as a notification and as `reach.pairCode`.
    func showCodeLocally() throws -> [String: Any] {
        guard let issued = pairer.request(device: "") else {
            throw ReachError.notAuthorized("pairing is locked for \(retryAfterSec()) s after repeated wrong codes")
        }
        showCode(issued.code, expires: issued.expiresAt, device: "")
        return ["code": issued.code, "expiresAt": iso8601.string(from: issued.expiresAt)]
    }

    /// Re-sends the valid code (if any) to a settings page that has just loaded.
    func resendOutstandingCode() {
        guard let issued = pairer.outstanding else { return }
        emitLocal("reach.pairCode", ["code": issued.code, "expiresAt": iso8601.string(from: issued.expiresAt), "device": ""])
    }

    private func showCode(_ code: String, expires: Date, device: String) {
        let body = device.isEmpty
            ? "Pairing code \(code) — valid for 2 minutes"
            : "\(device): pairing code \(code) — valid for 2 minutes"
        onNotify?("Reach", body)
        emitLocal("reach.pairCode", ["code": code, "expiresAt": iso8601.string(from: expires), "device": device])
    }

    // MARK: - Auth and sockets

    /// The server's `auth`: the PWA and `/api/*` are open; `/ws` needs a pairing token in
    /// `?token=` or the `mu_token` cookie (401 otherwise). The matched pairing is parked for
    /// `accept`, which the server calls right after, still on the main actor.
    private func authorize(_ request: HTTPRequest) -> Bool {
        guard request.path == "/ws" else { return true }
        pendingAccept = nil
        let token = ReachAPI.token(of: request)
        guard let pairing = pairings.find(token: token) else { return false }
        pairings.touch(token: token)
        if request.isWebSocketUpgrade {
            pendingAccept = (pairing.device, token)
        }
        return true
    }

    /// `?token=` or the `mu_token` cookie.
    static func token(of request: HTTPRequest) -> String {
        if let token = request.query["token"], !token.isEmpty {
            return token
        }
        guard let cookies = request.header("cookie") else { return "" }
        let prefix = cookieName + "="
        for part in cookies.split(separator: ";") {
            let pair = part.trimmingCharacters(in: .whitespaces)
            if pair.hasPrefix(prefix) {
                return String(pair.dropFirst(prefix.count))
            }
        }
        return ""
    }

    private func accept(_ socket: WebSocketConnection) {
        guard let accepted = pendingAccept else {
            socket.close(code: 1008, reason: "unauthorized")
            return
        }
        pendingAccept = nil
        let session = WSSession(socket: socket, device: accepted.device, token: accepted.token, api: self)
        sessions.append(session)
        Log.info("ws: \"\(accepted.device)\" connected from \(socket.remoteDescription)", category: "net")
        session.start()
    }

    func sessionClosed(_ session: WSSession) {
        guard sessions.contains(where: { $0 === session }) else { return }
        sessions.removeAll { $0 === session }
        Log.info("ws: \"\(session.device)\" disconnected", category: "net")
    }

    /// Drops the sockets of revoked pairings.
    private func closeSessions(tokens: [String]) {
        let victims = sessions.filter { tokens.contains($0.token) }
        sessions.removeAll { tokens.contains($0.token) }
        for session in victims {
            session.close()
        }
    }

    // MARK: - Events

    /// Pushes `{event, data}` to every paired phone.
    func broadcast(event: String, data: Any) {
        for session in sessions {
            session.send(event: event, data: data)
        }
    }

    /// Pushes an event to the agent's own settings page only.
    func emitLocal(_ event: String, _ data: Any) {
        onLocalEvent?(event, data)
    }

    // MARK: - Shapes

    var permissions: [String: Bool] {
        [
            "accessibility": Permissions.accessibilityGranted,
            "screenRecording": Permissions.screenRecordingGranted,
        ]
    }

    static var hostname: String {
        Host.current().localizedName ?? ProcessInfo.processInfo.hostName
    }

    /// `GET /api/info`.
    func infoPublic() -> [String: Any] {
        [
            "id": agentID,
            "name": pairings.snapshot.name,
            "platform": ReachAPI.platform,
            "version": version,
            "pairingRequired": true,
            "caps": ReachAPI.caps,
        ]
    }

    /// The `info` method and event.
    func infoFull() -> [String: Any] {
        var info = infoPublic()
        info["hostname"] = ReachAPI.hostname
        info["user"] = NSUserName()
        info["uptimeSec"] = Int(ProcessInfo.processInfo.systemUptime)
        info["permissions"] = permissions
        info["port"] = port
        return info
    }

    /// `reach.state`.
    func state() -> [String: Any] {
        let snapshot = pairings.snapshot
        return [
            "name": snapshot.name,
            "port": port,
            "configuredPort": snapshot.port,
            "agentId": agentID,
            "platform": ReachAPI.platform,
            "version": version,
            "hostname": ReachAPI.hostname,
            "fake": false,
            "pairings": pairings.publicPairings,
            "permissions": permissions,
            "peers": peers.peers,
            "launchAtLogin": snapshot.launchAtLogin,
            "urls": LANAddresses.urls(port: port),
        ]
    }

    // MARK: - Settings (local)

    /// `reach.settings.set {name?, port?, launchAtLogin?}` → the state plus `restartRequired`.
    func applySettings(_ params: [String: Any]) throws -> [String: Any] {
        var restartRequired = false
        var renamed = false
        if let raw = params["name"] as? String {
            let name = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !name.isEmpty, name.count <= Pairings.maxDeviceName else {
                throw ReachError.invalidParams("name must be 1–\(Pairings.maxDeviceName) characters")
            }
            if name != pairings.snapshot.name {
                pairings.update { $0.name = name }
                renamed = true
            }
        }
        if let raw = params["port"] {
            guard let newPort = (raw as? NSNumber)?.intValue, newPort >= 1, newPort <= 65535 else {
                throw ReachError.invalidParams("port must be 1–65535")
            }
            pairings.update { $0.port = newPort }
            restartRequired = newPort != port
        }
        if let raw = params["launchAtLogin"] {
            guard let enabled = raw as? Bool else {
                throw ReachError.invalidParams("launchAtLogin must be a boolean")
            }
            try setLaunchAtLogin(enabled)
        }
        if renamed {
            restartServer()
        }
        var result = state()
        result["restartRequired"] = restartRequired
        return result
    }

    /// Registers or removes the login item and remembers the choice.
    func setLaunchAtLogin(_ enabled: Bool) throws {
        do {
            try LaunchAtLogin.setEnabled(enabled)
        } catch {
            throw ReachError.internalError("launch at login: \(error.localizedDescription)")
        }
        pairings.update { $0.launchAtLogin = enabled }
        if enabled, LaunchAtLogin.requiresApproval {
            LaunchAtLogin.openLoginItemsSettings()
        }
        onLaunchAtLoginChanged?()
    }

    /// `reach.pairings.revoke {device}`: forgets the device and drops its sockets.
    func revoke(device: String) throws -> [String: Any] {
        let tokens = pairings.revoke(device: device)
        guard !tokens.isEmpty else {
            throw ReachError.notFound("no pairing for \"\(device)\"")
        }
        closeSessions(tokens: tokens)
        Log.info("revoked pairing of \"\(device)\"", category: "pair")
        return ["pairings": pairings.publicPairings]
    }

    // MARK: - Watchers

    /// Pushes `apps.running` when the list changes, at most once a second, only while
    /// someone is listening.
    private func startRunningWatcher() {
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 1, repeating: 1.0, leeway: .milliseconds(100))
        timer.setEventHandler { [weak self] in
            MainActor.assumeIsolated {
                self?.pushRunningIfChanged()
            }
        }
        timer.resume()
        runningTimer = timer
    }

    private func pushRunningIfChanged() {
        guard !sessions.isEmpty || localListening else {
            lastRunning = nil
            return
        }
        let list = methods.apps.running()
        guard let encoded = try? JSONSerialization.data(withJSONObject: list, options: [.sortedKeys]),
              encoded != lastRunning else { return }
        lastRunning = encoded
        broadcast(event: "apps.running", data: list)
        if localListening {
            emitLocal("apps.running", list)
        }
    }

    /// There is no notification for a TCC grant, so the switches are polled and `info` is
    /// pushed when one flips (`docs/reach-api.md` §3).
    private func startPermissionWatcher() {
        lastPermissions = permissions
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 3, repeating: 3.0, leeway: .milliseconds(500))
        timer.setEventHandler { [weak self] in
            MainActor.assumeIsolated {
                self?.checkPermissions()
            }
        }
        timer.resume()
        permissionTimer = timer
    }

    private func checkPermissions() {
        let current = permissions
        guard current != lastPermissions else { return }
        lastPermissions = current
        Log.info("permissions changed: \(current)", category: "app")
        let info = infoFull()
        broadcast(event: "info", data: info)
        emitLocal("info", info)
        emitLocal("reach.state", state())
    }
}
