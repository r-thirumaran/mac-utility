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
    struct Span: Equatable {
        var lo: Int
        var hi: Int

        var length: Int {
            max(hi - lo, 0)
        }

        func contains(_ v: Int) -> Bool {
            v >= lo && v < hi
        }

        /// 0 when `v` is inside, otherwise the gap to the nearer end.
        func distance(to v: Int) -> Int {
            if v < lo { return lo - v }
            if v >= hi { return v - hi + 1 }
            return 0
        }
    }

    /// Merged intervals (along the edge) that displays actually occupy on the given outer edge
    /// of `u`. Touching or overlapping ranges merge, so a corner is only where a merged run
    /// ends — a join between two monitors on one edge is not a corner.
    static func edgeSpans(_ edge: Edge, _ u: Rect, _ displays: [Display]) -> [Span] {
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

    // MARK: - World canvas (spec §1–§3)

    /// Spec §2: two displays touch when their facing edges are within this many world units.
    static let touchTolerance = 2
    /// Spec §3 hotkeys: the half-angle of the cone searched when no boundary exists.
    static let hotkeyConeCosine = 0.5 // cos 60°

    /// One edge where a local display meets a display of another machine (spec §2). `span`
    /// is the overlap along the edge in WORLD coordinates (y for left/right, x for top/bottom).
    struct Boundary: Equatable {
        /// The edge of the local display `display` that the neighbour sits against.
        var edge: Edge
        /// Local display id.
        var display: String
        /// The machine on the other side.
        var peer: String
        /// That machine's display id.
        var peerDisplay: String
        var span: Span
    }

    /// Every machine placed on the shared canvas (spec §1): its offset and the displays it
    /// last reported, in its own desktop coordinates. The world rect of display `d` of
    /// machine `m` is `(m.x + d.x, m.y + d.y, d.w, d.h)`.
    ///
    /// Pure value type — build one from the layout plus the known display lists and ask it
    /// questions; nothing here touches the node, so every rule below is testable on its own.
    struct World {

        struct Member {
            var id: String
            var offset: Point
            var displays: [Display]
        }

        var members: [Member]

        init(members: [Member]) {
            self.members = members
        }

        /// The machines of `layout` that have a placement, with the displays known for them
        /// (`[]` when a peer has never been seen).
        init(layout: Layout, displays: [String: [Display]]) {
            members = layout.machines
                .filter { !$0.id.isEmpty }
                .map { Member(id: $0.id, offset: Point(x: $0.x, y: $0.y), displays: displays[$0.id] ?? []) }
        }

        func member(_ id: String) -> Member? {
            members.first(where: { $0.id == id })
        }

        func offset(of id: String) -> Point? {
            member(id)?.offset
        }

        /// The machine's displays as world rects, paired with the display they came from.
        func rects(of id: String) -> [(display: Display, rect: Rect)] {
            guard let m = member(id) else { return [] }
            return World.rects(of: m)
        }

        static func rects(of m: Member) -> [(display: Display, rect: Rect)] {
            m.displays
                .filter { $0.w > 0 && $0.h > 0 }
                .map { ($0, Rect(x: m.offset.x + $0.x, y: m.offset.y + $0.y, w: $0.w, h: $0.h)) }
        }

        /// Bounding box of the machine's displays on the canvas (`.zero` when unknown).
        func union(of id: String) -> Rect {
            guard let m = member(id) else { return .zero }
            let u = Geometry.union(m.displays)
            if u.isEmpty { return .zero }
            return Rect(x: m.offset.x + u.x, y: m.offset.y + u.y, w: u.w, h: u.h)
        }

        /// Spec §1 validation: the first pair of DIFFERENT machines whose displays overlap by
        /// a positive area, or `nil` when the canvas is valid.
        func firstOverlap() -> (a: String, b: String)? {
            let all = members.map { (id: $0.id, rects: World.rects(of: $0).map { $0.rect }) }
            for i in all.indices {
                for j in all.indices where j > i {
                    for ra in all[i].rects {
                        for rb in all[j].rects where Geometry.intersects(ra, rb) {
                            return (all[i].id, all[j].id)
                        }
                    }
                }
            }
            return nil
        }

        /// Spec §2: every boundary segment of `id`, per local display and edge. Recompute
        /// whenever the layout or anyone's displays change.
        ///
        /// Worked example — A (offset 0,0) has one 2560×1440 display and B (offset 2560,180)
        /// one 1920×1080 display. `A.right = 2560`, `B.left = 2560`, so they touch, and the
        /// y-overlap is 180…1260: one boundary `{edge: .right, span: 180…1260, peer: B}`.
        /// Nudging B to (2563,180) leaves |2560 − 2563| = 3 > 2, so there is no boundary and
        /// the cursor simply stops at A's right edge.
        func boundaries(of id: String) -> [Boundary] {
            guard let me = member(id) else { return [] }
            let mine = World.rects(of: me)
            guard !mine.isEmpty else { return [] }
            var out: [Boundary] = []
            for other in members where other.id != id {
                for (theirDisplay, rb) in World.rects(of: other) {
                    for (myDisplay, ra) in mine {
                        let vertical = Span(lo: max(ra.y, rb.y), hi: min(ra.y + ra.h, rb.y + rb.h))
                        let horizontal = Span(lo: max(ra.x, rb.x), hi: min(ra.x + ra.w, rb.x + rb.w))
                        if vertical.length >= 1 {
                            if abs((ra.x + ra.w) - rb.x) <= Geometry.touchTolerance {
                                out.append(Boundary(edge: .right, display: myDisplay.id, peer: other.id,
                                                    peerDisplay: theirDisplay.id, span: vertical))
                            }
                            if abs(ra.x - (rb.x + rb.w)) <= Geometry.touchTolerance {
                                out.append(Boundary(edge: .left, display: myDisplay.id, peer: other.id,
                                                    peerDisplay: theirDisplay.id, span: vertical))
                            }
                        }
                        if horizontal.length >= 1 {
                            if abs((ra.y + ra.h) - rb.y) <= Geometry.touchTolerance {
                                out.append(Boundary(edge: .bottom, display: myDisplay.id, peer: other.id,
                                                    peerDisplay: theirDisplay.id, span: horizontal))
                            }
                            if abs(ra.y - (rb.y + rb.h)) <= Geometry.touchTolerance {
                                out.append(Boundary(edge: .top, display: myDisplay.id, peer: other.id,
                                                    peerDisplay: theirDisplay.id, span: horizontal))
                            }
                        }
                    }
                }
            }
            return out
        }
    }

    /// Positive-area intersection.
    static func intersects(_ a: Rect, _ b: Rect) -> Bool {
        max(a.x, b.x) < min(a.x + a.w, b.x + b.w) && max(a.y, b.y) < min(a.y + a.h, b.y + b.h)
    }

    /// Where a pointer leaves one machine and arrives on another (spec §3).
    struct Hop: Equatable {
        /// The edge of the local desktop the pointer left by.
        var edge: Edge
        /// Position along the local union's edge, for v1 receivers (`enter.frac`).
        var frac: Double
        var target: String
        /// The display it arrives on.
        var targetDisplay: String
        /// `enter.pt`: the arrival point in the target's own desktop coordinates.
        var point: Point
    }

    /// The display that owns `p`, else the nearest one.
    static func display(containing p: Point, in displays: [Display]) -> Display? {
        var best: Display?
        var bestDistance = Double.infinity
        for d in displays where d.w > 0 && d.h > 0 {
            let r = rect(of: d)
            if r.contains(p) { return d }
            let q = Point(x: min(max(p.x, r.x), r.x + r.w - 1), y: min(max(p.y, r.y), r.y + r.h - 1))
            let dx = Double(q.x - p.x)
            let dy = Double(q.y - p.y)
            let distance = dx * dx + dy * dy
            if distance < bestDistance {
                best = d
                bestDistance = distance
            }
        }
        return best
    }

    /// Spec §3: the hop for the pointer sitting at local point `p` on machine `id`, or `nil`
    /// when it is not on an outer edge, is in a dead zone, or no boundary faces it.
    ///
    /// Worked example — A (offset 0,0, one 2560×1440 display) with B (offset 2560,180, one
    /// 1920×1080 display), dead zone 32. The pointer at A-local (2559, 700) is on A's right
    /// edge; world x = 2559, world y = 700, which is inside the boundary span 180…1260 and
    /// more than 32 from either end, so it hops. `q = (2559 − 2560, 700 − 180) = (−1, 520)`,
    /// then snapped onto B's left edge inset 1 px → `enter.pt = (1, 520)`.
    /// At A-local (2559, 200) the along-coordinate is 200, within 32 of the segment's low end
    /// 180 — but 180 is NOT a corner of A's merged right-edge run (0…1440), so the extra dead
    /// zone of §3 step 2 does not apply and the hop still happens.
    /// At A-local (2559, 1430) the pointer is within 32 of A's own run end 1440, so the
    /// existing `edgeHit` dead zone already refuses: no hop.
    static func hop(world: World, machine id: String, point p: Point, deadZone: Int) -> Hop? {
        guard let me = world.member(id), !me.displays.isEmpty else { return nil }
        // Rule 1: the pointer must sit on an outer edge of this desktop (existing geometry,
        // including the corner dead zone measured along the merged run of displays there).
        guard let hit = edgeHit(p, displays: me.displays, deadZone: deadZone) else { return nil }
        guard let local = display(containing: p, in: me.displays) else { return nil }

        // Rule 2: convert to world and find the boundary segment facing this point.
        let w = Point(x: me.offset.x + p.x, y: me.offset.y + p.y)
        let along = hit.edge.isHorizontal ? w.x : w.y
        let zone = max(deadZone, 0)
        let runs = edgeSpans(hit.edge, union(me.displays), me.displays)
        let runOffset = hit.edge.isHorizontal ? me.offset.x : me.offset.y

        var found: Boundary?
        for b in world.boundaries(of: id) where b.edge == hit.edge && b.display == local.id {
            guard b.span.contains(along) else { continue }
            // Extra dead zone of §3 step 2: only at an end of the segment that is also a
            // corner of this machine's merged run on that edge.
            if isRunCorner(b.span.lo, runs: runs, offset: runOffset), along < b.span.lo + zone {
                return nil
            }
            if isRunCorner(b.span.hi, runs: runs, offset: runOffset), along >= b.span.hi - zone {
                return nil
            }
            found = b
            break
        }
        guard let boundary = found, let target = world.member(boundary.peer) else { return nil }
        guard let entry = entryPoint(onto: target, display: boundary.peerDisplay, from: w, edge: hit.edge) else {
            return nil
        }
        return Hop(edge: hit.edge, frac: hit.frac, target: boundary.peer,
                   targetDisplay: boundary.peerDisplay, point: entry)
    }

    /// Whether a world coordinate along an edge is an end of one of this machine's merged runs
    /// (the runs are in local coordinates, `offset` converts them).
    private static func isRunCorner(_ worldValue: Int, runs: [Span], offset: Int) -> Bool {
        runs.contains { abs(($0.lo + offset) - worldValue) <= touchTolerance
                     || abs(($0.hi + offset) - worldValue) <= touchTolerance }
    }

    /// Spec §3 rule 3: the world point `w` expressed in `target`'s own coordinates, moved onto
    /// the facing edge of `display` inset 1 px and clamped into that display's span.
    private static func entryPoint(onto target: World.Member, display displayID: String,
                                   from w: Point, edge: Edge) -> Point? {
        guard let b = target.displays.first(where: { $0.id == displayID }) ?? primary(target.displays) else {
            return nil
        }
        var q = Point(x: w.x - target.offset.x, y: w.y - target.offset.y)
        let r = rect(of: b)
        switch edge {
        case .right: // left the local desktop to the right → arrive on the neighbour's left
            q.x = r.x + 1
            q.y = min(max(q.y, r.y), r.y + r.h - 1)
        case .left:
            q.x = r.x + r.w - 2
            q.y = min(max(q.y, r.y), r.y + r.h - 1)
        case .bottom:
            q.y = r.y + 1
            q.x = min(max(q.x, r.x), r.x + r.w - 1)
        case .top:
            q.y = r.y + r.h - 2
            q.x = min(max(q.x, r.x), r.x + r.w - 1)
        }
        // A 1-px display would put the inset outside: keep the point on the display.
        q.x = min(max(q.x, r.x), r.x + r.w - 1)
        q.y = min(max(q.y, r.y), r.y + r.h - 1)
        return q
    }

    /// Spec §3 rule 4, receiver side: an `enter.pt` from the sender, placed on a real display.
    static func entryPoint(pt: Point, displays: [Display]) -> Point {
        snapToDisplays(clamp(pt, to: displays), displays: displays)
    }

    /// Spec §3 hotkeys: the machine to hand control to when Ctrl+Alt+arrow is pressed on
    /// `id` with the pointer at local point `cursor`.
    ///
    /// First choice is a machine that has a boundary with `id` on that side — the one whose
    /// segment is closest to the cursor along the edge, ties going to the largest segment.
    /// Failing that, the machine whose world-union centre is nearest in that direction inside
    /// a ±60° cone. Failing that, `nil` (a no-op).
    ///
    /// Worked example — A (offset 0,0, 2560×1440) with B touching A's right edge over
    /// y 0…1080 and C touching it over y 1200…1440. Ctrl+Alt+→ with the cursor at y 300 picks
    /// B (distance 0 to its segment, C is 900 away); at y 1300 it picks C.
    static func hotkeyTarget(world: World, machine id: String, direction: Edge, cursor: Point) -> String? {
        guard let me = world.member(id) else { return nil }
        let w = Point(x: me.offset.x + cursor.x, y: me.offset.y + cursor.y)
        let along = direction.isHorizontal ? w.x : w.y

        var best: (peer: String, distance: Int, length: Int)?
        for b in world.boundaries(of: id) where b.edge == direction {
            let distance = b.span.distance(to: along)
            let length = b.span.length
            if let current = best {
                if distance > current.distance { continue }
                if distance == current.distance, length <= current.length { continue }
            }
            best = (b.peer, distance, length)
        }
        if let best { return best.peer }

        // No boundary: the nearest union centre in that direction, within a ±60° cone.
        let mine = world.union(of: id)
        guard !mine.isEmpty else { return nil }
        let origin = Point(x: mine.x + mine.w / 2, y: mine.y + mine.h / 2)
        let dir: (x: Double, y: Double)
        switch direction {
        case .left: dir = (-1, 0)
        case .right: dir = (1, 0)
        case .top: dir = (0, -1)
        case .bottom: dir = (0, 1)
        }
        var nearest: (peer: String, distance: Double)?
        for other in world.members where other.id != id {
            let u = world.union(of: other.id)
            if u.isEmpty { continue }
            let vx = Double(u.x + u.w / 2 - origin.x)
            let vy = Double(u.y + u.h / 2 - origin.y)
            let length = (vx * vx + vy * vy).squareRoot()
            if length <= 0 { continue }
            let projection = vx * dir.x + vy * dir.y
            guard projection > 0, projection >= hotkeyConeCosine * length else { continue }
            if let current = nearest, length >= current.distance { continue }
            nearest = (other.id, length)
        }
        return nearest?.peer
    }
}
