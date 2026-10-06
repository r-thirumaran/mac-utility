import Foundation

/// The group's tile grid (`docs/edgehop-protocol.md` §6 `layout`, `core/layout.go`): one tile
/// per machine; neighbours are tiles directly adjacent on the left/right/top/bottom. The newest
/// `updatedAt` (ms since the Unix epoch) wins when layouts conflict.
struct Layout: Codable, Equatable {

    /// A machine's cell in the grid.
    struct Tile: Codable, Equatable {
        var id: String
        var col: Int
        var row: Int

        init(id: String, col: Int, row: Int) {
            self.id = id
            self.col = col
            self.row = row
        }

        private enum CodingKeys: String, CodingKey {
            case id, col, row
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
            col = try container.decodeIfPresent(Int.self, forKey: .col) ?? 0
            row = try container.decodeIfPresent(Int.self, forKey: .row) ?? 0
        }
    }

    /// A tile that does not fit the grid rules (`core.Layout.Validate`).
    struct ValidationError: Error, CustomStringConvertible {
        let description: String
    }

    /// Columns and rows the layout editor offers.
    static let gridSize = 5

    var updatedAt: Int64
    var tiles: [Tile]

    init(updatedAt: Int64 = 0, tiles: [Tile] = []) {
        self.updatedAt = updatedAt
        self.tiles = tiles
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case updatedAt, tiles
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        updatedAt = try container.decodeIfPresent(Int64.self, forKey: .updatedAt) ?? 0
        tiles = try container.decodeIfPresent([Tile].self, forKey: .tiles) ?? []
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(updatedAt, forKey: .updatedAt)
        try container.encode(tiles, forKey: .tiles)
    }

    // MARK: - Rules

    /// No machine and no cell appears twice; every cell is inside 0…63.
    func validate() throws {
        var ids: Set<String> = []
        var cells: [String: String] = [:]
        for t in tiles {
            if t.id.isEmpty {
                throw ValidationError(description: "layout: tile without id")
            }
            if ids.contains(t.id) {
                throw ValidationError(description: "layout: \(t.id) appears twice")
            }
            ids.insert(t.id)
            if t.col < 0 || t.row < 0 || t.col >= 64 || t.row >= 64 {
                throw ValidationError(description: "layout: \(t.id) at (\(t.col),\(t.row)) is outside the grid")
            }
            let cell = "\(t.col),\(t.row)"
            if let other = cells[cell] {
                throw ValidationError(description: "layout: \(other) and \(t.id) share cell (\(t.col),\(t.row))")
            }
            cells[cell] = t.id
        }
    }

    func tile(_ id: String) -> Tile? {
        tiles.first(where: { $0.id == id })
    }

    func has(_ id: String) -> Bool {
        tile(id) != nil
    }

    /// The machine directly adjacent to `id` on the given edge.
    func neighbour(of id: String, _ edge: Edge) -> String? {
        guard let t = tile(id) else { return nil }
        var col = t.col
        var row = t.row
        switch edge {
        case .left: col -= 1
        case .right: col += 1
        case .top: row -= 1
        case .bottom: row += 1
        }
        return tiles.first(where: { $0.col == col && $0.row == row && $0.id != id })?.id
    }

    /// A copy without `id`.
    func without(_ id: String) -> Layout {
        Layout(updatedAt: updatedAt, tiles: tiles.filter { $0.id != id })
    }

    /// A copy with the tiles sorted by row, column, id, so equal layouts compare equal.
    func normalized() -> Layout {
        let sorted = tiles.sorted { a, b in
            if a.row != b.row { return a.row < b.row }
            if a.col != b.col { return a.col < b.col }
            return a.id < b.id
        }
        return Layout(updatedAt: updatedAt, tiles: sorted)
    }
}
