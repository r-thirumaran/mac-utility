# shared/swift — the Swift library every macOS app is built from

Compiled into each app by `Tools/build_mac_app.sh` (every `.swift` under `Sources/Shared`,
`-parse-as-library`, Swift 5 language mode, macOS 14). There is no `main.swift` and no
top-level code here; the apps keep their `@main` in `apps/<app>/mac`.

`Package.swift` exists only for tests: `swift test` runs the Foundation-only parts on the
macOS CI runner **and** on Linux (CryptoKit is swapped for `swift-crypto` there; every
AppKit / WebKit / Network file is wrapped in `#if canImport(…)` and compiles to nothing).

```
cd shared/swift && swift test          # macOS or Linux
```

## Layout

| File | Purpose |
|---|---|
| `Net/FrameCodec.swift` | `u32` big-endian length-prefixed frames (Edgehop §3), 1 MiB cap |
| `Net/WebSocketFrame.swift` | RFC 6455 framing + `Sec-WebSocket-Accept` |
| `Net/HTTPRequest.swift` · `HTTPRequestParser.swift` · `HTTPResponse.swift` | incremental HTTP/1.1 request parser, response builder, MIME table |
| `Net/StaticFileResolver.swift` | path → file under a root, traversal-safe, `index.html` for directories |
| `Net/HTTPServer.swift` · `HTTPConnection.swift` · `WebSocketConnection.swift` | `NWListener` HTTP server with static files, routes, WebSocket upgrade, Bonjour advertising |
| `Net/Bonjour.swift` · `BonjourService.swift` | `NWBrowser` wrapper with TXT records and an address resolver |
| `Crypto/Identity.swift` · `PeerInfo.swift` · `TrustStore.swift` · `MemoryTrustStore.swift` | long-term X25519 identity, peer record, trust storage |
| `Crypto/Handshake.swift` · `HandshakeOptions.swift` · `HandshakeStep.swift` · `HandshakeError.swift` · `HandshakeMessage.swift` · `EdgehopKDF.swift` | Edgehop §4 handshake state machine and key derivation |
| `Crypto/SecureChannel.swift` · `SecureChannelKeys.swift` · `SecureChannelError.swift` | ChaCha20-Poly1305 channel with counter nonces |
| `Web/NativeBridge.swift` · `BridgeError.swift` | `WKScriptMessageHandlerWithReply` dispatcher for `shared/ui/native.js` |
| `Web/AppSchemeHandler.swift` | serves `app://ui/…` from `Resources/ui` |
| `Web/WebWindowController.swift` | `NSWindow` + `WKWebView` hosting the UI |
| `MenuBar/StatusItemHost.swift` | `NSStatusItem` with closure menu items and a live title |
| `System/Permissions.swift` | Accessibility / Input Monitoring / Screen Recording checks, prompts, Settings deep links |
| `System/AdminShell.swift` | `osascript … with administrator privileges` runner |
| `System/LaunchAtLogin.swift` | `SMAppService` wrapper |
| `System/Log.swift` | `os.Logger` wrapper (print on Linux) |
| `System/Shell.swift` | run a process, capture stdout/stderr |
| `JSON/JSONValue.swift` | Codable ⇄ `[String: Any]` helpers (`jsonObject`, `decodeJSON`) |

Everything is `internal`: the files are compiled straight into the app module, and the test
target uses `@testable import Shared`.

## API

### Framing — `FrameCodec`

```swift
enum FrameCodec {
    static let maxLength = 1 << 20
    static func encode(_ payload: Data) -> Data
    static func decode(from buffer: inout Data) throws -> Data?   // nil until a whole frame is buffered
}
enum FrameError: Error { case tooLarge }
```

### Edgehop crypto

```swift
struct Identity {                       // long-term X25519 key; persist rawPrivateKey (0600)
    init(); init(rawPrivateKey: Data) throws
    var publicKeyData: Data; var rawPrivateKey: Data
    var id: String                      // lowercase hex of SHA-256(pub)[0..<16]
}
struct PeerInfo { var id, name, plat: String; var staticPublicKey: Data }
protocol TrustStore: AnyObject { func trustedKey(for id: String) -> Data?; func trust(_ peer: PeerInfo) }
final class MemoryTrustStore: TrustStore

struct HandshakeOptions {
    init(identity: Identity, name: String, plat: String, trust: any TrustStore,
         pair: Bool = false,
         askCode: (() throws -> String)? = nil,            // initiator: blocking prompt (off-main)
         showCode: ((String, PeerInfo) -> Void)? = nil,    // acceptor: display the generated code
         ephemeralKeyOverride: Curve25519.KeyAgreement.PrivateKey? = nil,   // tests / vectors
         codeOverride: String? = nil)                      // tests, or a code already collected
}
enum HandshakeStep {
    case send(Data)
    case established(keys: SecureChannelKeys, peer: PeerInfo, reply: Data?)  // send `reply` first if present
    case needCode(peer: PeerInfo)                                           // initiator: call resume(code:)
    case failed(reason: String, reply: Data?)                               // send `reply` if present, then close
}
final class Handshake {
    init(options: HandshakeOptions, initiator: Bool)
    func start() throws -> Data?                  // initiator: the `hello` payload; acceptor: nil
    func receive(_ plaintext: Data) throws -> HandshakeStep
    func resume(code: String) throws -> HandshakeStep
    func confirmAuthenticated()                   // after auth/auth_ok: persists a pairing into the trust store
    var peer: PeerInfo?; var isPairing: Bool; var pairingCode: String?
    static let authFrame: Data                    // 0x01 || {"t":"auth"}
    static let authOKFrame: Data                  // 0x01 || {"t":"auth_ok"}
}
struct SecureChannelKeys { var initiatorToAcceptor: Data; var acceptorToInitiator: Data }
final class SecureChannel {                       // not thread-safe: one queue per connection
    init(keys: SecureChannelKeys, isInitiator: Bool)
    func seal(_ plaintext: Data) throws -> Data   // ciphertext || tag
    func open(_ ciphertext: Data) throws -> Data  // throws .decryptionFailed / .counterExhausted / .broken
}
```

`hello` / `hello_ack` are built by hand with the field order `t, v, id, name, plat, spk, epk,
pair` and Go-compatible string escaping, and the transcript hash covers the exact bytes sent
and received. Rate-limiting failed pairings (5 per peer per 10 min) and timing out the code
display (120 s) are the app's job.

### WebSocket framing — `WebSocketFrame`

```swift
struct WebSocketFrame {
    enum Opcode: UInt8 { case continuation, text, binary, close, ping, pong }
    var opcode: Opcode; var fin: Bool; var payload: Data
    static func text(_: String) / binary(_: Data) / close(code: UInt16 = 1000, reason: String = "") -> WebSocketFrame
    static func encode(_ frame: WebSocketFrame, masked: Bool = false, maskingKey: UInt32? = nil) -> Data
    static func decode(from buffer: inout Data, requireMask: Bool = false) throws -> WebSocketFrame?
    static func acceptKey(for clientKey: String) -> String
    var closeCode: UInt16?; var textPayload: String?
}
```

### HTTP

```swift
struct HTTPRequest { var method, target, path: String; var query, headers: [String: String]; var body: Data
                     var isWebSocketUpgrade: Bool; var webSocketKey: String?; func header(_:) -> String? }
enum HTTPRequestParser { static func parse(from buffer: inout Data) throws -> HTTPRequest? }
struct HTTPResponse {
    var status: Int; var reason: String; var headers: [String: String]; var body: Data
    static func ok(body:contentType:), ok(text:), json(_:status:), noContent(), badRequest(_:),
                unauthorized(), forbidden(), notFound(), methodNotAllowed(), internalError(_:),
                switchingProtocols(acceptKey:)
    func allowingAnyOrigin() -> HTTPResponse; func withHeader(_:_:) -> HTTPResponse
    func serialize() -> Data
    static func mimeType(forExtension: String) -> String
}

@MainActor final class HTTPServer {
    init(port: UInt16?, bonjour: (type: String, name: String, txt: [String: String])?, allowLAN: Bool)
    var staticRoot: URL?
    var auth: ((HTTPRequest) -> Bool)?               // false → 401 (applies to /ws too)
    func route(_ method: String, _ path: String, handler: @escaping (HTTPRequest) -> HTTPResponse)
    var onWebSocket: ((WebSocketConnection) -> Void)?
    var onReady: ((UInt16) -> Void)?; var onError: ((Error) -> Void)?
    func start() throws; func stop(); var port: UInt16?
}
final class WebSocketConnection {
    let remoteDescription: String
    func send(text: String); func send(binary: Data); func close(code: UInt16 = 1000, reason: String = "")
    var onText: ((String) -> Void)?; var onBinary: ((Data) -> Void)?; var onClose: (() -> Void)?
}
```

Responses carry `Connection: close` (one request per connection). All app callbacks run on
the main actor; I/O runs on the server's own queue.

### Bonjour

```swift
final class BonjourBrowser {
    init(type: String, domain: String? = nil)
    var onChange: (([BonjourService]) -> Void)?       // main queue, full sorted set on every change
    var onError: ((Error) -> Void)?
    func start(); func stop()
}
struct BonjourService {
    let name, type, domain: String; let endpoint: NWEndpoint; let txt: [String: String]
    static func resolve(_ endpoint: NWEndpoint, timeout: TimeInterval = 5, completion: @escaping (HostPort?) -> Void)
}
```

Advertising is done by `HTTPServer`'s listener; a plain TCP listener does the same with
`listener.service = NWListener.Service(name:type:domain:txtRecord:)`.

### Web UI

```swift
@MainActor final class NativeBridge: NSObject, WKScriptMessageHandlerWithReply {
    typealias Handler = ([String: Any]) throws -> Any?
    func register(_ method: String, _ handler: @escaping Handler)
    func registerAsync(_ method: String, _ handler: @escaping ([String: Any], @escaping (Result<Any?, Error>) -> Void) -> Void)
    func attach(to configuration: WKWebViewConfiguration)   // installs "native"
    func emit(_ event: String, _ data: Any)                  // window.__nativeEvent({event, data})
    weak var webView: WKWebView?
}
struct BridgeError: Error { let code: String; let message: String
    init(_ code: Code, _ message: String)   // .notAuthorized .needsPermission .notSupported .invalidParams .internalError
}
@MainActor final class AppSchemeHandler: NSObject, WKURLSchemeHandler { init(root: URL) }   // app://ui/<path>
@MainActor final class WebWindowController: NSWindowController {
    init(title: String, size: NSSize, uiRoot: URL, bridge: NativeBridge, minSize: NSSize = NSSize(width: 720, height: 480))
    func show(); func reload(); let webView: WKWebView; let bridge: NativeBridge
}
```

Handler results and event data must be JSON-serialisable; `jsonObject(_:)` turns a Codable
model into one and `decodeJSON(_:from:)` reads `params` into one.

### Menu bar, system

```swift
@MainActor final class StatusItemHost {
    init(image: NSImage?, title: String?)
    var menu: NSMenu
    func setTitle(_: String?); func setImage(_: NSImage?); func setToolTip(_: String?)
    @discardableResult func addItem(title: String, key: String = "", action: @escaping () -> Void) -> NSMenuItem
    func addSeparator(); func removeAllItems(); func remove()
}
enum Permissions {
    enum Pane { case accessibility, inputMonitoring, screenRecording, localNetwork }
    static var accessibilityGranted / inputMonitoringGranted / screenRecordingGranted: Bool
    static func requestAccessibility() / requestInputMonitoring() / requestScreenRecording() -> Bool
    static func openSettingsPane(_ pane: Pane)
    static var summary: [String: Bool]           // for app.info
}
enum AdminShell { static func run(script: String, prompt: String) throws -> String
                  static func run(commands: [String], prompt: String) throws -> String }
enum AdminShellError: Error { case cancelled; case failed(status: Int32, stderr: String) }
enum LaunchAtLogin { static var isEnabled: Bool; static var requiresApproval: Bool
                     static func setEnabled(_: Bool) throws; static func openLoginItemsSettings() }
enum Log { static func info/error/debug(_ message: String, category: String = "app") }
enum Shell { static func run(_ launchPath: String, _ args: [String], input: String? = nil) throws
                 -> (status: Int32, stdout: String, stderr: String) }
```

## Wiring an app

### Status item + web window + bridge

```swift
@main
enum VitalsMain {
    private static let delegate = AppDelegate()
    static func main() {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        app.delegate = delegate
        app.run()
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let bridge = NativeBridge()
    private var window: WebWindowController?
    private lazy var statusItem = StatusItemHost(
        image: NSImage(systemSymbolName: "gauge", accessibilityDescription: "Vitals"), title: nil)

    func applicationDidFinishLaunching(_ notification: Notification) {
        bridge.register("app.info") { _ in
            ["name": "Vitals", "version": "1.0", "platform": "macos",
             "machine": Host.current().localizedName ?? "", "permissions": Permissions.summary]
        }
        bridge.register("app.openSettingsPane") { params in
            guard let raw = params["pane"] as? String, let pane = Permissions.Pane(rawValue: raw) else {
                throw BridgeError.invalidParams("unknown pane")
            }
            Permissions.openSettingsPane(pane)
            return [:]
        }
        bridge.registerAsync("power.apply") { params, done in
            DispatchQueue.global().async {
                do {
                    let out = try AdminShell.run(commands: ["pmset -b displaysleep 5"],
                                                 prompt: "Vitals wants to change power settings.")
                    done(.success(["applied": out]))
                } catch AdminShellError.cancelled {
                    done(.failure(BridgeError.notAuthorized("cancelled")))
                } catch {
                    done(.failure(error))
                }
            }
        }

        statusItem.addItem(title: "Open Vitals", key: "o") { [weak self] in self?.showWindow() }
        statusItem.addSeparator()
        statusItem.addItem(title: "Quit Vitals", key: "q") { NSApp.terminate(nil) }
    }

    private func showWindow() {
        if window == nil, let ui = Bundle.main.resourceURL?.appendingPathComponent("ui") {
            window = WebWindowController(title: "Vitals", size: NSSize(width: 900, height: 600),
                                         uiRoot: ui, bridge: bridge)
        }
        window?.show()
    }

    // Push a sample every second: the page sees it via native.on('vitals.sample', …).
    private func publish(_ sample: Sample) throws {
        bridge.emit("vitals.sample", try jsonObject(sample))
    }
}
```

The web UI (`apps/<app>/ui/static`) is copied into `Contents/Resources/ui` by the build
script, with `shared/ui` at `ui/shared`, so `index.html` imports `./shared/native.js`.

### HTTP server + Bonjour (Reach)

```swift
let server = HTTPServer(port: 7788,
                        bonjour: (type: "_reach._tcp", name: Host.current().localizedName ?? "Mac",
                                  txt: ["v": "1", "id": agentID, "name": name, "plat": "macos", "port": "7788"]),
                        allowLAN: true)
server.staticRoot = Bundle.main.resourceURL?.appendingPathComponent("ui")
server.route("GET", "/api/info") { _ in
    HTTPResponse.json(["id": agentID, "name": name, "platform": "macos", "version": version,
                       "pairingRequired": true, "caps": ["screen", "apps", "media", "power", "clipboard"]])
        .allowingAnyOrigin()
}
server.route("OPTIONS", "/api/pair") { _ in HTTPResponse.noContent().allowingAnyOrigin() }
server.auth = { request in
    guard request.path == "/ws" else { return true }          // only the socket needs a token
    return tokens.isValid(request.query["token"] ?? "")
}
server.onWebSocket = { socket in
    socket.onText = { text in
        // {id, method, params} → {id, result} | {id, error}; same dispatch as the bridge.
        socket.send(text: handle(text))
    }
    socket.onClose = { streams.stop(for: socket) }
    socket.send(binary: jpegFrame)                            // screen frames
}
try server.start()

let browser = BonjourBrowser(type: "_reach._tcp")
browser.onChange = { services in
    peers = services.filter { $0.txt["id"] != agentID }
    for service in peers {
        BonjourService.resolve(service.endpoint) { address in /* host/port for /api/peers */ }
    }
}
browser.start()
```

### Secure channel over `NWConnection` (Edgehop)

```swift
final class PeerLink {
    private let queue = DispatchQueue(label: "edgehop.peer")
    private let connection: NWConnection
    private let handshake: Handshake
    private var buffer = Data()
    private var channel: SecureChannel?

    init(endpoint: NWEndpoint, identity: Identity, trust: TrustStore, pair: Bool) {
        connection = NWConnection(to: endpoint, using: .tcp)
        handshake = Handshake(options: HandshakeOptions(
            identity: identity, name: "Thiru's Mac", plat: "macos", trust: trust, pair: pair,
            showCode: { code, peer in /* show in UI for 120 s */ }), initiator: true)
    }

    func start() {
        connection.stateUpdateHandler = { [weak self] state in
            guard let self, case .ready = state else { return }
            if let hello = try? self.handshake.start() { self.sendFrame(hello) }
            self.receiveLoop()
        }
        connection.start(queue: queue)
    }

    private func sendFrame(_ payload: Data) {
        connection.send(content: FrameCodec.encode(payload), completion: .contentProcessed { _ in })
    }

    private func receiveLoop() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { [weak self] data, _, done, error in
            guard let self else { return }
            if let data { self.buffer.append(data); self.drain() }
            if done || error != nil { self.connection.cancel() } else { self.receiveLoop() }
        }
    }

    private func drain() {
        do {
            while let payload = try FrameCodec.decode(from: &buffer) {
                if let channel {
                    try handleEncrypted(try channel.open(payload))       // u8 type || body
                } else {
                    try handle(try handshake.receive(payload))
                }
            }
        } catch {
            connection.cancel()                                           // §3: close on any failure
        }
    }

    private func handle(_ step: HandshakeStep) throws {
        switch step {
        case .send(let data):
            sendFrame(data)
        case .established(let keys, _, let reply):
            if let reply { sendFrame(reply) }
            let channel = SecureChannel(keys: keys, isInitiator: true)
            self.channel = channel
            sendFrame(try channel.seal(Handshake.authFrame))          // acceptor answers auth_ok
        case .needCode(let peer):
            askUserForCode(peer) { [weak self] code in
                self?.queue.async { if let self, let step = try? self.handshake.resume(code: code) { try? self.handle(step) } }
            }
        case .failed(_, let reply):
            if let reply { sendFrame(reply) }
            connection.cancel()
        }
    }

    private func handleEncrypted(_ plaintext: Data) throws {
        if plaintext == Handshake.authOKFrame { handshake.confirmAuthenticated(); return }
        // dispatch on plaintext[0] (0x01 control JSON, 0x02 mouse_move, …)
    }
}
```

The acceptor side is the mirror image: `Handshake(…, initiator: false)`, no `start()`
payload, and on `.established` it sends `reply` (the `hello_ack`) and then waits for the
encrypted `auth`; if `open` throws, close and report `pair_failed`, otherwise reply with
`seal(Handshake.authOKFrame)` and call `confirmAuthenticated()`.

## Tests

`Tests/SharedTests` covers framing, WebSocket framing (RFC vectors), the HTTP parser and
responses, the static resolver, JSON helpers, `Shell`, the full handshake (trusted, pairing,
wrong code, unpaired rejection, tampering) and `SecureChannel`. `HandshakeVectorTests`
replays `shared/testdata/edgehop/handshake-vectors.json` from the Go implementation when the
file exists and skips otherwise. CI runs the suite on macOS and Linux (`shared-swift` and
`shared-swift-linux` jobs in `.github/workflows/build.yml`), and type-checks every source with
the same flags the app build uses.
