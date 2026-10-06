import Foundation

/// Desktop geometry: the union of the displays, edge hits with corner dead zones, and the
/// entry point of an arriving cursor — the same rules as `core/geometry.go`
/// (`docs/edgehop-protocol.md` §6.1 and §6.3), so both implementations switch at the same
/// pixel and land the cursor on the same spot.
enum Geometry {

    /// A pixel position.
    struct Point: Equatable {
        var x: Int
        var y: Int

        init(x: Int, y: Int) {
            self.x = x
            self.y = y
        }

        static let zero = Point(x: 0, y: 0)
    }

    /// An axis-aligned pixel rectangle; `x + w` and `y + h` are exclusive.
    struct Rect: Equatable {
        var x: Int
        var y: Int
        var w: Int
        var h: Int

        static let zero = Rect(x: 0, y: 0, w: 0, h: 0)

        var isEmpty: Bool {
            w <= 0 || h <= 0
        }

        func contains(_ p: Point) -> Bool {
            p.x >= x && p.x < x + w && p.y >= y && p.y < y + h
        }
    }

    // MARK: - Union

    static func rect(of display: Display) -> Rect {
        Rect(x: display.x, y: display.y, w: display.w, h: display.h)
    }

    /// Bounding box of the displays (`.zero` when there are none).
    static func union(_ displays: [Display]) -> Rect {
        var result = Rect.zero
        var first = true
        for d in displays where d.w > 0 && d.h > 0 {
            if first {
                result = rect(of: d)
                first = false
                continue
            }
            let minX = min(result.x, d.x)
            let minY = min(result.y, d.y)
            let maxX = max(result.x + result.w, d.x + d.w)
            let maxY = max(result.y + result.h, d.y + d.h)
            result = Rect(x: minX, y: minY, w: maxX - minX, h: maxY - minY)
        }
        return result
    }

    /// The primary display, or the first one.
    static func primary(_ displays: [Display]) -> Display? {
        displays.first(where: { $0.primary }) ?? displays.first
    }

    /// Centre of the primary display.
    static func centre(_ displays: [Display]) -> Point {
        guard let d = primary(displays) else { return .zero }
        return Point(x: d.x + d.w / 2, y: d.y + d.h / 2)
    }

    // MARK: - Edge detection

    /// A half-open interval `[lo, hi)` along an edge.
    private struct Span {
        var lo: Int
        var hi: Int
    }

    /// Merged intervals (along the edge) that displays actually occupy on the given outer edge
    /// of `u`. Touching or overlapping ranges merge, so a corner is only where a merged run
    /// ends — a join between two monitors on one edge is not a corner.
    private static func edgeSpans(_ edge: Edge, _ u: Rect, _ displays: [Display]) -> [Span] {
        var spans: [Span] = []
        for d in displays where d.w > 0 && d.h > 0 {
            switch edge {
            case .left:
                if d.x == u.x { spans.append(Span(lo: d.y, hi: d.y + d.h)) }
            case .right:
                if d.x + d.w == u.x + u.w { spans.append(Span(lo: d.y, hi: d.y + d.h)) }
            case .top:
                if d.y == u.y { spans.append(Span(lo: d.x, hi: d.x + d.w)) }
            case .bottom:
                if d.y + d.h == u.y + u.h { spans.append(Span(lo: d.x, hi: d.x + d.w)) }
            }
        }
        spans.sort { $0.lo < $1.lo }
        var merged: [Span] = []
        for s in spans {
            if let last = merged.last, s.lo <= last.hi {
                if s.hi > last.hi {
                    merged[merged.count - 1].hi = s.hi
                }
                continue
            }
            merged.append(s)
        }
        return merged
    }

    /// Whether `p` touches edge `e` of `u`: left/top are the first pixel column/row
    /// (`p.x ≤ u.minX`), right/bottom the last one (`p.x ≥ u.maxX − 1`), where the OS clamps
    /// the hardware cursor.
    private static func onEdge(_ edge: Edge, _ u: Rect, _ p: Point) -> Bool {
        switch edge {
        case .left: return p.x <= u.x
        case .right: return p.x >= u.x + u.w - 1
        case .top: return p.y <= u.y
        case .bottom: return p.y >= u.y + u.h - 1
        }
    }

    /// §6.1 rule 2: `p` hits edge `e` of the union when it touches that outer edge AND a
    /// display owns that row (left/right) or column (top/bottom) there, excluding `deadZone`
    /// pixels at either end of the owning run (the corners). `frac` is the position along the
    /// union's edge. Edges are tested left, right, top, bottom; the first hit wins.
    static func edgeHit(_ p: Point, displays: [Display], deadZone: Int) -> (edge: Edge, frac: Double)? {
        let u = union(displays)
        guard !u.isEmpty else { return nil }
        let zone = max(deadZone, 0)
        for edge in Edge.all {
            guard onEdge(edge, u, p) else { continue }
            let along = edge.isHorizontal ? p.x : p.y
            for s in edgeSpans(edge, u, displays) {
                if along < s.lo || along >= s.hi { continue }
                if along < s.lo + zone || along >= s.hi - zone {
                    return nil // corner dead zone
                }
                let frac: Double
                if edge.isHorizontal {
                    frac = Double(p.x - u.x) / Double(u.w)
                } else {
                    frac = Double(p.y - u.y) / Double(u.h)
                }
                return (edge, clamp01(frac))
            }
        }
        return nil
    }

    // MARK: - Entry point

    /// Where the cursor appears on a machine with `displays` when it arrives on edge `e` at
    /// fraction `frac` of the union (§6.1 rule 4): on that edge, inset one pixel, snapped onto
    /// a real display if the union has a gap there.
    static func entryPoint(edge: Edge, frac: Double, displays: [Display]) -> Point {
        let u = union(displays)
        guard !u.isEmpty else { return .zero }
        let f = clamp01(frac)
        var p: Point
        switch edge {
        case .left:
            p = Point(x: u.x + 1, y: u.y + Int((f * Double(u.h)).rounded()))
        case .right:
            p = Point(x: u.x + u.w - 2, y: u.y + Int((f * Double(u.h)).rounded()))
        case .top:
            p = Point(x: u.x + Int((f * Double(u.w)).rounded()), y: u.y + 1)
        case .bottom:
            p = Point(x: u.x + Int((f * Double(u.w)).rounded()), y: u.y + u.h - 2)
        }
        // Keep the inset point inside the union even for 1 px wide unions.
        p.x = min(max(p.x, u.x), u.x + u.w - 1)
        p.y = min(max(p.y, u.y), u.y + u.h - 1)
        return snapToDisplays(p, displays: displays)
    }

    /// `p` when a display contains it, otherwise the nearest point inside any display.
    static func snapToDisplays(_ p: Point, displays: [Display]) -> Point {
        var best = p
        var bestDistance = Double.infinity
        for d in displays {
            let r = rect(of: d)
            if r.isEmpty { continue }
            if r.contains(p) { return p }
            let q = Point(x: min(max(p.x, r.x), r.x + r.w - 1), y: min(max(p.y, r.y), r.y + r.h - 1))
            let dx = Double(q.x - p.x)
            let dy = Double(q.y - p.y)
            let distance = dx * dx + dy * dy
            if distance < bestDistance {
                best = q
                bestDistance = distance
            }
        }
        return best
    }

    /// Keeps `p` inside the union of the displays, like the OS does for the hardware cursor.
    static func clamp(_ p: Point, to displays: [Display]) -> Point {
        let u = union(displays)
        guard !u.isEmpty else { return p }
        return Point(x: min(max(p.x, u.x), u.x + u.w - 1), y: min(max(p.y, u.y), u.y + u.h - 1))
    }

    static func clamp01(_ f: Double) -> Double {
        if f.isNaN || f < 0 { return 0 }
        if f > 1 { return 1 }
        return f
    }
}
