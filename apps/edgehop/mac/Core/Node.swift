import Foundation

/// The Edgehop state machine, a line-for-line mirror of `core/node.go`: it owns `active`, the
/// switch timer, the forwarding of physical input and the injection of remote input, the
/// hotkeys, the clipboard hand-over and peer liveness, and produces the state the UI shows.
///
/// Everything runs on the main actor: the event tap delivers on the main run loop, the
/// transport hops its callbacks over, and injection/sends never re-enter synchronously (posted
/// events come back through the tap on a later run-loop turn, sends are queued), so no lock
/// and no deferred-action list are needed.
@MainActor
final class Node {

    /// Liveness (`docs/edgehop-protocol.md` §6 ping/pong) and the input acceptance window after
    /// an `active` naming this machine (§7).
    static let pingInterval: TimeInterval = 5
    static let peerTimeout: TimeInterval = 15
    static let activeGrace: TimeInterval = 0.25
    static let receivingWindow: TimeInterval = 2
    static let tickInterval: TimeInterval = 1

    /// HID usages of the hotkey keys.
    private static let usageRight = 0x4F
    private static let usageLeft = 0x50
    private static let usageDown = 0x51
    private static let usageUp = 0x52
    private static let usageHome = 0x4A

    /// Modifier families (either side) for the hotkey combination.
    private static let famCtrl: UInt8 = 0x11
    private static let famShift: UInt8 = 0x22
    private static let famAlt: UInt8 = 0x44
    private static let famGUI: UInt8 = 0x88

    struct Config {
        var id: String
        var name: String
        var plat: String
        var version: String
        var store: PeerStore
        var device: InputDevice?
        var sender: FrameSender?
        var clipboard: TextClipboard?
        /// Reported verbatim in the state (the macOS permission badges).
        var permissions: () -> [String: Bool]
        /// Spec §6: flashes the locate ring at this machine's cursor.
        var showLocate: (() -> Void)?
    }

    /// What the node knows about one peer beyond the store.
    private final class PeerRecord {
        let id: String
        var name = ""
        var plat = ""
        var version = ""
        var online = false
        var discovered = false
        var addr = ""
        var pk = ""
        var displays: [Display] = []
        var lastSeen = Date.distantPast
        var lastPing = Date.distantPast
        var secureInput = false

        init(id: String) {
            self.id = id
        }
    }

    /// Fired after every state change; the bridge emits `edgehop.state` and the menu refreshes.
    var onStateChanged: (() -> Void)?
    /// Every log line (also written to the unified log).
    var onLog: ((LogEntry) -> Void)?

    let id: String
    private(set) var name: String
    let plat: String
    private let version: String
    private let config: Config

    private var peers: [String: PeerRecord] = [:]
    private var displays: [Display] = []
    private(set) var layout: Layout
    private(set) var settings: EdgehopSettings
    private var hotkey: [UInt8] = []

    private(set) var active: String
    private var seq: UInt32 = 0
    /// Physical buttons held (bit = button number).
    private var buttons: UInt8 = 0
    /// Physical modifiers held (HID byte).
    private var mods: UInt8 = 0
    private var edgePending: Edge?
    private var edgeSince = Date.distantPast
    private var edgeTimer: DispatchWorkItem?
    private var lastNamedActive = Date.distantPast
    private var lastRemoteInput: Date?
    private var forwardedToActive = false
    private var lastHandoverFrom = ""
    private var lastRole = ""
    private var port = 0
    private var secureInputOn = false

    private var ticker: DispatchSourceTimer?
    private var started = false
    private var shake = ShakeDetector()

    // MARK: - Lifecycle

    init(config: Config) {
        self.config = config
        id = config.id
        name = config.name
        plat = config.plat
        version = config.version
        active = config.id
        layout = config.store.layout
        settings = config.store.settings.normalized()
        hotkey = Node.parseHotkey(settings.hotkeySwitch)
        if let device = config.device {
            displays = device.displays()
        }
        for p in config.store.peers {
            let record = PeerRecord(id: p.id)
            record.name = p.name
            record.plat = p.plat
            record.addr = p.lastAddr
            record.pk = p.staticPub
            record.displays = p.displays
            peers[p.id] = record
        }
        // Spec §4: a config written before layout v2 carries only `tiles`; convert it once so
        // everything below works on the canvas, and write the placements back.
        let upgraded = layout.upgraded(displays: displaysByMachine())
        if upgraded != layout.normalized() {
            try? config.store.setLayout(upgraded)
        }
        layout = upgraded
    }

    /// Records the port the transport actually listens on (for the state and `info`).
    func setPort(_ port: Int) {
        self.port = port
    }

    /// Installs the capture callback, watches display changes and starts the liveness ticker.
    func start() {
        guard !started else { return }
        started = true
        if let device = config.device {
            device.onDisplaysChanged { [weak self] in self?.displaysChanged() }
            device.start { [weak self] event in self?.onLocalInput(event) ?? false }
        }
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + Node.tickInterval, repeating: Node.tickInterval, leeway: .milliseconds(100))
        timer.setEventHandler { [weak self] in
            MainActor.assumeIsolated { self?.tick() }
        }
        timer.resume()
        ticker = timer
        log("info", "Edgehop \(version) ready as \"\(name)\" (\(Node.short(id)))")
        emitState()
    }

    /// Ends capture and the ticker; releases the cursor.
    func stop() {
        guard started else { return }
        started = false
        edgeTimer?.cancel()
        edgeTimer = nil
        ticker?.cancel()
        ticker = nil
        if let device = config.device {
            device.lockCursor(false, centre: Geometry.centre(displays))
            device.stop()
        }
    }

    // MARK: - Helpers

    private func log(_ level: String, _ message: String) {
        switch level {
        case "warn", "error": Log.error(message, category: "node")
        case "debug": Log.debug(message, category: "node")
        default: Log.info(message, category: "node")
        }
        onLog?(LogEntry(level: level, msg: message))
    }

    private static func short(_ id: String) -> String {
        String(id.prefix(8))
    }

    private func send<T: Encodable>(_ message: T, to peerID: String) {
        guard let frame = Wire.control(message) else { return }
        send(frame, to: peerID)
    }

    private func send(_ frame: Data, to peerID: String) {
        guard let sender = config.sender else { return }
        if !sender.send(frame, to: peerID) {
            log("debug", "send to \(Node.short(peerID)) failed: not connected or queue full")
        }
    }

    private func broadcast<T: Encodable>(_ message: T) {
        guard let frame = Wire.control(message) else { return }
        config.sender?.broadcast(frame)
    }

    private func peerName(_ id: String) -> String {
        if isSelf(id) { return name }
        if let p = peers[id], !p.name.isEmpty { return p.name }
        return Node.short(id)
    }

    private func ensurePeer(_ id: String) -> PeerRecord {
        if let p = peers[id] { return p }
        let p = PeerRecord(id: id)
        peers[id] = p
        return p
    }

    private func isSelf(_ id: String) -> Bool {
        id == self.id
    }

    private func peerOnline(_ id: String) -> Bool {
        peers[id]?.online ?? false
    }

    /// Every machine's displays: this one live, each peer's live while it is online and its
    /// last known list otherwise (spec §4).
    private func displaysByMachine() -> [String: [Display]] {
        var out: [String: [Display]] = [id: displays]
        for (peerID, p) in peers where !p.displays.isEmpty {
            out[peerID] = p.displays
        }
        return out
    }

    /// The shared canvas of spec §1, rebuilt from the current layout and display lists.
    private func world() -> Geometry.World {
        Geometry.World(layout: layout, displays: displaysByMachine())
    }

    private func role() -> String {
        if !isSelf(active) { return "sending" }
        if let last = lastRemoteInput, Date().timeIntervalSince(last) < Node.receivingWindow {
            return "receiving"
        }
        return "idle"
    }

    // MARK: - State

    /// The `edgehop.state` payload (core part).
    func state() -> NodeState {
        let me = Machine(id: id, name: name, plat: plat, version: version, status: "self",
                         paired: true, active: isSelf(active), displays: displays)
        var paired: [String: StoredPeer] = [:]
        for p in config.store.peers {
            paired[p.id] = p
        }
        var list: [Machine] = []
        for (peerID, p) in peers {
            let stored = paired[peerID]
            let isPaired = stored != nil
            if !isPaired && !(p.discovered && !p.addr.isEmpty) { continue }
            var m = Machine(id: peerID, name: p.name, plat: p.plat, version: p.version, status: "",
                            paired: isPaired, active: active == peerID, addr: p.addr,
                            displays: p.displays, secureInput: p.secureInput)
            if m.name.isEmpty { m.name = stored?.name ?? "" }
            if m.plat.isEmpty { m.plat = stored?.plat ?? "" }
            if m.addr.isEmpty { m.addr = stored?.lastAddr ?? "" }
            if p.online {
                m.status = "online"
            } else if isPaired {
                m.status = "offline"
            } else {
                m.status = "discovered"
            }
            if p.lastSeen != Date.distantPast {
                m.lastSeen = Int64(p.lastSeen.timeIntervalSince1970 * 1000)
            }
            list.append(m)
        }
        for (peerID, p) in paired where peers[peerID] == nil { // paired but never seen since start
            list.append(Machine(id: peerID, name: p.name, plat: p.plat, status: "offline",
                                paired: true, addr: p.lastAddr))
        }
        list.sort { a, b in
            if a.name != b.name { return a.name < b.name }
            return a.id < b.id
        }
        return NodeState(selfMachine: me, peers: list, layout: layout, active: active,
                         role: role(), permissions: config.permissions(), settings: settings, port: port)
    }

    private func emitState() {
        lastRole = role()
        onStateChanged?()
    }

    /// Stores and applies new settings. A changed port takes effect at the next start.
    func setSettings(_ newSettings: EdgehopSettings) {
        let s = newSettings.normalized()
        config.store.setSettings(s)
        settings = s
        hotkey = Node.parseHotkey(s.hotkeySwitch)
        if !isSelf(active), let device = config.device {
            device.lockCursor(s.lockCursorWhileRemote, centre: Geometry.centre(displays))
        }
        emitState()
    }

    /// Renames this machine and tells the peers.
    func setName(_ newName: String) throws {
        let trimmed = newName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw BridgeError.invalidParams("empty name") }
        config.store.setName(trimmed)
        name = trimmed
        broadcast(infoMessage(Wire.tInfo))
        emitState()
    }

    /// Validates (spec §1), stamps, stores and broadcasts a layout edited here. A legacy
    /// `{tiles}` layout is converted to the canvas first (spec §4).
    func setLayout(_ edited: Layout) throws {
        try edited.validate()
        let known = displaysByMachine()
        var l = edited.upgraded(displays: known)
        try l.validateOverlaps(displays: known)
        let now = Int64(Date().timeIntervalSince1970 * 1000)
        if l.updatedAt <= layout.updatedAt || l.updatedAt == 0 {
            l.updatedAt = max(now, layout.updatedAt + 1)
        }
        try config.store.setLayout(l)
        layout = l
        broadcast(Wire.LayoutMessage(layout: l))
        log("info", "layout saved (\(l.machines.count) machines on the canvas)")
        emitState()
    }

    private func infoMessage(_ t: String) -> Wire.Info {
        Wire.Info(t: t, name: name, plat: plat, version: version, port: port > 0 ? port : nil, displays: displays)
    }

    private func displaysChanged() {
        guard let device = config.device else { return }
        displays = device.displays()
        broadcast(Wire.Info(t: Wire.tDisplays, displays: displays))
        // Spec §2: the boundaries are derived from the layout plus the display lists, so the
        // derived v1 tiles are refreshed here too (the placements themselves do not move).
        layout = layout.upgraded(displays: displaysByMachine())
        log("info", "displays changed: \(displays.count)")
        emitState()
    }

    // MARK: - Transport callbacks

    /// A connection to `peer` is authenticated: send `info` and the layout.
    func peerUp(_ peer: PeerInfo, addr: String) {
        let p = ensurePeer(peer.id)
        p.online = true
        p.name = peer.name
        p.plat = peer.plat
        p.pk = peer.staticPublicKey.base64EncodedString()
        if !addr.isEmpty {
            p.addr = addr
        }
        let now = Date()
        p.lastSeen = now
        p.lastPing = now
        config.store.setLastAddr(peer.id, addr)
        config.store.updatePeerInfo(peer.id, name: peer.name, plat: peer.plat)
        send(infoMessage(Wire.tInfo), to: peer.id)
        if !layout.isEmpty {
            send(Wire.LayoutMessage(layout: layout), to: peer.id)
        }
        log("info", "connected to \(peer.name) (\(peer.plat))")
        emitState()
    }

    /// The connection to `peerID` is gone.
    func peerDown(_ peerID: String) {
        guard let p = peers[peerID], p.online else { return }
        p.online = false
        log("info", "disconnected from \(peerName(peerID))")
        peerGone(peerID)
        emitState()
    }

    /// The active machine vanished (§6 ping/pong, §6.3).
    private func peerGone(_ peerID: String) {
        guard active == peerID else { return }
        if forwardedToActive || isSelf(lastHandoverFrom) {
            log("warn", "\(peerName(peerID)) vanished while active; taking control back")
            claimSelf()
            return
        }
        // Nobody here forwarded to it: revert to the start state silently.
        active = id
        becomeActiveLocal(enter: nil, buttons: 0, mods: 0)
    }

    /// A machine was seen (or vanished) on the network.
    func discovered(_ d: Discovered) {
        guard !d.id.isEmpty, !isSelf(d.id) else { return }
        let p = ensurePeer(d.id)
        if d.addr.isEmpty {
            p.discovered = false
        } else {
            p.discovered = true
            p.addr = d.addr
            if !d.name.isEmpty { p.name = d.name }
            if !d.plat.isEmpty { p.plat = d.plat }
            if !d.pk.isEmpty { p.pk = d.pk }
            config.store.setLastAddr(d.id, d.addr)
        }
        emitState()
    }

    /// The best known address of `peerID` (`nil` when unknown).
    func peerAddr(_ peerID: String) -> String? {
        if let p = peers[peerID], !p.addr.isEmpty { return p.addr }
        if let stored = config.store.peer(peerID), !stored.lastAddr.isEmpty { return stored.lastAddr }
        return nil
    }

    /// Drops a peer from the store, the layout and the state.
    func forget(_ peerID: String) {
        config.store.forget(peerID)
        layout = config.store.layout
        if let p = peers[peerID] {
            p.online = false
            if !p.discovered {
                peers[peerID] = nil
            }
        }
        if active == peerID {
            active = id
            becomeActiveLocal(enter: nil, buttons: 0, mods: 0)
        }
        config.sender?.disconnect(peerID)
        log("info", "unpaired \(peerName(peerID))")
        emitState()
    }

    /// Routes a decrypted frame from `peerID`.
    func onPeerMessage(_ peerID: String, _ payload: Data) {
        guard let p = peers[peerID] else { return }
        p.lastSeen = Date()
        let message: Wire.Message
        do {
            message = try Wire.decode(payload)
        } catch {
            log("warn", "bad frame from \(peerName(peerID)): \(error)")
            send(Wire.ErrorMessage(code: "protocol", message: String(describing: error)), to: peerID)
            config.sender?.disconnect(peerID)
            return
        }
        if case .control(let t, let body) = message {
            handleControl(p, t: t, body: body)
        } else {
            handleInput(p, message)
        }
    }

    private func handleControl(_ p: PeerRecord, t: String, body: Data) {
        switch t {
        case Wire.tInfo, Wire.tDisplays:
            guard let m = try? Wire.parse(Wire.Info.self, from: body) else {
                log("warn", "\(t): malformed")
                return
            }
            p.displays = m.displays ?? []
            // Spec §4: remembered so this peer still has a place (and boundaries) on the
            // canvas while it is offline.
            config.store.setPeerDisplays(p.id, p.displays)
            if t == Wire.tInfo {
                if let n = m.name, !n.isEmpty { p.name = n }
                if let pl = m.plat, !pl.isEmpty { p.plat = pl }
                p.version = m.version ?? ""
                config.store.updatePeerInfo(p.id, name: p.name, plat: p.plat)
                learnPort(p, m.port ?? 0)
            }
            emitState()
        case Wire.tLayout:
            guard let m = try? Wire.parse(Wire.LayoutMessage.self, from: body) else {
                log("warn", "layout: malformed")
                return
            }
            let l = m.layout
            do {
                try l.validate()
            } catch {
                log("warn", "layout from \(peerName(p.id)): \(error)")
                return
            }
            if l.updatedAt > layout.updatedAt {
                // Spec §4: `machines` wins; a v1 peer sends only `tiles`, which convert.
                layout = l.upgraded(displays: displaysByMachine())
                try? config.store.setLayout(layout)
                log("info", "layout updated by \(peerName(p.id))")
                emitState()
            }
        case Wire.tActive:
            guard let m = try? Wire.parse(Wire.Active.self, from: body) else {
                log("warn", "active: malformed")
                return
            }
            if m.seq < seq {
                log("debug", "ignoring stale active seq \(m.seq) < \(seq)")
                return
            }
            seq = m.seq
            lastHandoverFrom = m.from
            if isSelf(m.id) {
                log("info", "control arrived from \(peerName(m.from))")
                active = id
                becomeActiveLocal(enter: m.enter, buttons: m.buttons, mods: m.modifiers)
            } else {
                setRemoteActive(m.id)
            }
            emitState()
        case Wire.tClaim:
            guard let m = try? Wire.parse(Wire.Claim.self, from: body) else {
                log("warn", "claim: malformed")
                return
            }
            if m.seq < seq { return }
            seq = m.seq
            let target = m.id.isEmpty ? p.id : m.id
            lastHandoverFrom = p.id
            if isSelf(target) {
                active = id
                becomeActiveLocal(enter: nil, buttons: 0, mods: 0)
            } else {
                log("info", "\(peerName(target)) took control")
                setRemoteActive(target)
            }
            emitState()
        case Wire.tClipboard:
            guard let m = try? Wire.parse(Wire.Clipboard.self, from: body) else { return }
            if m.kind == "text", settings.clipboardSync, let clipboard = config.clipboard {
                clipboard.setText(m.text ?? "")
            }
        case Wire.tPing:
            guard let m = try? Wire.parse(Wire.Ping.self, from: body) else { return }
            send(Wire.Ping(t: Wire.tPong, ts: m.ts), to: p.id)
        case Wire.tPong, Wire.tAuth, Wire.tAuthOK:
            break
        case Wire.tSecureInput:
            guard let m = try? Wire.parse(Wire.SecureInput.self, from: body) else { return }
            p.secureInput = m.on
            emitState()
        case Wire.tLocate:
            // Spec §6: the machine holding the mouse shook it; show the ring here.
            guard settings.shakeToLocate else { return }
            log("debug", "locate from \(peerName(p.id))")
            config.showLocate?()
        case Wire.tError:
            let m = try? Wire.parse(Wire.ErrorMessage.self, from: body)
            log("warn", "\(peerName(p.id)) reports error \(m?.code ?? ""): \(m?.message ?? "")")
        default:
            // Spec §6: an unknown control `t` must be IGNORED (only an unknown frame TYPE
            // byte closes the connection, in `Wire.decode`).
            log("debug", "unknown control \"\(t)\" from \(peerName(p.id))")
        }
    }

    /// Replaces the port of the peer's known address with the port it says it listens on
    /// (the remote port of an inbound connection is ephemeral).
    private func learnPort(_ p: PeerRecord, _ port: Int) {
        guard port > 0, port <= 65535, !p.addr.isEmpty else { return }
        guard let parts = AddressParser.split(p.addr), !parts.host.isEmpty else { return }
        let addr = AddressParser.join(parts.host, port)
        guard addr != p.addr else { return }
        p.addr = addr
        config.store.setLastAddr(p.id, addr)
    }

    /// Records that `peerID` is now active.
    private func setRemoteActive(_ peerID: String) {
        let wasSelf = isSelf(active)
        active = peerID
        forwardedToActive = false
        edgePending = nil
        if wasSelf {
            releaseModifiers()
            if settings.lockCursorWhileRemote, let device = config.device {
                device.lockCursor(true, centre: Geometry.centre(displays))
            }
        }
    }

    /// §6.1 rule 4 after `active` was set to self. Spec §3 rule 4: an `enter.pt` places the
    /// cursor exactly there (snapped onto a real display); without it, edge + frac as before.
    private func becomeActiveLocal(enter: Wire.Enter?, buttons: UInt8, mods: UInt8) {
        lastNamedActive = Date()
        forwardedToActive = false
        edgePending = nil
        shake.reset()
        let point: Geometry.Point
        if let pt = enter?.pt {
            point = Geometry.entryPoint(pt: pt.point, displays: displays)
        } else if let enter {
            point = Geometry.entryPoint(edge: enter.edge, frac: enter.frac, displays: displays)
        } else {
            point = Geometry.centre(displays)
        }
        guard let device = config.device else { return }
        device.lockCursor(false, centre: Geometry.centre(displays))
        device.moveTo(x: point.x, y: point.y)
        for bit in 0..<8 where mods & (1 << UInt8(bit)) != 0 {
            device.key(wire: 0xE0 + bit, down: true, mods: mods)
        }
        for b in 0..<5 where buttons & (1 << UInt8(b)) != 0 {
            device.button(b, down: true, clicks: 1)
        }
    }

    /// Injects key-ups for the physically held modifiers so the local OS does not keep them
    /// pressed while the key-ups go to the peer.
    private func releaseModifiers() {
        let held = mods
        guard held != 0, let device = config.device else { return }
        for bit in 0..<8 where held & (1 << UInt8(bit)) != 0 {
            device.key(wire: 0xE0 + bit, down: false, mods: 0)
        }
    }

    private func handleInput(_ p: PeerRecord, _ message: Wire.Message) {
        let now = Date()
        if !isSelf(active) && now.timeIntervalSince(lastNamedActive) > Node.activeGrace {
            return
        }
        lastRemoteInput = now
        guard let device = config.device else { return }
        let swap = settings.swapCmdCtrl && !p.plat.isEmpty && p.plat != plat
        switch message {
        case .mouseMove(let dx, let dy):
            device.moveRelative(dx: dx, dy: dy)
        case .mouseButton(let button, let down, let clicks):
            device.button(button, down: down, clicks: clicks)
        case .scroll(let dx, let dy, let flags):
            device.scroll(dx: dx, dy: dy, discrete: flags & Wire.scrollFlagDiscrete != 0)
        case .key(let usage, let state, let modifiers):
            var wire = usage
            var m = modifiers
            if swap {
                wire = Node.swapGUICtrl(wire)
                m = Node.swapGUICtrlMods(m)
            }
            device.key(wire: wire, down: state != Wire.keyUp, mods: m)
        case .keyText(let text):
            device.text(text)
        case .control:
            break
        }
        if role() != lastRole {
            emitState()
        }
    }

    /// Exchanges the GUI and Control usages (§5 optional modifier swap).
    static func swapGUICtrl(_ wire: Int) -> Int {
        switch wire {
        case 0xE0: return 0xE3
        case 0xE3: return 0xE0
        case 0xE4: return 0xE7
        case 0xE7: return 0xE4
        default: return wire
        }
    }

    /// Exchanges the Ctrl and GUI bits of a modifier byte.
    static func swapGUICtrlMods(_ m: UInt8) -> UInt8 {
        let ctrl: UInt8 = 0x11
        let gui: UInt8 = 0x88
        let c = ((m & 0x01) << 3) | ((m & 0x10) << 3)
        let g = ((m & 0x08) >> 3) | ((m & 0x80) >> 3)
        return (m & ~(ctrl | gui)) | c | g
    }

    // MARK: - Local input

    private static func buttonBit(_ button: Int) -> UInt8 {
        guard button >= 0, button <= 7 else { return 0 }
        return 1 << UInt8(button)
    }

    /// The capture callback. Physical events are either consumed locally (edge detection,
    /// hotkeys) or forwarded to the active machine, in which case `true` is returned so the OS
    /// never sees them. Injected events are never forwarded, but on the active machine their
    /// position still runs through edge detection so a remote mouse can push on to a third
    /// machine.
    func onLocalInput(_ event: InputEvent) -> Bool {
        if event.injected {
            if event.kind == .mouseMove && isSelf(active) {
                checkEdge(Geometry.Point(x: event.x, y: event.y))
            }
            return false
        }
        switch event.kind {
        case .mouseMove:
            // Spec §6: detected from the raw deltas of the physical mouse, whether or not
            // this machine is forwarding.
            if settings.shakeToLocate, shake.feed(dx: event.dx, at: Date()) {
                shakeDetected()
            }
        case .mouseButton:
            if event.down {
                buttons |= Node.buttonBit(event.button)
            } else {
                buttons &= ~Node.buttonBit(event.button)
            }
        case .key:
            if let bit = KeyMap.modifierBit(event.wire) {
                if event.down {
                    mods |= bit
                } else {
                    mods &= ~bit
                }
            } else if event.mods != 0 || mods != 0 {
                // Trust the capturer's view of the modifier state when it has one.
                if event.mods != 0 {
                    mods = event.mods
                }
            }
            if event.down && !event.isRepeat && hotkeyPressed(event) {
                return true
            }
        default:
            break
        }
        if isSelf(active) {
            if event.kind == .mouseMove || event.kind == .mouseButton {
                checkEdge(Geometry.Point(x: event.x, y: event.y))
            }
            return false
        }
        forward(event)
        return true
    }

    /// Spec §6: this machine's pointer was shaken. When it drives a remote machine the ring
    /// belongs there, so send `locate`; when it is active itself macOS already has its own
    /// shake-to-locate, so do nothing.
    private func shakeDetected() {
        guard !isSelf(active) else { return }
        log("debug", "shake: asking \(peerName(active)) to show the pointer")
        send(Wire.Locate(), to: active)
    }

    private func forward(_ event: InputEvent) {
        let frame: Data
        switch event.kind {
        case .mouseMove:
            if event.dx == 0 && event.dy == 0 { return }
            frame = Wire.mouseMove(dx: event.dx, dy: event.dy)
        case .mouseButton:
            frame = Wire.mouseButton(button: event.button, down: event.down, clicks: event.clickCount)
        case .scroll:
            frame = Wire.scroll(dx: event.dx, dy: event.dy, flags: event.flags)
        case .key:
            var state = Wire.keyUp
            if event.down {
                state = event.isRepeat ? Wire.keyRepeat : Wire.keyDown
            }
            let m = event.mods == 0 ? mods : event.mods
            frame = Wire.key(usage: event.wire, state: state, mods: m)
        case .keyText:
            frame = Wire.keyText(event.text)
        }
        forwardedToActive = true
        send(frame, to: active)
    }

    /// Spec §3 (replaces §6.1 step 2): the pointer is on an outer edge and a boundary segment
    /// faces it, so control hops to the machine on the other side. The timer, the switch delay
    /// and the "no buttons held" rule are unchanged.
    private func checkEdge(_ p: Geometry.Point) {
        guard let hop = Geometry.hop(world: world(), machine: id, point: p,
                                     deadZone: settings.edgeCornerDeadZonePx) else {
            edgePending = nil
            return
        }
        guard peerOnline(hop.target), buttons == 0 else {
            edgePending = nil
            return
        }
        let now = Date()
        if edgePending != hop.edge {
            edgePending = hop.edge
            edgeSince = now
        }
        let delay = Double(settings.switchDelayMs) / 1000
        let elapsed = now.timeIntervalSince(edgeSince)
        if elapsed < delay {
            let remaining = delay - elapsed
            edgeTimer?.cancel()
            let item = DispatchWorkItem { [weak self] in
                MainActor.assumeIsolated { self?.edgeTimerFired() }
            }
            edgeTimer = item
            DispatchQueue.main.asyncAfter(deadline: .now() + remaining, execute: item)
            return
        }
        handOver(to: hop.target,
                 enter: Wire.Enter(edge: hop.edge.opposite, frac: hop.frac, pt: hop.point))
    }

    /// Re-checks the edge once the switch delay elapsed.
    private func edgeTimerFired() {
        guard let device = config.device else { return }
        let position = device.cursorPosition()
        if edgePending != nil && isSelf(active) {
            checkEdge(position)
        }
    }

    /// Broadcasts `active` naming `target` and makes this machine inactive.
    private func handOver(to target: String, enter: Wire.Enter?) {
        seq += 1
        broadcast(Wire.Active(id: target, from: active, enter: enter, buttons: buttons, modifiers: mods, seq: seq))
        lastHandoverFrom = active
        if settings.clipboardSync, let clipboard = config.clipboard,
           let text = clipboard.text(), !text.isEmpty {
            send(Wire.Clipboard(text: text), to: target)
        }
        let whereTo: String
        if let enter {
            whereTo = "\(enter.edge.rawValue) edge at " + String(format: "%.2f", enter.frac)
        } else {
            whereTo = "centre"
        }
        log("info", "control → \(peerName(target)) (\(whereTo))")
        setRemoteActive(target)
        emitState()
    }

    /// Broadcasts `claim` and makes this machine active.
    private func claimSelf() {
        seq += 1
        broadcast(Wire.Claim(id: id, seq: seq))
        lastHandoverFrom = id
        active = id
        becomeActiveLocal(enter: nil, buttons: 0, mods: 0)
        log("info", "control → \(name) (claimed)")
        emitState()
    }

    /// Moves control to `targetID`: `claim` when it is this machine, otherwise an `active`
    /// without `enter` (the new machine shows its cursor centred).
    func claim(_ targetID: String) throws {
        if isSelf(targetID) {
            if !isSelf(active) {
                claimSelf()
            }
            return
        }
        guard peerOnline(targetID) else {
            throw BridgeError.invalidParams("\(peerName(targetID)) is not connected")
        }
        if active == targetID { return }
        handOver(to: targetID, enter: nil)
    }

    // MARK: - Hotkeys

    /// Turns "ctrl+alt" into the modifier families that must be held.
    static func parseHotkey(_ spec: String) -> [UInt8] {
        var families: [UInt8] = []
        for part in spec.lowercased().split(separator: "+") {
            switch part.trimmingCharacters(in: .whitespaces) {
            case "ctrl", "control", "⌃": families.append(famCtrl)
            case "alt", "option", "opt", "⌥": families.append(famAlt)
            case "shift", "⇧": families.append(famShift)
            case "cmd", "command", "gui", "win", "meta", "super", "⌘": families.append(famGUI)
            default: break
            }
        }
        return families
    }

    /// Whether `mods` holds exactly the given families.
    static func hotkeyMatch(_ families: [UInt8], _ mods: UInt8) -> Bool {
        guard !families.isEmpty else { return false }
        var all: UInt8 = 0
        for f in families {
            if mods & f == 0 { return false }
            all |= f
        }
        return mods & ~all == 0
    }

    /// Spec §3 hotkeys: the machine with a boundary on that side of the ACTIVE machine (the
    /// segment nearest the pointer along the edge, ties to the largest), else the nearest
    /// world-union centre in that direction inside a ±60° cone.
    private func hotkeyTarget(_ direction: Edge) -> String? {
        let cursor: Geometry.Point
        if isSelf(active), let device = config.device {
            cursor = device.cursorPosition()
        } else {
            // Not our pointer: aim from the middle of the active machine's desktop.
            let u = Geometry.union(displaysByMachine()[active] ?? [])
            cursor = Geometry.Point(x: u.x + u.w / 2, y: u.y + u.h / 2)
        }
        return Geometry.hotkeyTarget(world: world(), machine: active, direction: direction, cursor: cursor)
    }

    /// §6.2; returns `true` when the event was a hotkey.
    private func hotkeyPressed(_ event: InputEvent) -> Bool {
        let m = event.mods == 0 ? mods : event.mods
        guard Node.hotkeyMatch(hotkey, m) else { return false }
        let direction: Edge
        switch event.wire {
        case Node.usageLeft: direction = .left
        case Node.usageRight: direction = .right
        case Node.usageUp: direction = .top
        case Node.usageDown: direction = .bottom
        case Node.usageHome:
            if !isSelf(active) {
                claimSelf()
            }
            return true
        default:
            return false
        }
        guard let target = hotkeyTarget(direction) else {
            log("debug", "hotkey \(direction.rawValue): nothing that way")
            return true
        }
        if isSelf(target) {
            if !isSelf(active) {
                claimSelf()
            }
            return true
        }
        guard peerOnline(target) else {
            log("debug", "hotkey \(direction.rawValue): \(peerName(target)) is offline")
            return true
        }
        handOver(to: target, enter: nil)
        return true
    }

    // MARK: - Liveness

    /// Sends pings, drops silent peers, re-checks a pending edge and reports secure-input
    /// changes. Runs every second.
    private func tick() {
        let now = Date()
        var changed = false
        for (peerID, p) in peers where p.online {
            if now.timeIntervalSince(p.lastSeen) > Node.peerTimeout {
                p.online = false
                log("warn", "\(peerName(peerID)) stopped answering")
                config.sender?.disconnect(peerID)
                peerGone(peerID)
                changed = true
                continue
            }
            if now.timeIntervalSince(p.lastPing) >= Node.pingInterval {
                p.lastPing = now
                send(Wire.Ping(t: Wire.tPing, ts: Int64(now.timeIntervalSince1970 * 1000)), to: peerID)
            }
        }
        if let device = config.device {
            if edgePending != nil && isSelf(active) {
                checkEdge(device.cursorPosition())
            }
            // §6 secure_input: tell the senders when a password field has the keyboard here.
            let secure = device.secureInputEnabled
            if secure != secureInputOn {
                secureInputOn = secure
                broadcast(Wire.SecureInput(on: secure))
                log("info", secure ? "secure input is on: remote keystrokes are blocked" : "secure input is off")
            }
        }
        if changed || role() != lastRole {
            emitState()
        }
    }
}
