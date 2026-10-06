package core

import "testing"

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
