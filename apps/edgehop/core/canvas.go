package core

import (
	"math"
	"sort"
)

// The world canvas: boundary segments between the displays of different
// machines and the hop decision taken on the active machine
// (docs/edgehop-protocol.md §6.1 and the canvas rules of §1).

// BoundaryTouchSlack is how far apart two displays may be (world units) and
// still touch.
const BoundaryTouchSlack = 2

// Boundary is one segment where a local display touches a display of another
// machine. Edge is the side of the local display (and of this machine's
// desktop) the pointer leaves by; Lo and Hi are the touching span in world
// coordinates along that edge (y for left/right, x for top/bottom).
type Boundary struct {
	Edge   Edge
	Local  Display   // the local display, in this machine's coordinates
	Target string    // the machine on the other side
	Disp   Display   // the target display, in the target's coordinates
	Offset Placement // the target's canvas placement
	Lo, Hi int
}

// Width is the length of the segment along the edge.
func (b Boundary) Width() int { return b.Hi - b.Lo }

// Boundaries returns every boundary segment of machine self on the canvas,
// sorted by edge then span, given the last known displays of every machine.
// Recompute it when the layout or any machine's displays change.
func Boundaries(l Layout, self string, displays map[string][]Display) []Boundary {
	me, ok := l.Placement(self)
	if !ok {
		return nil
	}
	var out []Boundary
	for _, local := range displays[self] {
		if local.W <= 0 || local.H <= 0 {
			continue
		}
		a := WorldRect(me, local)
		for _, other := range l.Machines {
			if other.ID == self {
				continue
			}
			for _, d := range displays[other.ID] {
				if d.W <= 0 || d.H <= 0 {
					continue
				}
				b := WorldRect(other, d)
				// Horizontal neighbours: y ranges must overlap by ≥ 1.
				lo, hi := max(a.Y, b.Y), min(a.Y+a.H, b.Y+b.H)
				if hi-lo >= 1 {
					if abs(a.X+a.W-b.X) <= BoundaryTouchSlack {
						out = append(out, Boundary{EdgeRight, local, other.ID, d, other, lo, hi})
					}
					if abs(b.X+b.W-a.X) <= BoundaryTouchSlack {
						out = append(out, Boundary{EdgeLeft, local, other.ID, d, other, lo, hi})
					}
				}
				// Vertical neighbours: x ranges must overlap by ≥ 1.
				lo, hi = max(a.X, b.X), min(a.X+a.W, b.X+b.W)
				if hi-lo >= 1 {
					if abs(a.Y+a.H-b.Y) <= BoundaryTouchSlack {
						out = append(out, Boundary{EdgeBottom, local, other.ID, d, other, lo, hi})
					}
					if abs(b.Y+b.H-a.Y) <= BoundaryTouchSlack {
						out = append(out, Boundary{EdgeTop, local, other.ID, d, other, lo, hi})
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Edge != out[j].Edge {
			return out[i].Edge < out[j].Edge
		}
		if out[i].Lo != out[j].Lo {
			return out[i].Lo < out[j].Lo
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Disp.ID < out[j].Disp.ID
	})
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Hop is the decision taken for a pointer sitting on an outer edge: control
// moves to Target, which places its cursor at Pt (its own local
// coordinates). Edge is the edge of this machine the pointer left by, Enter
// the facing edge of the target and Frac the union fraction a v1 receiver
// uses instead of Pt.
type Hop struct {
	Target string
	Edge   Edge
	Enter  Edge
	Frac   float64
	Pt     Point
}

// DisplayAt returns the display containing p ("" false when none does).
func DisplayAt(p Point, displays []Display) (Display, bool) {
	for _, d := range displays {
		if rectOf(d).Contains(p) {
			return d, true
		}
	}
	return Display{}, false
}

// HopAt decides whether a pointer at local point p on the machine placed at
// me hops to another machine (§6.1 rules 2–3 on the canvas):
//
//  1. p must sit on an outer edge of this desktop that a display owns, outside
//     the corner dead zone of the merged run of displays on that edge (EdgeHit).
//  2. the world coordinate of p along that edge must fall inside a boundary
//     segment of the display p is on, and not within deadZone of a segment end
//     that is also an end of the merged run.
//  3. the entry point is p in the target's coordinates, pushed onto the facing
//     edge of the target display, inset 1 px and clamped into its span.
func HopAt(p Point, selfDisplays []Display, me Placement, bs []Boundary, deadZone int) (Hop, bool) {
	edge, frac, ok := EdgeHit(p, selfDisplays, deadZone)
	if !ok {
		return Hop{}, false
	}
	local, ok := DisplayAt(p, selfDisplays)
	if !ok {
		return Hop{}, false
	}
	if deadZone < 0 {
		deadZone = 0
	}
	horizontal := edge == EdgeLeft || edge == EdgeRight
	along := p.Y
	if !horizontal {
		along = p.X
	}
	// The world coordinate of p along the edge.
	worldAlong := along + me.Y
	if !horizontal {
		worldAlong = along + me.X
	}
	// The merged run of local displays on this edge, in world coordinates,
	// that contains the pointer: its ends are the corners of §6.3.
	runLo, runHi, haveRun := 0, 0, false
	offset := me.Y
	if !horizontal {
		offset = me.X
	}
	for _, s := range edgeSpans(edge, Union(selfDisplays), selfDisplays) {
		if along >= s.Lo && along < s.Hi {
			runLo, runHi, haveRun = s.Lo+offset, s.Hi+offset, true
			break
		}
	}
	for _, b := range bs {
		if b.Edge != edge || b.Local.ID != local.ID {
			continue
		}
		if worldAlong < b.Lo || worldAlong >= b.Hi {
			continue
		}
		if haveRun && deadZone > 0 {
			if b.Lo == runLo && worldAlong < b.Lo+deadZone {
				return Hop{}, false
			}
			if b.Hi == runHi && worldAlong >= b.Hi-deadZone {
				return Hop{}, false
			}
		}
		return Hop{Target: b.Target, Edge: edge, Enter: Opposite(edge), Frac: frac, Pt: b.entryPoint(p, me, edge)}, true
	}
	return Hop{}, false
}

// entryPoint converts a local point of the sending machine to the receiving
// machine's coordinates and places it on the facing edge of the target
// display, inset 1 px, clamped into that display.
func (b Boundary) entryPoint(p Point, me Placement, edge Edge) Point {
	q := Point{me.X + p.X - b.Offset.X, me.Y + p.Y - b.Offset.Y}
	d := b.Disp
	switch edge {
	case EdgeRight: // arriving on the target's left edge
		q.X = d.X + 1
	case EdgeLeft:
		q.X = d.X + d.W - 2
	case EdgeBottom: // arriving on the target's top edge
		q.Y = d.Y + 1
	case EdgeTop:
		q.Y = d.Y + d.H - 2
	}
	q.X = min(max(q.X, d.X), d.X+d.W-1)
	q.Y = min(max(q.Y, d.Y), d.Y+d.H-1)
	return q
}

// HotkeyTarget picks the machine control moves to for direction dir from the
// machine `from` (§6.2 on the canvas): the machine sharing a boundary on that
// side — the one whose segment is nearest the cursor along the edge, ties
// going to the widest segment — else the machine whose world-union centre
// lies nearest in that direction within a ±60° cone. cursor is `from`'s local
// pointer position and may be nil when it is not known (a remote active
// machine), in which case the widest segment wins.
func HotkeyTarget(dir Edge, from string, l Layout, displays map[string][]Display, cursor *Point) (string, bool) {
	if !dir.Valid() {
		return "", false
	}
	me, ok := l.Placement(from)
	if !ok {
		return "", false
	}
	horizontal := dir == EdgeLeft || dir == EdgeRight
	var want int
	haveCursor := false
	if cursor != nil {
		haveCursor = true
		if horizontal {
			want = me.Y + cursor.Y
		} else {
			want = me.X + cursor.X
		}
	}
	best, bestDist, bestWidth := "", math.Inf(1), -1
	for _, b := range Boundaries(l, from, displays) {
		if b.Edge != dir {
			continue
		}
		dist := 0.0
		if haveCursor {
			switch {
			case want < b.Lo:
				dist = float64(b.Lo - want)
			case want >= b.Hi:
				dist = float64(want - b.Hi + 1)
			}
		}
		w := b.Width()
		better := dist < bestDist ||
			(dist == bestDist && w > bestWidth) ||
			(dist == bestDist && w == bestWidth && best != "" && b.Target < best)
		if best == "" || better {
			best, bestDist, bestWidth = b.Target, dist, w
		}
	}
	if best != "" {
		return best, true
	}
	return coneTarget(dir, from, me, l, displays)
}

// coneTarget is the fallback of §6.2: the nearest machine centre inside a
// ±60° cone around dir.
func coneTarget(dir Edge, from string, me Placement, l Layout, displays map[string][]Display) (string, bool) {
	u := WorldUnion(me, displays[from])
	cx, cy := float64(u.X+u.W/2), float64(u.Y+u.H/2)
	var ux, uy float64
	switch dir {
	case EdgeLeft:
		ux = -1
	case EdgeRight:
		ux = 1
	case EdgeTop:
		uy = -1
	case EdgeBottom:
		uy = 1
	}
	const cos60 = 0.5
	best, bestDist := "", math.Inf(1)
	for _, m := range l.Machines {
		if m.ID == from {
			continue
		}
		o := WorldUnion(m, displays[m.ID])
		dx, dy := float64(o.X+o.W/2)-cx, float64(o.Y+o.H/2)-cy
		d := math.Hypot(dx, dy)
		if d == 0 {
			continue
		}
		if (dx*ux+dy*uy)/d < cos60 {
			continue
		}
		if d < bestDist || (d == bestDist && m.ID < best) {
			best, bestDist = m.ID, d
		}
	}
	return best, best != ""
}
