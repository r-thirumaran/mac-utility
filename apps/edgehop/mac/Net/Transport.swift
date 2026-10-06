import Foundation
import Network

/// The listener, Bonjour advertising and browsing, the per-peer connections with their
/// reconnect loops, and the pairing flow — `core/transport.go` on `NWListener` /
/// `NWConnection`. Connection rules of `docs/edgehop-protocol.md` §2: one connection per
/// peer, the smaller `machineId` dials, a newer inbound connection replaces an older one, the
/// initiator reconnects with 1–30 s backoff.
///
/// Main actor throughout; the sockets run on their links' queues and report back here.
@MainActor
final class Transport: FrameSender, PeerLinkDelegate {

    static let serviceType = "_edgehop._tcp"
    static let defaultPort = 24800
    /// §4: the pairing code is shown for 120 s.
    static let codeTTLSeconds = 120

    struct Config {
        var identity: Identity
        var name: String
        var plat: String
        var store: PeerStore
        /// Preferred listening port; 0 or a busy port falls back to any free port.
        var port: Int
    }

    // MARK: - Callbacks (the node and the bridge)

    var onPeerUp: ((PeerInfo, String) -> Void)?
    var onPeerDown: ((String) -> Void)?
    var onPeerMessage: ((String, Data) -> Void)?
    var onDiscovered: ((Discovered) -> Void)?
    /// The bound port, once the listener is up.
    var onListening: ((Int) -> Void)?
    /// The best address of a peer (`nil` = unknown); the node answers from its live state.
    var addrFor: ((String) -> String?)?
    /// UI events: `edgehop.pairCode` (`PairCode`), `edgehop.pairResult` (`PairResult`),
    /// `edgehop.pairing` (`PairingState`).
    var emit: ((String, Any) -> Void)?
    var logf: ((String, String) -> Void)?

    /// The bound TCP port (0 until the listener is ready).
    private(set) var port = 0

    var id: String {
        config.identity.id
    }

    private let config: Config
    private let ioQueue = DispatchQueue(label: "edgehop.transport")
    private var listener: NWListener?
    private var listenerFellBack = false
    private var browser: BonjourBrowser?
    /// Bonjour services by peer id (dialled by endpoint, so IPv6 scopes and renames work).
    private var services: [String: BonjourService] = [:]
    private var connections: [String: PeerLink] = [:]
    /// Links still in their handshake, retained until they report.
    private var pendingLinks: [ObjectIdentifier: PeerLink] = [:]
    private var dialers: [String: Dialer] = [:]
    private var pairing: PairingSession?
    private let rateLimiter = PairingRateLimiter()
    private var started = false

    init(config: Config) {
        self.config = config
    }

    // MARK: - Lifecycle

    /// Listens, advertises, browses and starts the reconnect loops of the paired peers this
    /// machine must connect to.
    func start() {
        guard !started else { return }
        started = true
        startListener(port: config.port)
        startBonjour()
        for peer in config.store.peers {
            ensureDialer(peer.id)
        }
    }

    func stop() {
        guard started else { return }
        started = false
        listener?.cancel()
        listener = nil
        browser?.stop()
        browser = nil
        for (_, dialer) in dialers {
            dialer.stop()
        }
        dialers = [:]
        for (_, link) in connections {
            link.close(reason: nil)
        }
        connections = [:]
        for (_, link) in pendingLinks {
            link.close(reason: nil)
        }
        pendingLinks = [:]
        pairing = nil
    }

    private func log(_ level: String, _ message: String) {
        logf?(level, message)
    }

    private static func short(_ id: String) -> String {
        String(id.prefix(8))
    }

    private func options(pair: Bool) -> HandshakeOptions {
        HandshakeOptions(identity: config.identity, name: config.name, plat: config.plat, trust: config.store, pair: pair)
    }

    private static func tcpParameters() -> NWParameters {
        let tcp = NWProtocolTCP.Options()
        tcp.noDelay = true
        tcp.enableKeepalive = true
        tcp.connectionTimeout = Int(PeerLink.dialTimeout)
        let parameters = NWParameters(tls: nil, tcp: tcp)
        parameters.allowLocalEndpointReuse = true
        return parameters
    }

    // MARK: - Listener

    private func txtRecord() -> [String: String] {
        ["v": "1", "id": id, "name": config.name, "plat": config.plat,
         "pk": config.identity.publicKeyData.base64EncodedString()]
    }

    private func startListener(port requested: Int) {
        let listener: NWListener
        do {
            let endpointPort = NWEndpoint.Port(rawValue: UInt16(clamping: max(requested, 0))) ?? .any
            listener = try NWListener(using: Transport.tcpParameters(), on: endpointPort)
        } catch {
            log("error", "listen: \(error)")
            return
        }
        listener.service = NWListener.Service(name: config.name,
                                              type: Transport.serviceType,
                                              domain: nil,
                                              txtRecord: NWTXTRecord(txtRecord()))
        listener.stateUpdateHandler = { [weak self, weak listener] state in
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, let listener, self.listener === listener else { return }
                    self.listenerStateChanged(state, requested: requested)
                }
            }
        }
        listener.newConnectionHandler = { [weak self] connection in
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.accept(connection)
                }
            }
        }
        self.listener = listener
        listener.start(queue: ioQueue)
    }

    private func listenerStateChanged(_ state: NWListener.State, requested: Int) {
        switch state {
        case .ready:
            port = Int(listener?.port?.rawValue ?? 0)
            log("info", "listening on tcp port \(port)")
            onListening?(port)
        case .failed(let error):
            if !listenerFellBack && requested != 0 {
                listenerFellBack = true
                log("warn", "port \(requested) busy, using any free port: \(error)")
                listener?.cancel()
                listener = nil
                startListener(port: 0)
            } else {
                log("error", "listener failed: \(error)")
                listener = nil
            }
        default:
            break
        }
    }

    private func accept(_ connection: NWConnection) {
        guard started else {
            connection.cancel()
            return
        }
        let link = PeerLink(connection: connection, role: .acceptor, address: "", options: options(pair: false))
        let limiter = rateLimiter
        link.pairingGate = { peer in !limiter.isLimited(peer.id) }
        link.delegate = self
        pendingLinks[ObjectIdentifier(link)] = link
        link.start()
    }

    /// Where an inbound peer listens: the address Bonjour (or the user) gave for it, else its
    /// host with the default port — the remote port of an inbound connection is ephemeral.
    private func inboundAddr(_ peerID: String, _ link: PeerLink) -> String {
        if let known = addrFor?(peerID), !known.isEmpty { return known }
        if let stored = config.store.peer(peerID), !stored.lastAddr.isEmpty { return stored.lastAddr }
        guard !link.remoteHost.isEmpty else { return "" }
        return AddressParser.join(link.remoteHost, Transport.defaultPort)
    }

    // MARK: - Bonjour

    private func startBonjour() {
        let browser = BonjourBrowser(type: Transport.serviceType)
        browser.onChange = { [weak self] found in
            MainActor.assumeIsolated { self?.handleBrowse(found) }
        }
        browser.onError = { [weak self] error in
            MainActor.assumeIsolated { self?.log("warn", "bonjour browse: \(error)") }
        }
        self.browser = browser
        browser.start()
    }

    private func handleBrowse(_ found: [BonjourService]) {
        var seen: Set<String> = []
        for service in found {
            guard let peerID = service.txt["id"], !peerID.isEmpty, peerID != id else { continue }
            seen.insert(peerID)
            let changed = services[peerID]?.endpoint != service.endpoint
            services[peerID] = service
            if changed {
                resolve(service, peerID: peerID)
            }
        }
        for (peerID, service) in services where !seen.contains(peerID) {
            services[peerID] = nil
            onDiscovered?(Discovered(id: peerID, name: Transport.serviceName(service), plat: service.txt["plat"] ?? "",
                                     addr: "", pk: service.txt["pk"] ?? ""))
        }
    }

    private static func serviceName(_ service: BonjourService) -> String {
        if let name = service.txt["name"], !name.isEmpty { return name }
        return service.name
    }

    private func resolve(_ service: BonjourService, peerID: String) {
        BonjourService.resolve(service.endpoint) { [weak self] hostPort in
            MainActor.assumeIsolated {
                guard let self, self.services[peerID]?.endpoint == service.endpoint else { return }
                let name = Transport.serviceName(service)
                guard let hostPort else {
                    self.log("debug", "could not resolve \(name); dialling by name")
                    self.ensureDialer(peerID)
                    self.poke(peerID)
                    return
                }
                let addr = AddressParser.join(hostPort.host, Int(hostPort.port))
                self.log("debug", "discovered \(name) at \(addr)")
                self.onDiscovered?(Discovered(id: peerID, name: name, plat: service.txt["plat"] ?? "",
                                              addr: addr, pk: service.txt["pk"] ?? ""))
                self.ensureDialer(peerID)
                self.poke(peerID)
            }
        }
    }

    // MARK: - Connections

    func isConnected(_ peerID: String) -> Bool {
        connections[peerID] != nil
    }

    func isPaired(_ peerID: String) -> Bool {
        config.store.peer(peerID) != nil
    }

    /// §2: the lexicographically smaller id connects.
    private func isInitiator(for peerID: String) -> Bool {
        id < peerID
    }

    /// The endpoint to dial for a peer: its Bonjour service when seen, else its known address.
    private func endpoint(for peerID: String) -> (endpoint: NWEndpoint, address: String)? {
        var known = addrFor?(peerID) ?? ""
        if known.isEmpty, let stored = config.store.peer(peerID) {
            known = stored.lastAddr
        }
        if let service = services[peerID] {
            return (service.endpoint, known)
        }
        guard !known.isEmpty, let endpoint = AddressParser.endpoint(known) else { return nil }
        return (endpoint, known)
    }

    /// Starts a trusted handshake with a paired peer for `dialer`; `nil` when there is nothing
    /// to dial (no address known yet).
    func dial(_ peerID: String, dialer: Dialer, verbose: Bool) -> PeerLink? {
        guard started, isPaired(peerID), let target = endpoint(for: peerID) else { return nil }
        let connection = NWConnection(to: target.endpoint, using: Transport.tcpParameters())
        let link = PeerLink(connection: connection, role: .initiator(pair: false), address: target.address,
                            options: options(pair: false))
        link.expectedPeerID = peerID
        link.verbose = verbose
        link.delegate = self
        pendingLinks[ObjectIdentifier(link)] = link
        link.start()
        return link
    }

    private func ensureDialer(_ peerID: String) {
        guard started, isInitiator(for: peerID), dialers[peerID] == nil else { return }
        let dialer = Dialer(peerID: peerID, transport: self)
        dialers[peerID] = dialer
        dialer.kick()
    }

    /// Makes the reconnect loop of `peerID` (if any) try again right now.
    func poke(_ peerID: String) {
        dialers[peerID]?.kick()
    }

    func dialerFinished(_ dialer: Dialer) {
        if dialers[dialer.peerID] === dialer {
            dialers[dialer.peerID] = nil
        }
    }

    // MARK: - FrameSender

    @discardableResult
    func send(_ frame: Data, to peerID: String) -> Bool {
        guard let link = connections[peerID] else { return false }
        link.send(frame)
        return true
    }

    func broadcast(_ frame: Data) {
        for (_, link) in connections {
            link.send(frame)
        }
    }

    /// Closes the connection to `peerID`; the reconnect loop (if this machine is the
    /// initiator) dials again after its backoff.
    func disconnect(_ peerID: String) {
        connections[peerID]?.close(reason: nil)
    }

    /// Stops reconnecting to `peerID` and drops its connection (the store is the node's
    /// business).
    func unpair(_ peerID: String) {
        if let dialer = dialers[peerID] {
            dialer.stop()
            dialers[peerID] = nil
        }
        connections[peerID]?.close(reason: nil)
    }

    // MARK: - Pairing

    /// Connects to a machine with a pairing handshake. `peerID` may be empty when `address`
    /// is given; a bare host gets the default port. Progress is reported through
    /// `edgehop.pairing` events and the code is asked for with `edgehop.pair.confirm`.
    func pairStart(peerID: String, address: String) throws {
        guard started else { throw TransportError.notStarted }
        guard pairing == nil else { throw TransportError.pairingInProgress }
        var addr = address.trimmingCharacters(in: .whitespacesAndNewlines)
        var endpoint: NWEndpoint?
        if addr.isEmpty {
            guard let target = self.endpoint(for: peerID) else { throw TransportError.noAddress(peerID) }
            endpoint = target.endpoint
            addr = target.address
        } else {
            if AddressParser.split(addr) == nil {
                addr = AddressParser.join(addr, Transport.defaultPort)
            }
            guard let parsed = AddressParser.endpoint(addr) else { throw TransportError.badAddress(address) }
            endpoint = parsed
        }
        guard let endpoint else { throw TransportError.noAddress(peerID) }

        let session = PairingSession(peerID: peerID, address: addr)
        pairing = session
        emitPairing(session, stage: "connecting")
        let connection = NWConnection(to: endpoint, using: Transport.tcpParameters())
        let link = PeerLink(connection: connection, role: .initiator(pair: true), address: addr, options: options(pair: true))
        link.delegate = self
        session.link = link
        pendingLinks[ObjectIdentifier(link)] = link
        link.start()
    }

    /// Supplies the code shown on the other machine.
    func pairConfirm(peerID: String, code: String) throws {
        guard let session = pairing, let link = session.link else { throw TransportError.noPairing }
        if !peerID.isEmpty && !session.peerID.isEmpty && peerID != session.peerID {
            throw TransportError.wrongPairing
        }
        guard !session.codeSubmitted else { throw TransportError.codeAlreadySubmitted }
        session.codeSubmitted = true
        link.resume(code: code)
    }

    /// Aborts the pairing in progress.
    func pairCancel() {
        pairing?.link?.close(reason: "cancelled")
    }

    private func emitPairing(_ session: PairingSession, stage: String, error: String = "") {
        emit?("edgehop.pairing", PairingState(stage: stage, peerId: session.peerID, name: session.peerName,
                                              address: session.address, error: error))
    }

    /// The UI's error word for a failed pairing (`pairError` in `transport.go`).
    private static func pairError(_ reason: String?) -> String {
        guard let reason, !reason.isEmpty else { return "failed" }
        switch reason {
        case "unpaired": return "refused"
        case "auth_failed", "protocol", "timeout", "bad_frame", "rate_limited": return "failed"
        default: return reason
        }
    }

    // MARK: - PeerLinkDelegate

    func linkDidEstablish(_ link: PeerLink, peer: PeerInfo) {
        pendingLinks[ObjectIdentifier(link)] = nil
        if let expected = link.expectedPeerID, expected != peer.id {
            log("warn", "\(Transport.short(peer.id)) answered at \(link.address) instead of \(Transport.short(expected))")
            link.close(reason: nil)
            return
        }
        var addr = link.address
        switch link.role {
        case .acceptor:
            addr = inboundAddr(peer.id, link)
            if link.pairingPeer != nil {
                emit?("edgehop.pairResult", PairResult(peerId: peer.id, name: peer.name, ok: true))
                log("info", "paired with \(peer.name)")
                ensureDialer(peer.id)
            }
        case .initiator(let pair):
            if pair, let session = pairing, session.link === link {
                session.peerID = peer.id
                session.peerName = peer.name
                log("info", "paired with \(peer.name)")
                emitPairing(session, stage: "paired")
                pairing = nil // the next pairing may start while this connection lives on
                ensureDialer(peer.id)
            }
        }
        if let old = connections[peer.id], old !== link {
            log("debug", "replacing older connection to \(peer.name)")
            old.close(reason: nil)
        }
        connections[peer.id] = link
        onPeerUp?(peer, addr)
    }

    func link(_ link: PeerLink, needsCodeFor peer: PeerInfo) {
        guard let session = pairing, session.link === link else {
            // A trusted dial met a peer that wants to pair: not something a reconnect can do.
            link.close(reason: "unpaired")
            return
        }
        session.peerID = peer.id
        session.peerName = peer.name
        emitPairing(session, stage: "waiting_code")
    }

    func link(_ link: PeerLink, showCode code: String, for peer: PeerInfo) {
        let machine = Machine(id: peer.id, name: peer.name, plat: peer.plat, status: "discovered", paired: false)
        emit?("edgehop.pairCode", PairCode(code: code, peer: machine, ttlSec: Transport.codeTTLSeconds))
    }

    func link(_ link: PeerLink, didReceive frame: Data) {
        guard let peer = link.peer, connections[peer.id] === link else { return }
        onPeerMessage?(peer.id, frame)
    }

    func link(_ link: PeerLink, didCloseWith reason: String?) {
        pendingLinks[ObjectIdentifier(link)] = nil

        // A trusted dial: tell its reconnect loop how it went.
        if let expected = link.expectedPeerID, let dialer = dialers[expected], dialer.link === link {
            if !link.isEstablished {
                if reason == "unpaired" {
                    log("warn", "\(Transport.short(expected)) no longer trusts this machine; pair again")
                } else if link.verbose {
                    log("debug", "dial \(Transport.short(expected)) (\(link.address)): \(reason ?? "closed")")
                }
            }
            dialer.attemptEnded(established: link.isEstablished)
        }

        // A pairing this machine started.
        if let session = pairing, session.link === link {
            pairing = nil
            if reason == "cancelled" {
                log("info", "pairing with \(session.address) cancelled")
                emitPairing(session, stage: "cancelled")
            } else {
                let error = Transport.pairError(reason)
                log("warn", "pairing with \(session.address) failed: \(error)")
                emitPairing(session, stage: "failed", error: error)
            }
        }

        // A pairing request that arrived here and did not complete.
        if case .acceptor = link.role, !link.isEstablished, let requester = link.pairingPeer {
            rateLimiter.recordFailure(requester.id)
            let error = Transport.pairError(reason)
            log("warn", "pairing with \(requester.name) failed: \(error)")
            emit?("edgehop.pairResult", PairResult(peerId: requester.id, name: requester.name, ok: false, error: error))
        } else if case .acceptor = link.role, !link.isEstablished, reason == "unpaired" {
            log("info", "refused unpaired connection from \(link.remoteHost)")
        }

        // A live connection ended.
        if let peer = link.peer, connections[peer.id] === link {
            connections[peer.id] = nil
            onPeerDown?(peer.id)
        }
    }
}
