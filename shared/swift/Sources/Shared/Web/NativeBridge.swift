#if canImport(WebKit)
import Foundation
import WebKit

/// The macOS side of `shared/ui/native.js` (`docs/ARCHITECTURE.md` §4).
///
/// The page calls `window.webkit.messageHandlers.native.postMessage({id, method, params})`
/// and gets a promise; this object looks the method up, runs the handler and resolves the
/// promise with `{id, result}` or `{id, error: {code, message}}`. Events go the other way
/// through `emit`, which evaluates `window.__nativeEvent({event, data})` in the page.
///
/// Handler results and event data must be JSON-serialisable (`[String: Any]`, `[Any]`,
/// `String`, numbers, `Bool`, `NSNull`); run Codable models through `jsonObject(_:)` first.
/// Everything runs on the main actor; async handlers may complete on any queue.
@MainActor
final class NativeBridge: NSObject, WKScriptMessageHandlerWithReply {

    typealias Handler = ([String: Any]) throws -> Any?
    typealias AsyncHandler = ([String: Any], @escaping (Result<Any?, Error>) -> Void) -> Void

    /// `window.webkit.messageHandlers.native`.
    static let messageHandlerName = "native"

    /// The web view `emit` targets; set by `WebWindowController`.
    weak var webView: WKWebView?

    private var handlers: [String: Handler] = [:]
    private var asyncHandlers: [String: AsyncHandler] = [:]

    // MARK: - Registration

    /// Registers a synchronous handler. Replaces any previous handler for `method`.
    func register(_ method: String, _ handler: @escaping Handler) {
        handlers[method] = handler
        asyncHandlers[method] = nil
    }

    /// Registers a handler that completes later (admin prompts, network, permissions).
    func registerAsync(_ method: String, _ handler: @escaping AsyncHandler) {
        asyncHandlers[method] = handler
        handlers[method] = nil
    }

    /// Every registered method name.
    var methods: [String] {
        Array(handlers.keys) + Array(asyncHandlers.keys)
    }

    /// Installs this bridge as the `native` message handler of a web view configuration.
    func attach(to configuration: WKWebViewConfiguration) {
        configuration.userContentController.addScriptMessageHandler(
            self, contentWorld: .page, name: NativeBridge.messageHandlerName)
    }

    func detach(from configuration: WKWebViewConfiguration) {
        configuration.userContentController.removeScriptMessageHandler(
            forName: NativeBridge.messageHandlerName, contentWorld: .page)
    }

    // MARK: - Events

    /// Pushes `{event, data}` to the page. Silently dropped when no web view is attached.
    func emit(_ event: String, _ data: Any) {
        guard let webView else { return }
        let envelope: [String: Any] = ["event": event, "data": data]
        guard let json = try? JSONValue.string(envelope) else {
            Log.error("NativeBridge: event \(event) is not JSON-serialisable", category: "bridge")
            return
        }
        webView.evaluateJavaScript("window.__nativeEvent(" + json + ")") { _, error in
            if let error {
                Log.error("NativeBridge: emit \(event) failed: \(error)", category: "bridge")
            }
        }
    }

    // MARK: - WKScriptMessageHandlerWithReply

    func userContentController(_ userContentController: WKUserContentController,
                               didReceive message: WKScriptMessage) async -> (Any?, String?) {
        guard let request = message.body as? [String: Any],
              let method = request["method"] as? String else {
            let error = BridgeError(.invalidParams, "malformed request")
            return (NativeBridge.errorReply(id: NSNull(), error), nil)
        }
        let id: Any = request["id"] ?? NSNull()
        let params = request["params"] as? [String: Any] ?? [:]

        do {
            let value = try await invoke(method, params)
            return (NativeBridge.resultReply(id: id, value), nil)
        } catch {
            return (NativeBridge.errorReply(id: id, error), nil)
        }
    }

    private func invoke(_ method: String, _ params: [String: Any]) async throws -> Any? {
        if let handler = handlers[method] {
            return try handler(params)
        }
        if let handler = asyncHandlers[method] {
            return try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Any?, Error>) in
                handler(params) { result in
                    continuation.resume(with: result)
                }
            }
        }
        throw BridgeError(.notSupported, "unknown method \(method)")
    }

    // MARK: - Replies

    nonisolated static func resultReply(id: Any, _ value: Any?) -> [String: Any] {
        ["id": id, "result": value ?? NSNull()]
    }

    nonisolated static func errorReply(id: Any, _ error: Error) -> [String: Any] {
        ["id": id, "error": BridgeError.wrap(error).asDictionary]
    }
}
#endif
