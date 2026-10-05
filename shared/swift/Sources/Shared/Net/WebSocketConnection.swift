#if canImport(Network)
import Foundation

/// A WebSocket accepted by `HTTPServer`, handed to `onWebSocket`.
///
/// Callbacks fire on the main actor; `send` / `close` may be called from anywhere. The
/// server answers pings itself. Keep a strong reference for as long as you need the socket —
/// dropping it does not close the connection, the peer going away does (`onClose`).
final class WebSocketConnection {

    /// `host:port` of the peer.
    let remoteDescription: String

    var onText: ((String) -> Void)?
    var onBinary: ((Data) -> Void)?
    /// Fired exactly once, after the connection is gone for any reason.
    var onClose: (() -> Void)?

    private let transport: HTTPConnection

    init(transport: HTTPConnection) {
        self.transport = transport
        self.remoteDescription = transport.remoteDescription
    }

    // MARK: - Sending

    func send(text: String) {
        transport.enqueueFrame(.text(text))
    }

    func send(binary: Data) {
        transport.enqueueFrame(.binary(binary))
    }

    /// Sends a close frame; `onClose` follows once the connection is down.
    func close(code: UInt16 = 1000, reason: String = "") {
        transport.enqueueClose(code: code, reason: reason)
    }
}
#endif
