import Foundation

/// Error raised by `FrameCodec.decode` when a peer announces a frame above `maxLength`.
enum FrameError: Error, Equatable {
    case tooLarge
}

/// Length-prefixed frames, `docs/edgehop-protocol.md` §3:
///
/// ```
/// u32 big-endian length N   (N ≤ 1 MiB)
/// N bytes payload
/// ```
///
/// `decode` is written against a caller-owned receive buffer so it slots straight into an
/// `NWConnection.receive` loop: append whatever arrived, then call `decode` until it returns
/// `nil`. Every index is taken relative to `buffer.startIndex`, so it is safe to pass a `Data`
/// slice as well as a freshly allocated buffer.
enum FrameCodec {

    /// Largest payload a frame may carry. Mirrors `framing.MaxFrame` on the Go side: a payload
    /// of exactly this size is valid, one byte more is not.
    static let maxLength = 1 << 20

    /// Size of the length prefix.
    static let headerLength = 4

    // MARK: - Encoding

    /// Prefixes `payload` with its big-endian length. Callers are expected to respect
    /// `maxLength`; a receiver would reject anything larger.
    static func encode(_ payload: Data) -> Data {
        assert(payload.count <= maxLength, "FrameCodec: payload exceeds maxLength")
        let count = UInt32(truncatingIfNeeded: payload.count)
        var out = Data(capacity: headerLength + payload.count)
        out.append(UInt8(truncatingIfNeeded: count >> 24))
        out.append(UInt8(truncatingIfNeeded: count >> 16))
        out.append(UInt8(truncatingIfNeeded: count >> 8))
        out.append(UInt8(truncatingIfNeeded: count))
        out.append(payload)
        return out
    }

    // MARK: - Decoding

    /// Removes and returns one complete frame's payload from the front of `buffer`.
    ///
    /// - Returns: `nil` when the buffer does not yet hold a whole frame (nothing is consumed).
    /// - Throws: `FrameError.tooLarge` when the announced length exceeds `maxLength`. The
    ///   header is left in the buffer; the caller must close the connection.
    static func decode(from buffer: inout Data) throws -> Data? {
        guard buffer.count >= headerLength else { return nil }

        let start = buffer.startIndex
        let length = (UInt32(buffer[start]) << 24)
            | (UInt32(buffer[start + 1]) << 16)
            | (UInt32(buffer[start + 2]) << 8)
            | UInt32(buffer[start + 3])
        guard length <= UInt32(maxLength) else { throw FrameError.tooLarge }

        let total = headerLength + Int(length)
        guard buffer.count >= total else { return nil }

        let payload = Data(buffer[(start + headerLength)..<(start + total)])
        buffer.removeSubrange(start..<(start + total))
        return payload
    }
}
