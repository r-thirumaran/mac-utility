#if canImport(Network)
import Foundation
import Network

/// A minimal HTTP/1.1 server on `NWListener`: static files, a few routes, and WebSocket
/// upgrades. It is what Reach uses to serve the phone PWA and `/ws`, and what any app can use
/// for a loopback API.
///
/// Threading: all socket I/O happens on `ioQueue` inside `HTTPConnection`; every closure the
/// app installs (`auth`, routes, `onWebSocket`, `WebSocketConnection` callbacks) is invoked on
/// the main actor. One request per connection: responses carry `Connection: close`, which
/// keeps the state machine trivial and is fine for a LAN API.
///
/// Bonjour advertising rides on the listener (`NWListener.service`). When `port` is `nil` the
/// kernel picks one; it is published through `port` / `onReady` once the listener is up.
@MainActor
final class HTTPServer {

    typealias RouteHandler = (HTTPRequest) -> HTTPResponse
    typealias Advertisement = (type: String, name: String, txt: [String: String])

    /// Directory served for paths no route claims (`/` → `index.html`).
    var staticRoot: URL?
    /// Gate every request (including WebSocket upgrades). `false` → 401.
    var auth: ((HTTPRequest) -> Bool)?
    /// Receives each accepted WebSocket. Unset → upgrades are answered with 404.
    var onWebSocket: ((WebSocketConnection) -> Void)?
    /// Fired on the main actor once the listener is accepting connections.
    var onReady: ((UInt16) -> Void)?
    /// Fired on the main actor if the listener fails after `start()`.
    var onError: ((Error) -> Void)?

    /// The bound port once listening.
    private(set) var port: UInt16?

    private let requestedPort: UInt16?
    private let advertisement: Advertisement?
    private let allowLAN: Bool
    private var routes: [String: RouteHandler] = [:]
    private var listener: NWListener?

    nonisolated let ioQueue = DispatchQueue(label: "shared.httpserver.io")
    nonisolated let registry = HTTPConnectionRegistry()

    /// - Parameters:
    ///   - port: fixed port, or `nil` for any free port.
    ///   - bonjour: advertise `_type._tcp` with this instance name and TXT record.
    ///   - allowLAN: `true` binds every interface; `false` binds 127.0.0.1 only.
    init(port: UInt16?,
         bonjour: (type: String, name: String, txt: [String: String])?,
         allowLAN: Bool) {
        self.requestedPort = port
        self.advertisement = bonjour
        self.allowLAN = allowLAN
    }

    // MARK: - Routes

    /// Registers an exact-match route, e.g. `route("GET", "/api/info") { _ in … }`.
    /// `HEAD` falls back to the `GET` route with the body stripped.
    func route(_ method: String, _ path: String, handler: @escaping RouteHandler) {
        routes[HTTPServer.routeKey(method, path)] = handler
    }

    private static func routeKey(_ method: String, _ path: String) -> String {
        method.uppercased() + " " + path
    }

    // MARK: - Lifecycle

    func start() throws {
        guard listener == nil else { return }

        let parameters = NWParameters.tcp
        parameters.allowLocalEndpointReuse = true

        let listener: NWListener
        if allowLAN {
            if let requestedPort {
                listener = try NWListener(using: parameters, on: NWEndpoint.Port(integerLiteral: requestedPort))
            } else {
                listener = try NWListener(using: parameters)
            }
        } else {
            let port: NWEndpoint.Port = requestedPort.map { NWEndpoint.Port(integerLiteral: $0) } ?? .any
            parameters.requiredLocalEndpoint = NWEndpoint.hostPort(host: "127.0.0.1", port: port)
            listener = try NWListener(using: parameters)
        }

        if let advertisement {
            listener.service = NWListener.Service(name: advertisement.name,
                                                  type: advertisement.type,
                                                  domain: nil,
                                                  txtRecord: NWTXTRecord(advertisement.txt))
        }

        listener.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready:
                DispatchQueue.main.async { [weak self] in
                    guard let self else { return }
                    MainActor.assumeIsolated {
                        let port = self.listener?.port?.rawValue
                        self.port = port
                        if let port { self.onReady?(port) }
                    }
                }
            case .failed(let error):
                Log.error("HTTPServer: listener failed: \(error)", category: "net")
                DispatchQueue.main.async { [weak self] in
                    guard let self else { return }
                    MainActor.assumeIsolated {
                        self.listener = nil
                        self.port = nil
                        self.onError?(error)
                    }
                }
            default:
                break
            }
        }

        listener.newConnectionHandler = { [weak self] connection in
            guard let self else {
                connection.cancel()
                return
            }
            let http = HTTPConnection(connection: connection,
                                      queue: self.ioQueue,
                                      registry: self.registry,
                                      server: self)
            self.registry.add(http)
            http.start()
        }

        self.listener = listener
        listener.start(queue: ioQueue)
    }

    func stop() {
        listener?.cancel()
        listener = nil
        port = nil
        registry.closeAll(on: ioQueue)
    }

    // MARK: - Dispatch (main actor)

    /// Called by `HTTPConnection` for every parsed request.
    func dispatch(_ request: HTTPRequest, from connection: HTTPConnection) {
        if let auth, !auth(request) {
            connection.respond(.unauthorized())
            return
        }

        if request.isWebSocketUpgrade {
            guard let onWebSocket, let key = request.webSocketKey else {
                connection.respond(.notFound())
                return
            }
            let socket = WebSocketConnection(transport: connection)
            let accept = WebSocketFrame.acceptKey(for: key)
            connection.upgrade(to: socket, response: .switchingProtocols(acceptKey: accept))
            onWebSocket(socket)
            return
        }

        let isHead = request.method == "HEAD"
        let lookupMethod = isHead ? "GET" : request.method
        var response: HTTPResponse
        if let handler = routes[HTTPServer.routeKey(lookupMethod, request.path)] {
            response = handler(request)
        } else {
            response = serveStatic(request, method: lookupMethod)
        }
        if isHead {
            response.headers["Content-Length"] = String(response.body.count)
            response.body = Data()
        }
        connection.respond(response)
    }

    private func serveStatic(_ request: HTTPRequest, method: String) -> HTTPResponse {
        guard method == "GET", let root = staticRoot else { return .notFound() }
        guard let file = StaticFileResolver.resolve(path: request.path, under: root),
              let data = try? Data(contentsOf: file) else { return .notFound() }
        return HTTPResponse.ok(body: data, contentType: HTTPResponse.mimeType(forExtension: file.pathExtension))
            .withHeader("Cache-Control", "no-cache")
    }
}
#endif
