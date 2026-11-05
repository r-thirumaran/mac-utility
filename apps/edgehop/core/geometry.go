package core

import (
	"math"
	"sort"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
)

// Display is one monitor in desktop-union coordinates (origin top-left, y
// down, docs/edgehop-protocol.md §6 `displays`). It is the same type as
// input.Display so the platform backends need not import core.
type Display = input.Display

// Edge names a side of the desktop union.
type Edge string

// The four edges.
const (
	EdgeLeft   Edge = "left"
	EdgeRight  Edge = "right"
	EdgeTop    Edge = "top"
	EdgeBottom Edge = "bottom"
)

// Edges lists the four edges in detection priority order.
var Edges = []Edge{EdgeLeft, EdgeRight, EdgeTop, EdgeBottom}

// Valid reports whether e is one of the four edges.
func (e Edge) Valid() bool {
	switch e {
	case EdgeLeft, EdgeRight, EdgeTop, EdgeBottom:
		return true
	}
	return false
}

// Opposite returns the facing edge (left ⇄ right, top ⇄ bottom).
func Opposite(e Edge) Edge {
	switch e {
	case EdgeLeft:
		return EdgeRight
	case EdgeRight:
		return EdgeLeft
	case EdgeTop:
		return EdgeBottom
	case EdgeBottom:
		return EdgeTop
	}
	return e
}

// Point is a pixel position. It goes on the wire as `enter.pt` (§6 `active`).
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect is an axis-aligned pixel rectangle; X+W and Y+H are exclusive.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether p lies inside r.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

// Empty reports whether r has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

func rectOf(d Display) Rect { return Rect{d.X, d.Y, d.W, d.H} }

// Union returns the bounding box of the displays (zero Rect when none).
func Union(displays []Display) Rect {
	var u Rect
	first := true
	for _, d := range displays {
		if d.W <= 0 || d.H <= 0 {
			continue
		}
		if first {
			u = rectOf(d)
			first = false
			continue
		}
		minX, minY := min(u.X, d.X), min(u.Y, d.Y)
		maxX, maxY := max(u.X+u.W, d.X+d.W), max(u.Y+u.H, d.Y+d.H)
		u = Rect{minX, minY, maxX - minX, maxY - minY}
	}
	return u
}

// Primary returns the primary display (or the first one).
func Primary(displays []Display) (Display, bool) {
	for _, d := range displays {
		if d.Primary {
			return d, true
		}
	}
	if len(displays) > 0 {
		return displays[0], true
	}
	return Display{}, false
}

// Centre returns the centre of the primary display.
func Centre(displays []Display) Point {
	d, ok := Primary(displays)
	if !ok {
		return Point{}
	}
	return Point{d.X + d.W/2, d.Y + d.H/2}
}

// span is a half-open interval [Lo, Hi) along an edge.
type span struct{ Lo, Hi int }

// edgeSpans returns the merged intervals (along the edge) that displays
// actually occupy on the given outer edge of u: for left/right the y ranges
// of displays whose left/right side lies on u's left/right side, for
// top/bottom the x ranges. Touching or overlapping ranges are merged, so a
// corner is only where a merged run ends (a gap or the union's corner).
func edgeSpans(e Edge, u Rect, displays []Display) []span {
	var spans []span
	for _, d := range displays {
		if d.W <= 0 || d.H <= 0 {
			continue
		}
		switch e {
		case EdgeLeft:
			if d.X == u.X {
				spans = append(spans, span{d.Y, d.Y + d.H})
			}
		case EdgeRight:
			if d.X+d.W == u.X+u.W {
				spans = append(spans, span{d.Y, d.Y + d.H})
			}
		case EdgeTop:
			if d.Y == u.Y {
				spans = append(spans, span{d.X, d.X + d.W})
			}
		case EdgeBottom:
			if d.Y+d.H == u.Y+u.H {
				spans = append(spans, span{d.X, d.X + d.W})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Lo < spans[j].Lo })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.Lo <= merged[n-1].Hi {
			if s.Hi > merged[n-1].Hi {
				merged[n-1].Hi = s.Hi
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// onEdge reports whether p touches edge e of u. The left and top edges are
// the first pixel column/row (p.x ≤ u.minX); the right and bottom edges are
// the last pixel column/row (p.x ≥ u.maxX − 1), which is where every OS
// clamps the hardware cursor.
func onEdge(e Edge, u Rect, p Point) bool {
	switch e {
	case EdgeLeft:
		return p.X <= u.X
	case EdgeRight:
		return p.X >= u.X+u.W-1
	case EdgeTop:
		return p.Y <= u.Y
	case EdgeBottom:
		return p.Y >= u.Y+u.H-1
	}
	return false
}

// EdgeHit implements docs/edgehop-protocol.md §6.1 rule 2: p is a hit on
// edge e of the desktop union when it touches that outer edge AND a display
// actually owns that row (left/right) or column (top/bottom) on the edge,
// excluding deadZone pixels at either end of the owning run of displays (the
// corners, where macOS Hot Corners live). frac is p's position along the
// union's edge: (p.y − u.minY) / u.height for left/right, the x equivalent for
// top/bottom. Edges are tested in the order left, right, top, bottom and the
// first hit wins (only reachable with deadZone 0).
func EdgeHit(p Point, displays []Display, deadZone int) (Edge, float64, bool) {
	u := Union(displays)
	if u.Empty() {
		return "", 0, false
	}
	if deadZone < 0 {
		deadZone = 0
	}
	for _, e := range Edges {
		if !onEdge(e, u, p) {
			continue
		}
		along := p.Y
		if e == EdgeTop || e == EdgeBottom {
			along = p.X
		}
		for _, s := range edgeSpans(e, u, displays) {
			if along < s.Lo || along >= s.Hi {
				continue
			}
			if along < s.Lo+deadZone || along >= s.Hi-deadZone {
				return "", 0, false // corner dead zone
			}
			var frac float64
			if e == EdgeTop || e == EdgeBottom {
				frac = float64(p.X-u.X) / float64(u.W)
			} else {
				frac = float64(p.Y-u.Y) / float64(u.H)
			}
			return e, clamp01(frac), true
		}
	}
	return "", 0, false
}

// EntryPoint is where the cursor appears on a machine whose displays are
// given, when it arrives on edge e at fraction frac of the union (§6.1 rule
// 4): on that edge, inset by one pixel, snapped onto a real display if the
// union has a gap there.
func EntryPoint(e Edge, frac float64, displays []Display) Point {
	u := Union(displays)
	if u.Empty() {
		return Point{}
	}
	frac = clamp01(frac)
	var p Point
	switch e {
	case EdgeLeft:
		p = Point{u.X + 1, u.Y + int(math.Round(frac*float64(u.H)))}
	case EdgeRight:
		p = Point{u.X + u.W - 2, u.Y + int(math.Round(frac*float64(u.H)))}
	case EdgeTop:
		p = Point{u.X + int(math.Round(frac*float64(u.W))), u.Y + 1}
	case EdgeBottom:
		p = Point{u.X + int(math.Round(frac*float64(u.W))), u.Y + u.H - 2}
	default:
		return Centre(displays)
	}
	// Keep the inset point inside the union even for 1 px wide unions.
	p.X = min(max(p.X, u.X), u.X+u.W-1)
	p.Y = min(max(p.Y, u.Y), u.Y+u.H-1)
	return SnapToDisplays(p, displays)
}

// SnapToDisplays returns p when a display contains it, otherwise the nearest
// point inside any display.
func SnapToDisplays(p Point, displays []Display) Point {
	best, bestDist := p, math.Inf(1)
	for _, d := range displays {
		r := rectOf(d)
		if r.Empty() {
			continue
		}
		if r.Contains(p) {
			return p
		}
		q := Point{min(max(p.X, r.X), r.X+r.W-1), min(max(p.Y, r.Y), r.Y+r.H-1)}
		dx, dy := float64(q.X-p.X), float64(q.Y-p.Y)
		if dist := dx*dx + dy*dy; dist < bestDist {
			best, bestDist = q, dist
		}
	}
	return best
}

func clamp01(f float64) float64 {
	if f < 0 || math.IsNaN(f) {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
