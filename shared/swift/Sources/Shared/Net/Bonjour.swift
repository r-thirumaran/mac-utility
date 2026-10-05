#if canImport(Network)
import Foundation
import Network

/// Browses for `_type._tcp` services with `NWBrowser` and reports the full current set on
/// every change (on the main queue). TXT records come with the results
/// (`bonjourWithTXTRecord`), so there is no separate resolve step for metadata.
///
/// Advertising is not here: `HTTPServer` advertises through its `NWListener`, and an app
/// that only needs a plain TCP listener does the same with `NWListener.service`.
final class BonjourBrowser {

    /// Current services whenever the set changes. Sorted by name.
    var onChange: (([BonjourService]) -> Void)?
    /// The browser failed (e.g. Local Network permission denied).
    var onError: ((Error) -> Void)?

    /// Last published set.
    private(set) var services: [BonjourService] = []

    private let type: String
    private let domain: String?
    private let queue = DispatchQueue(label: "shared.bonjour.browser")
    private var browser: NWBrowser?

    init(type: String, domain: String? = nil) {
        self.type = type
        self.domain = domain
    }

    // MARK: - Lifecycle

    func start() {
        guard browser == nil else { return }

        let parameters = NWParameters()
        parameters.includePeerToPeer = true
        let browser = NWBrowser(for: .bonjourWithTXTRecord(type: type, domain: domain), using: parameters)

        browser.browseResultsChangedHandler = { [weak self] results, _ in
            self?.publish(results)
        }
        browser.stateUpdateHandler = { [weak self] state in
            if case .failed(let error) = state {
                Log.error("BonjourBrowser: failed: \(error)", category: "net")
                DispatchQueue.main.async {
                    self?.onError?(error)
                }
            }
        }

        self.browser = browser
        browser.start(queue: queue)
    }

    func stop() {
        browser?.cancel()
        browser = nil
    }

    // MARK: - Results

    private func publish(_ results: Set<NWBrowser.Result>) {
        let found = results.compactMap(BonjourBrowser.service(from:)).sorted { $0.name < $1.name }
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.services = found
            self.onChange?(found)
        }
    }

    static func service(from result: NWBrowser.Result) -> BonjourService? {
        guard case .service(let name, let type, let domain, _) = result.endpoint else { return nil }
        var txt: [String: String] = [:]
        if case .bonjour(let record) = result.metadata {
            txt = record.dictionary
        }
        return BonjourService(name: name, type: type, domain: domain, endpoint: result.endpoint, txt: txt)
    }
}
#endif
