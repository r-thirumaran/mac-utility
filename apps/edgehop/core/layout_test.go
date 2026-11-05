package core

import (
	"strings"
	"testing"
)

func TestLayoutNeighbour(t *testing.T) {
	l := Layout{UpdatedAt: 1, Tiles: []Tile{
		{ID: "a", Col: 1, Row: 1},
		{ID: "b", Col: 2, Row: 1},
		{ID: "c", Col: 1, Row: 0},
		{ID: "d", Col: 3, Row: 1}, // not adjacent to a
	}}
	cases := []struct {
		id   string
		e    Edge
		want string
		ok   bool
	}{
		{"a", EdgeRight, "b", true},
		{"b", EdgeLeft, "a", true},
		{"a", EdgeTop, "c", true},
		{"c", EdgeBottom, "a", true},
		{"a", EdgeLeft, "", false},
		{"a", EdgeBottom, "", false},
		{"b", EdgeRight, "d", true},
		{"d", EdgeRight, "", false},
		{"zz", EdgeRight, "", false},
		{"a", Edge("x"), "", false},
	}
	for _, c := range cases {
		got, ok := Neighbour(l, c.id, c.e)
		if got != c.want || ok != c.ok {
			t.Errorf("Neighbour(%s, %s) = (%q, %v), want (%q, %v)", c.id, c.e, got, ok, c.want, c.ok)
		}
	}
}

func TestLayoutValidate(t *testing.T) {
	good := Layout{Tiles: []Tile{{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 4, Row: 4}}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
	bad := []Layout{
		{Tiles: []Tile{{ID: "a"}, {ID: "a", Col: 1}}},
		{Tiles: []Tile{{ID: "a"}, {ID: "b"}}},
		{Tiles: []Tile{{ID: "", Col: 1}}},
		{Tiles: []Tile{{ID: "a", Col: -1}}},
		{Tiles: []Tile{{ID: "a", Row: 99}}},
	}
	for i, l := range bad {
		if err := l.Validate(); err == nil {
			t.Errorf("bad layout %d accepted", i)
		}
	}
	if w := good.Without("a"); len(w.Tiles) != 1 || w.Tiles[0].ID != "b" {
		t.Errorf("Without = %+v", w)
	}
	n := Layout{Tiles: []Tile{{ID: "b", Col: 1, Row: 0}, {ID: "a", Col: 0, Row: 0}}}.Normalized()
	if n.Tiles[0].ID != "a" {
		t.Errorf("Normalized order = %+v", n.Tiles)
	}
}

func TestLayoutValidatePlacement(t *testing.T) {
	d := dispMap("a", fhd, "b", fhd, "c", qhd)
	ok := Layout{Machines: []Placement{{ID: "a", X: 0}, {ID: "b", X: 1920}}}
	if err := ok.ValidatePlacement(d); err != nil {
		t.Fatalf("valid canvas rejected: %v", err)
	}
	// Touching is fine; a 1 px overlap is not.
	bad := Layout{Machines: []Placement{{ID: "a", X: 0}, {ID: "b", X: 1919}}}
	if err := bad.ValidatePlacement(d); err == nil {
		t.Fatal("overlapping machines accepted")
	} else if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("error does not name the machines: %v", err)
	}
	// A machine's own displays may touch and overlap nothing else.
	if err := (Layout{Machines: []Placement{{ID: "a", X: 0}, {ID: "c", X: 4100}}}).ValidatePlacement(dispMap("a", twin, "c", qhd)); err != nil {
		t.Fatalf("two-monitor machine rejected: %v", err)
	}
	// Unknown displays never overlap.
	if err := (Layout{Machines: []Placement{{ID: "x", X: 0}, {ID: "y", X: 0}}}).ValidatePlacement(d); err != nil {
		t.Fatalf("unknown displays rejected: %v", err)
	}
	// Structural rules.
	for i, l := range []Layout{
		{Machines: []Placement{{ID: "a"}, {ID: "a", X: 10}}},
		{Machines: []Placement{{ID: "", X: 1}}},
		{Machines: []Placement{{ID: "a", X: LayoutMaxOffset + 1}}},
		{Machines: []Placement{{ID: "a", Y: -LayoutMaxOffset - 1}}},
	} {
		if err := l.ValidatePlacement(d); err == nil {
			t.Errorf("bad canvas %d accepted", i)
		}
	}
}

func TestTilesFromMachines(t *testing.T) {
	d := dispMap("a", fhd, "b", fhd, "c", fhd, "e", fhd)
	// A row of three: one row, columns left to right.
	got := TilesFromMachines([]Placement{{ID: "c", X: 3840}, {ID: "a", X: 0}, {ID: "b", X: 1920}}, d)
	want := []Tile{{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 1, Row: 0}, {ID: "c", Col: 2, Row: 0}}
	if !sameTiles(got, want) {
		t.Fatalf("row tiles = %+v", got)
	}
	// Two stacked rows: machines whose y ranges do not overlap are separate rows.
	got = TilesFromMachines([]Placement{{ID: "a", X: 0}, {ID: "b", X: 1920}, {ID: "c", X: 0, Y: 1080}, {ID: "e", X: 1920, Y: 1080}}, d)
	want = []Tile{
		{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 1, Row: 0},
		{ID: "c", Col: 0, Row: 1}, {ID: "e", Col: 1, Row: 1},
	}
	if !sameTiles(got, want) {
		t.Fatalf("grid tiles = %+v", got)
	}
	// Derived tiles always pass validation.
	if err := (Layout{Machines: nil, Tiles: got}).Validate(); err != nil {
		t.Fatalf("derived tiles invalid: %v", err)
	}
	if TilesFromMachines(nil, d) != nil {
		t.Fatal("empty canvas produced tiles")
	}
}

func TestMachinesFromTiles(t *testing.T) {
	d := dispMap("a", fhd, "b", qhd)
	// A v1 row: the second machine's union touches the first one's right edge.
	got := MachinesFromTiles([]Tile{{ID: "a", Col: 0, Row: 2}, {ID: "b", Col: 1, Row: 2}}, d)
	want := []Placement{{ID: "a", X: 0, Y: 0}, {ID: "b", X: 1920, Y: 0}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("row placements = %+v", got)
	}
	// Two rows stack by the tallest union of the rows above.
	got = MachinesFromTiles([]Tile{{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 0, Row: 1}}, d)
	if len(got) != 2 || got[0] != (Placement{ID: "a"}) || got[1] != (Placement{ID: "b", X: 0, Y: 1080}) {
		t.Fatalf("stacked placements = %+v", got)
	}
	// A machine whose displays are unknown gets the fallback size.
	got = MachinesFromTiles([]Tile{{ID: "zz", Col: 0, Row: 0}, {ID: "a", Col: 1, Row: 0}}, d)
	if len(got) != 2 || got[1] != (Placement{ID: "a", X: 1920}) {
		t.Fatalf("fallback placements = %+v", got)
	}
	// A machine whose displays start left of its primary keeps them on canvas.
	got = MachinesFromTiles([]Tile{{ID: "m", Col: 0, Row: 0}}, dispMap("m", []Display{
		{ID: "p", X: 0, Y: 0, W: 1920, H: 1080, Primary: true},
		{ID: "l", X: -1280, Y: 0, W: 1280, H: 1024},
	}))
	if len(got) != 1 || got[0] != (Placement{ID: "m", X: 1280, Y: 0}) {
		t.Fatalf("negative-origin placement = %+v", got)
	}
}

func TestLayoutUpgraded(t *testing.T) {
	d := dispMap("a", fhd, "b", fhd)
	// Tiles only (a v1 peer or an old config): machines are filled in.
	l, changed := (Layout{UpdatedAt: 3, Tiles: []Tile{{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 1, Row: 0}}}).Upgraded(d)
	if !changed || len(l.Machines) != 2 {
		t.Fatalf("tiles not upgraded: %+v", l)
	}
	if p, _ := l.Placement("b"); p.X != 1920 {
		t.Fatalf("placement = %+v", l.Machines)
	}
	// Machines only: tiles are derived for v1 peers.
	l, changed = (Layout{Machines: []Placement{{ID: "a", X: 0}, {ID: "b", X: 1920}}}).Upgraded(d)
	if !changed || len(l.Tiles) != 2 {
		t.Fatalf("tiles not derived: %+v", l)
	}
	// Idempotent.
	if _, changed = l.Upgraded(d); changed {
		t.Fatalf("second upgrade changed %+v", l)
	}
	// An empty layout stays empty.
	if l, changed = (Layout{}).Upgraded(d); changed || len(l.Machines) != 0 || len(l.Tiles) != 0 {
		t.Fatalf("empty layout = %+v %v", l, changed)
	}
	// Without / Has / IDs cover both models.
	full := Layout{Machines: []Placement{{ID: "a"}, {ID: "b", X: 1920}}, Tiles: []Tile{{ID: "a"}, {ID: "b", Col: 1}}}
	w := full.Without("a")
	if len(w.Machines) != 1 || len(w.Tiles) != 1 || w.Has("a") || !w.Has("b") {
		t.Fatalf("Without = %+v", w)
	}
	if ids := full.IDs(); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("IDs = %v", ids)
	}
}
