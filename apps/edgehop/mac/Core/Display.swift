import Foundation

/// One monitor in desktop-union coordinates — origin at the top-left of the primary display,
/// y down — exactly the `displays` entry of `docs/edgehop-protocol.md` §6 and `input.Display`
/// on the Go side. `CGDisplayBounds` already uses this space, so no AppKit flipping is needed.
///
/// Decoding is lenient (missing keys take their zero value) because Go omits nothing but a
/// hand-edited config or an older peer might.
struct Display: Codable, Equatable {

    var id: String
    var x: Int
    var y: Int
    var w: Int
    var h: Int
    var scale: Double
    var primary: Bool

    init(id: String, x: Int, y: Int, w: Int, h: Int, scale: Double = 1, primary: Bool = false) {
        self.id = id
        self.x = x
        self.y = y
        self.w = w
        self.h = h
        self.scale = scale
        self.primary = primary
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case id, x, y, w, h, scale, primary
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        x = try container.decodeIfPresent(Int.self, forKey: .x) ?? 0
        y = try container.decodeIfPresent(Int.self, forKey: .y) ?? 0
        w = try container.decodeIfPresent(Int.self, forKey: .w) ?? 0
        h = try container.decodeIfPresent(Int.self, forKey: .h) ?? 0
        scale = try container.decodeIfPresent(Double.self, forKey: .scale) ?? 1
        primary = try container.decodeIfPresent(Bool.self, forKey: .primary) ?? false
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(x, forKey: .x)
        try container.encode(y, forKey: .y)
        try container.encode(w, forKey: .w)
        try container.encode(h, forKey: .h)
        try container.encode(scale, forKey: .scale)
        try container.encode(primary, forKey: .primary)
    }
}
