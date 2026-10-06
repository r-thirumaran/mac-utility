import Foundation

/// A side of the desktop union (`core.Edge` on the Go side). The raw values are the strings
/// carried in `active.enter.edge`.
enum Edge: String, Codable {

    case left
    case right
    case top
    case bottom

    /// Detection priority order (`core.Edges`).
    static let all: [Edge] = [.left, .right, .top, .bottom]

    /// The facing edge: left ⇄ right, top ⇄ bottom.
    var opposite: Edge {
        switch self {
        case .left: return .right
        case .right: return .left
        case .top: return .bottom
        case .bottom: return .top
        }
    }

    /// `true` for top/bottom, whose position along the edge is an x coordinate.
    var isHorizontal: Bool {
        self == .top || self == .bottom
    }
}
