import Foundation
#if canImport(CryptoKit)
import CryptoKit
#else
import Crypto
#endif

/// Errors raised by `WebSocketFrame.decode`. Every one of them means the peer violated
/// RFC 6455 and the connection should be closed (status 1002 / 1009).
enum WebSocketFrameError: Error, Equatable {
    /// Payload longer than `WebSocketFrame.maxPayloadLength`.
    case tooLarge
    /// A client frame arrived unmasked while `requireMask` was set (RFC 6455 §5.1).
    case unmaskedClientFrame
    /// RSV1–3 set without a negotiated extension.
    case reservedBitsSet
    /// Opcode outside 0x0–0x2 and 0x8–0xA.
    case unknownOpcode(UInt8)
    /// Control frame fragmented or longer than 125 bytes (§5.5).
    case invalidControlFrame
}

/// One WebSocket frame (RFC 6455 §5.2), with the encoder/decoder as static functions so the
/// type is Foundation-only and testable on Linux. Extensions (RSV bits, compression) are not
/// supported and are rejected.
struct WebSocketFrame: Equatable {

    enum Opcode: UInt8 {
        case continuation = 0x0
        case text = 0x1
        case binary = 0x2
        case close = 0x8
        case ping = 0x9
        case pong = 0xA

        /// Control frames must not be fragmented and carry at most 125 bytes.
        var isControl: Bool { rawValue >= 0x8 }
    }

    var opcode: Opcode
    var fin: Bool
    var payload: Data

    init(opcode: Opcode, fin: Bool = true, payload: Data = Data()) {
        self.opcode = opcode
        self.fin = fin
        self.payload = payload
    }

    /// Largest payload accepted by `decode`: 1 MiB, the same ceiling as `FrameCodec`.
    static let maxPayloadLength = 1 << 20

    /// The GUID every server appends to the client key (§1.3).
    static let acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

    // MARK: - Convenience constructors

    static func text(_ string: String) -> WebSocketFrame {
        WebSocketFrame(opcode: .text, payload: Data(string.utf8))
    }

    static func binary(_ data: Data) -> WebSocketFrame {
        WebSocketFrame(opcode: .binary, payload: data)
    }

    /// A close frame with a 2-byte status code followed by an optional UTF-8 reason.
    static func close(code: UInt16 = 1000, reason: String = "") -> WebSocketFrame {
        var payload = Data()
        payload.append(UInt8(truncatingIfNeeded: code >> 8))
        payload.append(UInt8(truncatingIfNeeded: code))
        payload.append(Data(reason.utf8))
        return WebSocketFrame(opcode: .close, payload: payload)
    }

    /// The status code carried by a close frame, if it has one.
    var closeCode: UInt16? {
        guard opcode == .close, payload.count >= 2 else { return nil }
        let start = payload.startIndex
        return UInt16(payload[start]) << 8 | UInt16(payload[start + 1])
    }

    /// The payload decoded as UTF-8 (text and close-reason frames).
    var textPayload: String? {
        String(data: payload, encoding: .utf8)
    }

    // MARK: - Handshake

    /// `base64(SHA1(clientKey + GUID))` — the value of the `Sec-WebSocket-Accept` header (§4.2.2).
    static func acceptKey(for clientKey: String) -> String {
        let digest = Insecure.SHA1.hash(data: Data((clientKey + acceptGUID).utf8))
        return Data(digest).base64EncodedString()
    }

    // MARK: - Encoding

    /// Serialises a frame. Servers send unmasked frames; clients MUST mask (`masked: true`),
    /// in which case `maskingKey` is used verbatim (tests) or generated at random.
    static func encode(_ frame: WebSocketFrame, masked: Bool = false, maskingKey: UInt32? = nil) -> Data {
        var out = Data(capacity: frame.payload.count + 14)
        out.append((frame.fin ? 0x80 : 0x00) | frame.opcode.rawValue)

        let count = frame.payload.count
        let maskBit: UInt8 = masked ? 0x80 : 0x00
        if count < 126 {
            out.append(maskBit | UInt8(truncatingIfNeeded: count))
        } else if count <= 0xFFFF {
            out.append(maskBit | 126)
            out.append(UInt8(truncatingIfNeeded: count >> 8))
            out.append(UInt8(truncatingIfNeeded: count))
        } else {
            out.append(maskBit | 127)
            let length = UInt64(count)
            for shift in stride(from: 56, through: 0, by: -8) {
                out.append(UInt8(truncatingIfNeeded: length >> UInt64(shift)))
            }
        }

        if masked {
            let key = maskingKey ?? UInt32.random(in: UInt32.min...UInt32.max)
            let keyBytes: [UInt8] = [
                UInt8(truncatingIfNeeded: key >> 24),
                UInt8(truncatingIfNeeded: key >> 16),
                UInt8(truncatingIfNeeded: key >> 8),
                UInt8(truncatingIfNeeded: key),
            ]
            out.append(contentsOf: keyBytes)
            var index = 0
            for byte in frame.payload {
                out.append(byte ^ keyBytes[index & 3])
                index += 1
            }
        } else {
            out.append(frame.payload)
        }
        return out
    }

    // MARK: - Decoding

    /// Removes and returns one complete frame from the front of `buffer`.
    ///
    /// - Parameter requireMask: `true` on a server, where every client frame must be masked.
    /// - Returns: `nil` when the buffer does not yet hold a whole frame (nothing is consumed).
    static func decode(from buffer: inout Data, requireMask: Bool = false) throws -> WebSocketFrame? {
        let available = buffer.count
        guard available >= 2 else { return nil }
        let start = buffer.startIndex

        let first = buffer[start]
        let second = buffer[start + 1]
        let fin = first & 0x80 != 0
        guard first & 0x70 == 0 else { throw WebSocketFrameError.reservedBitsSet }
        guard let opcode = Opcode(rawValue: first & 0x0F) else {
            throw WebSocketFrameError.unknownOpcode(first & 0x0F)
        }
        let masked = second & 0x80 != 0

        // Payload length: 7 bits, or 16 / 64 bits big-endian when the 7-bit value is 126 / 127.
        var length = UInt64(second & 0x7F)
        var offset = 2
        if length == 126 {
            guard available >= 4 else { return nil }
            length = UInt64(buffer[start + 2]) << 8 | UInt64(buffer[start + 3])
            offset = 4
        } else if length == 127 {
            guard available >= 10 else { return nil }
            length = 0
            for i in 0..<8 {
                length = length << 8 | UInt64(buffer[start + 2 + i])
            }
            offset = 10
        }
        guard length <= UInt64(maxPayloadLength) else { throw WebSocketFrameError.tooLarge }
        let payloadLength = Int(length)

        if opcode.isControl && (!fin || payloadLength > 125) {
            throw WebSocketFrameError.invalidControlFrame
        }
        if !masked && requireMask {
            throw WebSocketFrameError.unmaskedClientFrame
        }

        let maskOffset = offset
        if masked { offset += 4 }
        let total = offset + payloadLength
        guard available >= total else { return nil }

        var payload = Data(buffer[(start + offset)..<(start + total)])
        if masked {
            let key: [UInt8] = [
                buffer[start + maskOffset],
                buffer[start + maskOffset + 1],
                buffer[start + maskOffset + 2],
                buffer[start + maskOffset + 3],
            ]
            payload.withUnsafeMutableBytes { (raw: UnsafeMutableRawBufferPointer) in
                for i in 0..<raw.count {
                    raw[i] ^= key[i & 3]
                }
            }
        }

        buffer.removeSubrange(start..<(start + total))
        return WebSocketFrame(opcode: opcode, fin: fin, payload: payload)
    }
}
