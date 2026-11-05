import Foundation

/// The group's layout (`docs/edgehop-protocol.md` §6 `layout`, `core/layout.go`), version 2:
/// every machine is **placed on one shared canvas** by an integer offset
/// (`machines: [{id, x, y}]`, world units = the machine's native desktop units), and the old
/// tile grid (`tiles: [{id, col, row}]`) is kept alongside it so a v1 peer keeps working.
/// The newest `updatedAt` (ms since the Unix epoch) wins when layouts conflict.
///
/// A v2 sender fills BOTH lists. A receiver that sees `machines` uses it; a receiver that sees
/// only `tiles` (a v1 peer, or a config written before this release) converts them with
/// `upgraded(displays:)` and is then v2 internally.
struct Layout: Codable, Equatable {

    /// A machine's offset on the world canvas (spec §1). The world rect of display `d` of
    /// machine `m` is `(m.x + d.x, m.y + d.y, d.w, d.h)`.
    struct Placement: Codable, Equatable {
        var id: String
        var x: Int
        var y: Int

        init(id: String, x: Int, y: Int) {
            self.id = id
            self.x = x
            self.y = y
        }

        private enum CodingKeys: String, CodingKey {
            case id, x, y
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
            x = try container.decodeIfPresent(Int.self, forKey: .x) ?? 0
            y = try container.decodeIfPresent(Int.self, forKey: .y) ?? 0
        }
    }

    /// A machine's cell in the v1 grid.
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

    /// A layout that does not fit the rules (`core.Layout.Validate`).
    struct ValidationError: Error, CustomStringConvertible {
        let description: String
    }

    /// Columns and rows the v1 layout editor offered.
    static let gridSize = 5
    /// Spec §1: `|x|, |y| ≤ 1_000_000`.
    static let maxOffset = 1_000_000

    var updatedAt: Int64
    var machines: [Placement]
    var tiles: [Tile]

    init(updatedAt: Int64 = 0, machines: [Placement] = [], tiles: [Tile] = []) {
        self.updatedAt = updatedAt
        self.machines = machines
        self.tiles = tiles
    }

    // MARK: - Codable

    private enum CodingKeys: String, CodingKey {
        case updatedAt, machines, tiles
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        updatedAt = try container.decodeIfPresent(Int64.self, forKey: .updatedAt) ?? 0
        machines = try container.decodeIfPresent([Placement].self, forKey: .machines) ?? []
        tiles = try container.decodeIfPresent([Tile].self, forKey: .tiles) ?? []
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(updatedAt, forKey: .updatedAt)
        try container.encode(machines, forKey: .machines)
        try container.encode(tiles, forKey: .tiles)
    }

    // MARK: - Shape

    /// Whether this layout carries canvas placements (spec §4: `machines` present → use it).
    var isCanvas: Bool {
        !machines.isEmpty
    }

    var isEmpty: Bool {
        machines.isEmpty && tiles.isEmpty
    }

    var ids: [String] {
        var seen: Set<String> = []
        var out: [String] = []
        for m in machines where !m.id.isEmpty && seen.insert(m.id).inserted {
            out.append(m.id)
        }
        for t in tiles where !t.id.isEmpty && seen.insert(t.id).inserted {
            out.append(t.id)
        }
        return out
    }

    func placement(_ id: String) -> Placement? {
        machines.first(where: { $0.id == id })
    }

    func tile(_ id: String) -> Tile? {
        tiles.first(where: { $0.id == id })
    }

    func has(_ id: String) -> Bool {
        placement(id) != nil || tile(id) != nil
    }

    /// A copy without `id`.
    func without(_ id: String) -> Layout {
        Layout(updatedAt: updatedAt,
               machines: machines.filter { $0.id != id },
               tiles: tiles.filter { $0.id != id })
    }

    /// A copy with both lists sorted, so equal layouts compare equal.
    func normalized() -> Layout {
        let places = machines.sorted { a, b in
            if a.y != b.y { return a.y < b.y }
            if a.x != b.x { return a.x < b.x }
            return a.id < b.id
        }
        let cells = tiles.sorted { a, b in
            if a.row != b.row { return a.row < b.row }
            if a.col != b.col { return a.col < b.col }
            return a.id < b.id
        }
        return Layout(updatedAt: updatedAt, machines: places, tiles: cells)
    }

    // MARK: - Rules (spec §1)

    /// Structural rules that need no display information: no machine appears twice in either
    /// list, no cell is shared, offsets and cells are in range. Display overlap is checked by
    /// `validateOverlaps(displays:)`, which needs every machine's monitors.
    func validate() throws {
        var placed: Set<String> = []
        for m in machines {
            if m.id.isEmpty {
                throw ValidationError(description: "layout: placement without id")
            }
            if placed.contains(m.id) {
                throw ValidationError(description: "layout: \(m.id) appears twice")
            }
            placed.insert(m.id)
            if abs(m.x) > Layout.maxOffset || abs(m.y) > Layout.maxOffset {
                throw ValidationError(description: "layout: \(m.id) at (\(m.x),\(m.y)) is off the canvas")
            }
        }
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

    /// Spec §1: no two displays of DIFFERENT machines may overlap by a positive area.
    /// `displays` maps machine id → that machine's monitors in its own desktop coordinates;
    /// machines with no known displays cannot overlap anything and are skipped.
    func validateOverlaps(displays: [String: [Display]]) throws {
        let world = Geometry.World(layout: self, displays: displays)
        if let clash = world.firstOverlap() {
            throw ValidationError(description: "layout: \(clash.a) and \(clash.b) overlap on the canvas")
        }
    }

    // MARK: - Conversion (spec §4)

    /// Tiles derived for v1 peers: machines are grouped into rows by world-union y-overlap
    /// (rows ordered top to bottom), and inside a row they are ranked left to right by the
    /// x-centre of their union. Simple and deterministic; it only has to preserve
    /// left/right/up/down roughly.
    ///
    /// Worked example — A at (0,0) with one 2560×1440 display, B at (2560,0) with one
    /// 1920×1080 display, C at (0,1440) with one 1920×1080 display:
    /// A and B overlap in y (0…1080) so they share row 0, ranked by x-centre → A col 0,
    /// B col 1. C overlaps neither, so it is row 1 col 0.
    static func derivedTiles(machines: [Placement], displays: [String: [Display]]) -> [Tile] {
        struct Box {
            var id: String
            var top: Int
            var bottom: Int
            var centreX: Int
            var centreY: Int
        }
        var boxes: [Box] = []
        for m in machines where !m.id.isEmpty {
            let u = Geometry.union(displays[m.id] ?? [])
            if u.isEmpty {
                // Never-seen machine: a zero-size box at its offset still sorts sensibly.
                boxes.append(Box(id: m.id, top: m.y, bottom: m.y, centreX: m.x, centreY: m.y))
                continue
            }
            let top = m.y + u.y
            let bottom = top + u.h
            boxes.append(Box(id: m.id, top: top, bottom: bottom,
                             centreX: m.x + u.x + u.w / 2, centreY: top + u.h / 2))
        }
        // Rows: machines whose y-ranges overlap (transitively) share a row.
        boxes.sort { a, b in
            if a.top != b.top { return a.top < b.top }
            if a.centreY != b.centreY { return a.centreY < b.centreY }
            return a.id < b.id
        }
        var rows: [[Box]] = []
        var rowBottom = Int.min
        for box in boxes {
            if var row = rows.last, box.top < rowBottom {
                row.append(box)
                rows[rows.count - 1] = row
                rowBottom = max(rowBottom, box.bottom)
                continue
            }
            rows.append([box])
            rowBottom = box.bottom
        }
        var out: [Tile] = []
        for (row, members) in rows.enumerated() {
            let ranked = members.sorted { a, b in
                if a.centreX != b.centreX { return a.centreX < b.centreX }
                return a.id < b.id
            }
            for (col, box) in ranked.enumerated() {
                out.append(Tile(id: box.id, col: col, row: row))
            }
        }
        return out
    }

    /// Placements derived from a v1 grid: row by row (ascending), machines are laid out left to
    /// right by column with each union touching the previous one's right edge, and each row
    /// starts below the tallest union of the rows above it.
    ///
    /// Worked example — tiles A(col 0,row 0), B(col 1,row 0), C(col 0,row 1) with the displays
    /// of the example above: row 0 gets A at x = 0 (union 2560 wide) and B at x = 2560;
    /// row 1 starts at y = 1440 (A's union is the tallest in row 0) so C lands at (0,1440).
    static func derivedMachines(tiles: [Tile], displays: [String: [Display]]) -> [Placement] {
        var byRow: [Int: [Tile]] = [:]
        for t in tiles where !t.id.isEmpty {
            byRow[t.row, default: []].append(t)
        }
        var out: [Placement] = []
        var top = 0
        for row in byRow.keys.sorted() {
            let members = (byRow[row] ?? []).sorted { a, b in
                if a.col != b.col { return a.col < b.col }
                return a.id < b.id
            }
            var x = 0
            var tallest = 0
            for t in members {
                let u = Geometry.union(displays[t.id] ?? [])
                // Offset so the union's top-left lands exactly at (x, top).
                out.append(Placement(id: t.id, x: x - u.x, y: top - u.y))
                x += u.w
                tallest = max(tallest, u.h)
            }
            top += tallest
        }
        return out
    }

    /// Spec §4: a layout that arrived (or was loaded) with only `tiles` becomes a canvas
    /// layout; a canvas layout gets its `tiles` refreshed for v1 peers. Idempotent.
    func upgraded(displays: [String: [Display]]) -> Layout {
        var out = self
        if !isCanvas && !tiles.isEmpty {
            out.machines = Layout.derivedMachines(tiles: tiles, displays: displays)
        }
        out.tiles = Layout.derivedTiles(machines: out.machines, displays: displays)
        return out.normalized()
    }
}
