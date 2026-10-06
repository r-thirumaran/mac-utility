import Foundation

/// One connection's screen stream: capture → scale → encode → send at `fps` on its own
/// serial queue (`docs/reach-api.md` §4).
///
/// Back-pressure is structural: the timer runs on the same serial queue the frame is produced
/// on, so a tick that arrives while a frame is still being captured or encoded is coalesced
/// by the source rather than queued, and a frame is never handed to the socket while the
/// previous one is still being built. A slow capture therefore lowers the rate instead of
/// piling frames up. Five consecutive capture failures end the stream, like the Go agent.
///
/// REVIEW: unlike the Go agent (whose send blocks), `socket.send(binary:)` only enqueues on the
/// NWConnection, so a phone that stops reading lets frames pile up in the connection's send
/// buffer (~0.5–1 MB/s) until the socket dies. Bounding that needs a send-completion hook on
/// the shared `WebSocketConnection`; left as is because it cannot crash and frees on close.
final class ScreenStreamer {

    static let maxFailures = 5

    private let queue = DispatchQueue(label: "reach.stream", qos: .userInitiated)
    private let display: Int
    private let maxWidth: Int
    private let quality: Double
    private let fps: Int
    private let send: (Data) -> Void

    private var timer: DispatchSourceTimer?
    private var producing = false
    private var failures = 0
    private var stopped = false

    /// `send` receives each complete binary frame on the stream's queue.
    init(display: Int, maxWidth: Int, quality: Double, fps: Int, send: @escaping (Data) -> Void) {
        self.display = display
        self.maxWidth = maxWidth
        self.quality = quality
        self.fps = max(ScreenCapture.minFPS, min(fps, ScreenCapture.maxFPS))
        self.send = send
    }

    // MARK: - Lifecycle

    func start() {
        queue.async { [self] in
            guard timer == nil, !stopped else { return }
            let interval = 1.0 / Double(fps)
            let source = DispatchSource.makeTimerSource(queue: queue)
            source.schedule(deadline: .now() + interval, repeating: interval, leeway: .milliseconds(10))
            source.setEventHandler { [weak self] in
                self?.tick()
            }
            source.resume()
            timer = source
        }
    }

    /// Ends the loop. Safe from any thread; a frame in flight finishes first.
    func stop() {
        queue.async { [self] in
            stopped = true
            timer?.cancel()
            timer = nil
        }
    }

    // MARK: - Frames (stream queue)

    private func tick() {
        guard !stopped, !producing else { return }
        producing = true
        defer { producing = false }
        do {
            let frame = try ScreenCapture.capture(index: display, maxWidth: maxWidth, quality: quality)
            failures = 0
            send(ScreenCapture.frame(display: display, width: frame.width, height: frame.height, jpeg: frame.jpeg))
        } catch {
            failures += 1
            Log.error("stream: capture display \(display) failed: \(ReachError.wrap(error).message)", category: "screen")
            if failures >= ScreenStreamer.maxFailures {
                stopped = true
                timer?.cancel()
                timer = nil
            }
        }
    }
}
