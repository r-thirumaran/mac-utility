package core

import (
	"fmt"
	"testing"
)

// Canvas fixtures. All sizes are native desktop units; the primary display's
// top-left is (0,0) on every machine (docs/edgehop-protocol.md §1).
var (
	fhd  = []Display{{ID: "d1", X: 0, Y: 0, W: 1920, H: 1080, Scale: 1, Primary: true}}
	qhd  = []Display{{ID: "d1", X: 0, Y: 0, W: 2560, H: 1440, Scale: 1, Primary: true}}
	twin = []Display{ // laptop plus an external monitor to its right
		{ID: "built-in", X: 0, Y: 0, W: 1512, H: 982, Scale: 2, Primary: true},
		{ID: "external", X: 1512, Y: -200, W: 2560, H: 1440, Scale: 1},
	}
)

func dispMap(pairs ...any) map[string][]Display {
	out := map[string][]Display{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i].(string)] = pairs[i+1].([]Display)
	}
	return out
}

func TestBoundariesSideBySide(t *testing.T) {
	// A (1920×1080 at 0,0) with B to its right, flush.
	l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 1920, Y: 0}}}
	d := dispMap("A", fhd, "B", fhd)

	bs := Boundaries(l, "A", d)
	if len(bs) != 1 {
		t.Fatalf("A boundaries = %+v", bs)
	}
	b := bs[0]
	if b.Edge != EdgeRight || b.Target != "B" || b.Lo != 0 || b.Hi != 1080 {
		t.Fatalf("A→B boundary = %+v", b)
	}
	// The other side sees the mirror image.
	bs = Boundaries(l, "B", d)
	if len(bs) != 1 || bs[0].Edge != EdgeLeft || bs[0].Target != "A" || bs[0].Width() != 1080 {
		t.Fatalf("B boundaries = %+v", bs)
	}
	// A 2 px gap still touches; 3 px does not.
	for _, c := range []struct {
		gap  int
		want int
	}{{1, 1}, {2, 1}, {3, 0}, {-2, 1}} {
		l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 1920 + c.gap, Y: 0}}}
		if got := len(Boundaries(l, "A", d)); got != c.want {
			t.Errorf("gap %d: %d boundaries, want %d", c.gap, got, c.want)
		}
	}
}

func TestBoundariesVerticalOffsetAndStack(t *testing.T) {
	// B sits 600 px lower, so only y 600–1080 of A's right edge is a boundary.
	l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 1920, Y: 600}}}
	d := dispMap("A", fhd, "B", fhd)
	bs := Boundaries(l, "A", d)
	if len(bs) != 1 || bs[0].Lo != 600 || bs[0].Hi != 1080 {
		t.Fatalf("offset boundary = %+v", bs)
	}
	// Below and above A, with x ranges overlapping: top and bottom boundaries.
	l = Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 0, Y: 1080}, {ID: "C", X: 0, Y: -1080}}}
	d = dispMap("A", fhd, "B", fhd, "C", fhd)
	got := map[Edge]string{}
	for _, b := range Boundaries(l, "A", d) {
		got[b.Edge] = b.Target
	}
	if got[EdgeBottom] != "B" || got[EdgeTop] != "C" || len(got) != 2 {
		t.Fatalf("stack boundaries = %+v", got)
	}
}

func TestBoundariesTwoMonitorsOnlyExternalTouches(t *testing.T) {
	// A has a laptop screen and an external monitor to its right; B is to the
	// right of the external monitor only, so the built-in screen has no
	// boundary at all.
	l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 4072, Y: -200}}}
	d := dispMap("A", twin, "B", qhd)
	bs := Boundaries(l, "A", d)
	if len(bs) != 1 {
		t.Fatalf("boundaries = %+v", bs)
	}
	if bs[0].Local.ID != "external" || bs[0].Edge != EdgeRight || bs[0].Lo != -200 || bs[0].Hi != 1240 {
		t.Fatalf("boundary = %+v", bs[0])
	}
	// Hopping from the built-in screen's own right edge is impossible: it is
	// not an outer edge of A's desktop, and no boundary names it.
	me, _ := l.Placement("A")
	if _, ok := HopAt(Point{1511, 500}, twin, me, bs, 0); ok {
		t.Fatal("hopped from an inner display edge")
	}
}

func TestBoundariesIgnoresSameMachineAndUnknownDisplays(t *testing.T) {
	// A's two displays touch each other, but a machine never borders itself.
	l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 9000, Y: 0}}}
	d := dispMap("A", twin) // B's displays are unknown
	if bs := Boundaries(l, "A", d); len(bs) != 0 {
		t.Fatalf("boundaries = %+v", bs)
	}
	// A machine missing from the layout has no boundaries.
	if bs := Boundaries(Layout{}, "A", d); bs != nil {
		t.Fatalf("unplaced machine has boundaries: %+v", bs)
	}
}

func TestHopAtEntryPoint(t *testing.T) {
	a := Placement{ID: "A", X: 0, Y: 0}
	cases := []struct {
		name     string
		l        Layout
		d        map[string][]Display
		self     []Display
		p        Point
		deadZone int
		target   string
		enter    Edge
		pt       Point
	}{{
		name:   "right into a same-size machine keeps the row",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 1920, Y: 0}}},
		d:      dispMap("A", fhd, "B", fhd),
		self:   fhd,
		p:      Point{1919, 540},
		target: "B", enter: EdgeLeft, pt: Point{1, 540},
	}, {
		name:   "right into a machine 600 px lower lands near its top",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 1920, Y: 600}}},
		d:      dispMap("A", fhd, "B", fhd),
		self:   fhd,
		p:      Point{1919, 700},
		target: "B", enter: EdgeLeft, pt: Point{1, 100},
	}, {
		name:   "left into a taller machine keeps the world row",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: -2560, Y: -180}}},
		d:      dispMap("A", fhd, "B", qhd),
		self:   fhd,
		p:      Point{0, 300},
		target: "B", enter: EdgeRight, pt: Point{2558, 480},
	}, {
		name:   "down into the machine below",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 0, Y: 1080}}},
		d:      dispMap("A", fhd, "B", fhd),
		self:   fhd,
		p:      Point{700, 1079},
		target: "B", enter: EdgeTop, pt: Point{700, 1},
	}, {
		name:   "up into the machine above",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 0, Y: -1080}}},
		d:      dispMap("A", fhd, "B", fhd),
		self:   fhd,
		p:      Point{700, 0},
		target: "B", enter: EdgeBottom, pt: Point{700, 1078},
	}, {
		name:   "the entry point is clamped into the target display",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 1920, Y: 900}}},
		d:      dispMap("A", fhd, "B", fhd),
		self:   fhd,
		p:      Point{1919, 950},
		target: "B", enter: EdgeLeft, pt: Point{1, 50},
	}, {
		name:   "from the external monitor of a two-screen machine",
		l:      Layout{Machines: []Placement{a, {ID: "B", X: 4072, Y: -200}}},
		d:      dispMap("A", twin, "B", qhd),
		self:   twin,
		p:      Point{4071, 500},
		target: "B", enter: EdgeLeft, pt: Point{1, 700},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			me, _ := c.l.Placement("A")
			bs := Boundaries(c.l, "A", c.d)
			hop, ok := HopAt(c.p, c.self, me, bs, c.deadZone)
			if !ok {
				t.Fatalf("no hop at %+v (boundaries %+v)", c.p, bs)
			}
			if hop.Target != c.target || hop.Enter != c.enter || hop.Pt != c.pt {
				t.Fatalf("hop = %+v, want target %s enter %s pt %+v", hop, c.target, c.enter, c.pt)
			}
		})
	}
}

func TestHopAtNoBoundaryAndDeadZones(t *testing.T) {
	a := Placement{ID: "A", X: 0, Y: 0}
	// B only covers the lower half of A's right edge.
	l := Layout{Machines: []Placement{a, {ID: "B", X: 1920, Y: 540}}}
	d := dispMap("A", fhd, "B", fhd)
	bs := Boundaries(l, "A", d)

	// Above the segment: the cursor just stops.
	if _, ok := HopAt(Point{1919, 200}, fhd, a, bs, 0); ok {
		t.Fatal("hopped where there is no boundary")
	}
	// Inside the segment, away from both ends.
	if _, ok := HopAt(Point{1919, 800}, fhd, a, bs, 32); !ok {
		t.Fatal("no hop in the middle of the segment")
	}
	// The segment's lower end is A's bottom-right corner (an end of A's
	// merged run on that edge), so the dead zone applies there.
	if _, ok := HopAt(Point{1919, 1070}, fhd, a, bs, 32); ok {
		t.Fatal("hopped inside the corner dead zone")
	}
	// The segment's upper end (y = 540) is NOT a corner of A's run, so a hop
	// right next to it still works.
	if _, ok := HopAt(Point{1919, 545}, fhd, a, bs, 32); !ok {
		t.Fatal("dead zone applied to a segment end that is not a corner")
	}
	// A machine that is not placed on the canvas never hops.
	if _, ok := HopAt(Point{1919, 800}, fhd, Placement{ID: "A", X: 5, Y: 5}, nil, 0); ok {
		t.Fatal("hopped with no boundaries")
	}
}

func TestHopAtCornerDeadZone(t *testing.T) {
	// Two machines side by side, full height: the top and bottom 32 px of the
	// shared edge are dead (Hot Corners).
	l := Layout{Machines: []Placement{{ID: "A", X: 0, Y: 0}, {ID: "B", X: 1920, Y: 0}}}
	d := dispMap("A", fhd, "B", fhd)
	me, _ := l.Placement("A")
	bs := Boundaries(l, "A", d)
	for _, c := range []struct {
		y    int
		want bool
	}{{0, false}, {31, false}, {32, true}, {540, true}, {1047, true}, {1048, false}, {1079, false}} {
		_, ok := HopAt(Point{1919, c.y}, fhd, me, bs, 32)
		if ok != c.want {
			t.Errorf("hop at y=%d: %v, want %v", c.y, ok, c.want)
		}
	}
}

func TestHopAtThreeMachines(t *testing.T) {
	// A | B | C in a row; A only borders B, B borders both.
	l := Layout{Machines: []Placement{{ID: "A", X: 0}, {ID: "B", X: 1920}, {ID: "C", X: 3840}}}
	d := dispMap("A", fhd, "B", fhd, "C", fhd)
	for _, c := range []struct {
		self   string
		p      Point
		target string
		pt     Point
	}{
		{"A", Point{1919, 540}, "B", Point{1, 540}},
		{"B", Point{1919, 540}, "C", Point{1, 540}},
		{"B", Point{0, 540}, "A", Point{1918, 540}},
	} {
		me, _ := l.Placement(c.self)
		hop, ok := HopAt(c.p, fhd, me, Boundaries(l, c.self, d), 0)
		if !ok || hop.Target != c.target || hop.Pt != c.pt {
			t.Errorf("%s at %+v: hop %+v ok %v, want %s %+v", c.self, c.p, hop, ok, c.target, c.pt)
		}
	}
	// C has nothing to its right.
	me, _ := l.Placement("C")
	if _, ok := HopAt(Point{1919, 540}, fhd, me, Boundaries(l, "C", d), 0); ok {
		t.Fatal("C hopped off the end of the row")
	}
}

func TestHotkeyTarget(t *testing.T) {
	// A | B in a row, C far below A with no touching edge.
	l := Layout{Machines: []Placement{{ID: "A", X: 0}, {ID: "B", X: 1920}, {ID: "C", X: 0, Y: 3000}}}
	d := dispMap("A", fhd, "B", fhd, "C", fhd)
	cur := Point{960, 540}
	cases := []struct {
		from string
		dir  Edge
		want string
		ok   bool
	}{
		{"A", EdgeRight, "B", true},  // boundary
		{"B", EdgeLeft, "A", true},   // boundary
		{"A", EdgeBottom, "C", true}, // cone fallback, no boundary
		{"A", EdgeTop, "", false},    // nothing that way
		{"A", EdgeLeft, "", false},
		{"A", Edge("x"), "", false},
		{"zz", EdgeRight, "", false},
	}
	for _, c := range cases {
		got, ok := HotkeyTarget(c.dir, c.from, l, d, &cur)
		if got != c.want || ok != c.ok {
			t.Errorf("HotkeyTarget(%s, %s) = (%q, %v), want (%q, %v)", c.dir, c.from, got, ok, c.want, c.ok)
		}
	}
	// Two machines share A's right edge: D takes the top 200 px, B the rest.
	// Without a cursor the widest segment wins.
	l2 := Layout{Machines: []Placement{{ID: "A", X: 0}, {ID: "B", X: 1920, Y: 200}, {ID: "D", X: 1920, Y: 0}}}
	d2 := dispMap("A", fhd, "B", fhd, "D", []Display{{ID: "s", W: 1920, H: 200, Primary: true}})
	if got, ok := HotkeyTarget(EdgeRight, "A", l2, d2, nil); !ok || got != "B" {
		t.Fatalf("widest segment = %q %v", got, ok)
	}
	// With the cursor at the very top of A, D's segment is the nearer one.
	top := Point{1919, 5}
	if got, ok := HotkeyTarget(EdgeRight, "A", l2, d2, &top); !ok || got != "D" {
		t.Fatalf("nearest segment = %q %v", got, ok)
	}
	// The ±60° cone excludes a machine that is mostly sideways.
	l3 := Layout{Machines: []Placement{{ID: "A", X: 0}, {ID: "E", X: 9000, Y: 400}}}
	d3 := dispMap("A", fhd, "E", fhd)
	if _, ok := HotkeyTarget(EdgeBottom, "A", l3, d3, &cur); ok {
		t.Fatal("a sideways machine was inside the down cone")
	}
	if got, ok := HotkeyTarget(EdgeRight, "A", l3, d3, &cur); !ok || got != "E" {
		t.Fatalf("cone right = %q %v", got, ok)
	}
}

func TestWorldRectAndUnion(t *testing.T) {
	p := Placement{ID: "A", X: 100, Y: -50}
	if got := WorldRect(p, twin[1]); got != (Rect{1612, -250, 2560, 1440}) {
		t.Fatalf("WorldRect = %+v", got)
	}
	if got := WorldUnion(p, twin); got != (Rect{100, -250, 4072, 1440}) {
		t.Fatalf("WorldUnion = %+v", got)
	}
	// A machine with no known displays still occupies the fallback size.
	if got := WorldUnion(p, nil); got != (Rect{100, -50, 1920, 1080}) {
		t.Fatalf("WorldUnion(nil) = %+v", got)
	}
}

func ExampleBoundaries() {
	l := Layout{Machines: []Placement{{ID: "mac", X: 0}, {ID: "pc", X: 1920, Y: 300}}}
	d := dispMap("mac", fhd, "pc", fhd)
	for _, b := range Boundaries(l, "mac", d) {
		fmt.Printf("%s edge of %s → %s (world %d..%d)\n", b.Edge, b.Local.ID, b.Target, b.Lo, b.Hi)
	}
	// Output: right edge of d1 → pc (world 300..1080)
}
