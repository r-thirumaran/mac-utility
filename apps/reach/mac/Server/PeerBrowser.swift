import Foundation

/// The other Reach agents on the LAN (`/api/peers`, `reach.state.peers`): a `BonjourBrowser`
/// on `_reach._tcp`, each new service resolved once to a literal host and port. Our own
/// advertisement is filtered out by the `id` TXT key. Services that failed to resolve are
/// retried on the next browse change and on a slow periodic refresh.
@MainActor
final class PeerBrowser {

    static let serviceType = "_reach._tcp"
    private static let refreshInterval: TimeInterval = 30

    var onChange: (() -> Void)?

    private let selfID: String
    private let browser = BonjourBrowser(type: PeerBrowser.serviceType)
    private var rows: [String: [String: Any]] = [:]
    private var resolving: Set<String> = []
    private var refreshTimer: DispatchSourceTimer?

    init(selfID: String) {
        self.selfID = selfID
    }

    // MARK: - Lifecycle

    func start() {
        browser.onChange = { [weak self] services in
            self?.update(services)
        }
        browser.onError = { error in
            Log.error("peers: browse failed: \(error)", category: "net")
        }
        browser.start()

        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + PeerBrowser.refreshInterval, repeating: PeerBrowser.refreshInterval,
                       leeway: .seconds(5))
        timer.setEventHandler { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.update(self.browser.services)
            }
        }
        timer.resume()
        refreshTimer = timer
    }

    func stop() {
        browser.stop()
        refreshTimer?.cancel()
        refreshTimer = nil
    }

    // MARK: - Peers

    /// `[{id, name, platform, host, port}]` sorted by name.
    var peers: [[String: Any]] {
        rows.values.sorted { left, right in
            (left["name"] as? String ?? "") < (right["name"] as? String ?? "")
        }
    }

    private func update(_ services: [BonjourService]) {
        var present = Set<String>()
        for service in services {
            let id = service.txt["id"] ?? ""
            guard !id.isEmpty, id != selfID else { continue }
            present.insert(service.name)
            guard rows[service.name] == nil, !resolving.contains(service.name) else { continue }
            resolve(service, id: id)
        }
        let gone = rows.keys.filter { !present.contains($0) }
        for name in gone {
            rows[name] = nil
        }
        if !gone.isEmpty {
            onChange?()
        }
    }

    private func resolve(_ service: BonjourService, id: String) {
        let instance = service.name
        let txt = service.txt
        resolving.insert(instance)
        BonjourService.resolve(service.endpoint) { [weak self] hostPort in
            guard let self else { return }
            self.resolving.remove(instance)
            guard let hostPort else { return }
            var port = Int(hostPort.port)
            if let advertised = Int(txt["port"] ?? ""), advertised > 0 {
                port = advertised
            }
            self.rows[instance] = [
                "id": id,
                "name": txt["name"] ?? instance,
                "platform": txt["plat"] ?? "",
                "host": hostPort.host,
                "port": port,
            ]
            self.onChange?()
        }
    }
}
