package core

import (
	"math"
	"testing"
)

var (
	single = []Display{{ID: "a", X: 0, Y: 0, W: 1920, H: 1080, Scale: 1, Primary: true}}
	// two side by side, the right one shorter and vertically centred
	sideBySide = []Display{
		{ID: "a", X: 0, Y: 0, W: 1920, H: 1080, Primary: true},
		{ID: "b", X: 1920, Y: 200, W: 1280, H: 720},
	}
	// L-shape: a wide primary with a second display above its right part
	lShape = []Display{
		{ID: "a", X: 0, Y: 1080, W: 3200, H: 1440, Primary: true},
		{ID: "b", X: 1280, Y: 0, W: 1920, H: 1080},
	}
	// a gap: two displays on the same row with 100 px between them
	gapped = []Display{
		{ID: "a", X: 0, Y: 0, W: 1000, H: 800, Primary: true},
		{ID: "b", X: 1100, Y: 0, W: 1000, H: 800},
	}
	// negative origin (Windows secondary left of primary)
	negative = []Display{
		{ID: "a", X: 0, Y: 0, W: 1920, H: 1080, Primary: true},
		{ID: "b", X: -2560, Y: -300, W: 2560, H: 1440},
	}
)

func TestUnion(t *testing.T) {
	cases := []struct {
		name string
		d    []Display
		want Rect
	}{
		{"single", single, Rect{0, 0, 1920, 1080}},
		{"side", sideBySide, Rect{0, 0, 3200, 1080}},
		{"l", lShape, Rect{0, 0, 3200, 2520}},
		{"gap", gapped, Rect{0, 0, 2100, 800}},
		{"neg", negative, Rect{-2560, -300, 4480, 1440}},
		{"none", nil, Rect{}},
	}
	for _, c := range cases {
		if got := Union(c.d); got != c.want {
			t.Errorf("%s: Union = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestEdgeHit(t *testing.T) {
	cases := []struct {
		name     string
		d        []Display
		p        Point
		dead     int
		wantEdge Edge
		wantFrac float64
		wantOK   bool
	}{
		{"single left middle", single, Point{0, 540}, 32, EdgeLeft, 0.5, true},
		{"single right last column", single, Point{1919, 270}, 32, EdgeRight, 0.25, true},
		{"single right one short", single, Point{1918, 270}, 32, "", 0, false},
		{"single top", single, Point{960, 0}, 32, EdgeTop, 0.5, true},
		{"single bottom", single, Point{480, 1079}, 32, EdgeBottom, 0.25, true},
		{"single interior", single, Point{100, 100}, 32, "", 0, false},
		{"single top-left corner dead", single, Point{0, 10}, 32, "", 0, false},
		{"single top-left corner just outside dead", single, Point{0, 32}, 32, EdgeLeft, 32.0 / 1080, true},
		{"single bottom-left corner dead", single, Point{0, 1060}, 32, "", 0, false},
		{"single corner with zero dead zone picks left first", single, Point{0, 0}, 0, EdgeLeft, 0, true},
		{"single beyond left (negative) still left", single, Point{-5, 540}, 32, EdgeLeft, 0.5, true},

		// side by side: right edge rows are only owned by b (y 200..919)
		{"side right owned by b", sideBySide, Point{3199, 500}, 32, EdgeRight, 500.0 / 1080, true},
		{"side right row not owned (above b)", sideBySide, Point{3199, 100}, 32, "", 0, false},
		{"side right row not owned (below b)", sideBySide, Point{3199, 1000}, 32, "", 0, false},
		{"side right corner of b dead", sideBySide, Point{3199, 210}, 32, "", 0, false},
		{"side a's right side is not an outer edge", sideBySide, Point{1919, 500}, 32, "", 0, false},
		{"side top owned by a only", sideBySide, Point{2000, 0}, 32, "", 0, false},
		{"side top on a", sideBySide, Point{960, 0}, 32, EdgeTop, 960.0 / 3200, true},
		{"side bottom on a", sideBySide, Point{960, 1079}, 32, EdgeBottom, 0.3, true},
		{"side bottom column of b (b ends at 920)", sideBySide, Point{2500, 1079}, 32, "", 0, false},

		// L-shape
		{"l left on a", lShape, Point{0, 2000}, 32, EdgeLeft, 2000.0 / 2520, true},
		{"l left above a is a gap", lShape, Point{0, 500}, 32, "", 0, false},
		{"l top on b", lShape, Point{2000, 0}, 32, EdgeTop, 2000.0 / 3200, true},
		{"l top left of b is a gap", lShape, Point{500, 0}, 32, "", 0, false},
		{"l a's top edge is not the union top", lShape, Point{500, 1080}, 32, "", 0, false},
		{"l right owned by both (merged run)", lShape, Point{3199, 1080}, 32, EdgeRight, 1080.0 / 2520, true},
		{"l right join is not a corner", lShape, Point{3199, 1070}, 32, EdgeRight, 1070.0 / 2520, true},
		{"l right bottom corner dead", lShape, Point{3199, 2500}, 32, "", 0, false},
		{"l bottom on a", lShape, Point{100, 2519}, 32, EdgeBottom, 100.0 / 3200, true},

		// gaps between displays: the gap sides are not outer edges
		{"gap left outer", gapped, Point{0, 400}, 32, EdgeLeft, 0.5, true},
		{"gap inner right of a", gapped, Point{999, 400}, 32, "", 0, false},
		{"gap inner left of b", gapped, Point{1100, 400}, 32, "", 0, false},
		{"gap right outer", gapped, Point{2099, 400}, 32, EdgeRight, 0.5, true},
		{"gap top in the gap column", gapped, Point{1050, 0}, 32, "", 0, false},
		{"gap top run end is a corner", gapped, Point{990, 0}, 32, "", 0, false},

		// negative origin
		{"neg left on b", negative, Point{-2560, 400}, 32, EdgeLeft, 700.0 / 1440, true},
		{"neg right on a", negative, Point{1919, 500}, 32, EdgeRight, 800.0 / 1440, true},
		{"neg right row above a", negative, Point{1919, -100}, 32, "", 0, false},
		{"neg top on b", negative, Point{-1000, -300}, 32, EdgeTop, 1560.0 / 4480, true},
		{"neg bottom on b", negative, Point{-1000, 1139}, 32, EdgeBottom, 1560.0 / 4480, true},
		{"neg bottom column of a (ends at 1080)", negative, Point{100, 1139}, 32, "", 0, false},
		{"no displays", nil, Point{0, 0}, 32, "", 0, false},
	}
	for _, c := range cases {
		edge, frac, ok := EdgeHit(c.p, c.d, c.dead)
		if ok != c.wantOK || edge != c.wantEdge || math.Abs(frac-c.wantFrac) > 1e-9 {
			t.Errorf("%s: EdgeHit(%v) = (%q, %.4f, %v), want (%q, %.4f, %v)", c.name, c.p, edge, frac, ok, c.wantEdge, c.wantFrac, c.wantOK)
		}
	}
}

func TestEntryPoint(t *testing.T) {
	cases := []struct {
		name string
		e    Edge
		frac float64
		d    []Display
		want Point
	}{
		{"single left", EdgeLeft, 0.5, single, Point{1, 540}},
		{"single right", EdgeRight, 0.25, single, Point{1918, 270}},
		{"single top", EdgeTop, 0.5, single, Point{960, 1}},
		{"single bottom", EdgeBottom, 0, single, Point{0, 1078}},
		{"frac clamped", EdgeLeft, 7, single, Point{1, 1079}},
		{"frac negative", EdgeLeft, -1, single, Point{1, 0}},
		{"side right lands on b", EdgeRight, 0.5, sideBySide, Point{3198, 540}},
		{"side right above b snaps down onto b", EdgeRight, 0.05, sideBySide, Point{3198, 200}},
		{"l left above a snaps down onto a (nearest display)", EdgeLeft, 0.1, lShape, Point{1, 1080}},
		{"neg top on b", EdgeTop, 0.25, negative, Point{-1440, -299}},
		{"unknown edge centres", Edge("diagonal"), 0.5, single, Point{960, 540}},
		{"no displays", EdgeLeft, 0.5, nil, Point{}},
	}
	for _, c := range cases {
		if got := EntryPoint(c.e, c.frac, c.d); got != c.want {
			t.Errorf("%s: EntryPoint = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEntryPointNeverHitsEdge(t *testing.T) {
	// An arriving cursor must not immediately trigger the opposite hand-over.
	for _, d := range [][]Display{single, sideBySide, lShape, gapped, negative} {
		for _, e := range Edges {
			for f := 0.0; f <= 1.0; f += 0.05 {
				p := EntryPoint(e, f, d)
				if hit, _, ok := EdgeHit(p, d, 0); ok && hit == e {
					t.Errorf("EntryPoint(%s, %.2f) = %v sits on its own edge", e, f, p)
				}
			}
		}
	}
}

func TestOppositeAndCentre(t *testing.T) {
	pairs := map[Edge]Edge{EdgeLeft: EdgeRight, EdgeRight: EdgeLeft, EdgeTop: EdgeBottom, EdgeBottom: EdgeTop}
	for a, b := range pairs {
		if Opposite(a) != b || Opposite(b) != a {
			t.Errorf("Opposite(%s) wrong", a)
		}
	}
	if c := Centre(sideBySide); c != (Point{960, 540}) {
		t.Errorf("Centre = %v", c)
	}
	if c := Centre([]Display{{X: 10, Y: 10, W: 100, H: 100}}); c != (Point{60, 60}) {
		t.Errorf("Centre without primary = %v", c)
	}
}
