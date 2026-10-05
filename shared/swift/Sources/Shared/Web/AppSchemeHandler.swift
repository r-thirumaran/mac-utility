#if canImport(WebKit)
import Foundation
import WebKit

/// Serves `app://ui/<path>` from a directory (the bundle's `Resources/ui`).
///
/// Loading the UI from `file://` would break ES-module imports and `fetch`; a custom scheme
/// gives the page a proper origin, correct MIME types and `index.html` for `/`. Files are read
/// synchronously — the UI is a handful of small local files and WebKit requires the
/// `WKURLSchemeTask` calls on the main thread anyway.
@MainActor
final class AppSchemeHandler: NSObject, WKURLSchemeHandler {

    static let scheme = "app"
    static let host = "ui"

    /// `app://ui/<path>`; `path` may or may not start with `/`.
    static func url(forPath path: String) -> URL? {
        var components = URLComponents()
        components.scheme = scheme
        components.host = host
        components.path = path.hasPrefix("/") ? path : "/" + path
        return components.url
    }

    private let root: URL

    init(root: URL) {
        self.root = root
        super.init()
    }

    // MARK: - WKURLSchemeHandler

    func webView(_ webView: WKWebView, start urlSchemeTask: WKURLSchemeTask) {
        guard let url = urlSchemeTask.request.url,
              url.scheme?.lowercased() == AppSchemeHandler.scheme else {
            urlSchemeTask.didFailWithError(URLError(.badURL))
            return
        }

        let path = url.path.isEmpty ? "/" : url.path
        guard let file = StaticFileResolver.resolve(path: path, under: root),
              let data = try? Data(contentsOf: file) else {
            respond(urlSchemeTask, url: url, status: 404, body: Data("not found".utf8),
                    contentType: "text/plain; charset=utf-8")
            return
        }
        respond(urlSchemeTask, url: url, status: 200, body: data,
                contentType: HTTPResponse.mimeType(forExtension: file.pathExtension))
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: WKURLSchemeTask) {
        // Responses are delivered synchronously inside `start`; nothing is in flight.
    }

    private func respond(_ task: WKURLSchemeTask, url: URL, status: Int, body: Data, contentType: String) {
        let headers = [
            "Content-Type": contentType,
            "Content-Length": String(body.count),
            "Cache-Control": "no-cache",
            "Access-Control-Allow-Origin": "*",
        ]
        guard let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1",
                                             headerFields: headers) else {
            task.didFailWithError(URLError(.cannotParseResponse))
            return
        }
        task.didReceive(response)
        task.didReceive(body)
        task.didFinish()
    }
}
#endif
