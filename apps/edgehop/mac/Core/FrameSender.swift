import Foundation

/// What the node needs from the network layer (`core.Sender`); `Transport` implements it.
/// Frames are encrypted payloads, `u8 type || body`, before sealing.
@MainActor
protocol FrameSender: AnyObject {
    /// Queues one frame for `peerID`; `false` when it is not connected or its queue is full.
    @discardableResult
    func send(_ frame: Data, to peerID: String) -> Bool
    /// Queues the frame for every connected peer.
    func broadcast(_ frame: Data)
    /// Drops the connection to `peerID` (it may reconnect later).
    func disconnect(_ peerID: String)
}
