import Foundation

/// One paired phone on `/ws`: parses `{id, method, params}`, dispatches through
/// `ReachMethods`, answers `{id, result}` or `{id, error: {code, message, details?}}`,
/// receives pushed events and owns the connection's screen stream. Sends the `info` event
/// once on connect (`docs/reach-api.md` §3).
@MainActor
final class WSSession {

    let socket: WebSocketConnection
    let device: String
    let token: String

    private unowned let api: ReachAPI
    private var stream: ScreenStreamer?
    private var closed = false

    init(socket: WebSocketConnection, device: String, token: String, api: ReachAPI) {
        self.socket = socket
        self.device = device
        self.token = token
        self.api = api
    }

    // MARK: - Lifecycle

    func start() {
        socket.onText = { [weak self] text in
            self?.handle(text)
        }
        socket.onBinary = { _ in
            // Phones never send binary frames.
        }
        socket.onClose = { [weak self] in
            self?.didClose()
        }
        send(event: "info", data: api.infoFull())
    }

    /// Closes the socket (a revoked pairing). `onClose` follows and removes the session.
    func close() {
        closed = true
        stopStream()
        socket.close(code: 1000, reason: "revoked")
    }

    private func didClose() {
        closed = true
        stopStream()
        api.sessionClosed(self)
    }

    // MARK: - Events

    func send(event: String, data: Any) {
        send(["event": event, "data": data])
    }

    // MARK: - Screen stream

    func startStream(display: Int, maxWidth: Int, quality: Double, fps: Int) {
        stopStream()
        let socket = self.socket
        let streamer = ScreenStreamer(display: display, maxWidth: maxWidth, quality: quality, fps: fps) { frame in
            socket.send(binary: frame)
        }
        stream = streamer
        streamer.start()
    }

    func stopStream() {
        stream?.stop()
        stream = nil
    }

    // MARK: - Requests

    private func handle(_ text: String) {
        guard let object = (try? JSONValue.parse(text)) as? [String: Any] else {
            reply(id: NSNull(), error: ReachError.invalidParams("bad request: not a JSON object"))
            return
        }
        let id: Any = object["id"] ?? NSNull()
        guard let method = object["method"] as? String, !method.isEmpty else {
            reply(id: id, error: ReachError.invalidParams("missing method"))
            return
        }
        let params = object["params"] as? [String: Any] ?? [:]
        api.methods.call(method, params: params, session: self, local: false) { [weak self] result in
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case .success(let value):
                        self.reply(id: id, result: value)
                    case .failure(let error):
                        self.reply(id: id, error: ReachError.wrap(error))
                    }
                }
            }
        }
    }

    private func reply(id: Any, result: Any) {
        send(["id": id, "result": result])
    }

    private func reply(id: Any, error: ReachError) {
        send(["id": id, "error": error.asDictionary])
    }

    private func send(_ object: [String: Any]) {
        guard !closed else { return }
        do {
            socket.send(text: try JSONValue.string(object))
        } catch {
            Log.error("ws: frame is not JSON-serialisable: \(error)", category: "net")
            guard let id = object["id"] else { return }
            let fallback: [String: Any] = ["id": id, "error": ReachError.internalError("encode result: \(error)").asDictionary]
            if let text = try? JSONValue.string(fallback) {
                socket.send(text: text)
            }
        }
    }
}
