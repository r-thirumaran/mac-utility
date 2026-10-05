package core

import (
	"fmt"
	"sort"
)

// GridSize is the number of columns and rows the layout editor offers.
const GridSize = 5

// Tile places a machine in the layout grid.
type Tile struct {
	ID  string `json:"id"`
	Col int    `json:"col"`
	Row int    `json:"row"`
}

// Layout is the group's tile grid (docs/edgehop-protocol.md §6 `layout`).
// The newest UpdatedAt (ms since the Unix epoch) wins when layouts conflict.
type Layout struct {
	UpdatedAt int64  `json:"updatedAt"`
	Tiles     []Tile `json:"tiles"`
}

// Validate checks that no machine and no cell appears twice and that every
// cell is non-negative.
func (l Layout) Validate() error {
	ids := map[string]bool{}
	cells := map[[2]int]string{}
	for _, t := range l.Tiles {
		if t.ID == "" {
			return fmt.Errorf("layout: tile without id")
		}
		if ids[t.ID] {
			return fmt.Errorf("layout: %s appears twice", t.ID)
		}
		ids[t.ID] = true
		if t.Col < 0 || t.Row < 0 || t.Col >= 64 || t.Row >= 64 {
			return fmt.Errorf("layout: %s at (%d,%d) is outside the grid", t.ID, t.Col, t.Row)
		}
		cell := [2]int{t.Col, t.Row}
		if other, dup := cells[cell]; dup {
			return fmt.Errorf("layout: %s and %s share cell (%d,%d)", other, t.ID, t.Col, t.Row)
		}
		cells[cell] = t.ID
	}
	return nil
}

// Tile returns the tile of id.
func (l Layout) Tile(id string) (Tile, bool) {
	for _, t := range l.Tiles {
		if t.ID == id {
			return t, true
		}
	}
	return Tile{}, false
}

// Has reports whether id is placed in the layout.
func (l Layout) Has(id string) bool {
	_, ok := l.Tile(id)
	return ok
}

// Neighbour returns the machine directly adjacent to id on the given edge.
func (l Layout) Neighbour(id string, e Edge) (string, bool) {
	t, ok := l.Tile(id)
	if !ok {
		return "", false
	}
	col, row := t.Col, t.Row
	switch e {
	case EdgeLeft:
		col--
	case EdgeRight:
		col++
	case EdgeTop:
		row--
	case EdgeBottom:
		row++
	default:
		return "", false
	}
	for _, o := range l.Tiles {
		if o.Col == col && o.Row == row && o.ID != id {
			return o.ID, true
		}
	}
	return "", false
}

// Neighbour is the free-function form of Layout.Neighbour.
func Neighbour(l Layout, id string, e Edge) (string, bool) { return l.Neighbour(id, e) }

// Without returns a copy of the layout without id.
func (l Layout) Without(id string) Layout {
	out := Layout{UpdatedAt: l.UpdatedAt}
	for _, t := range l.Tiles {
		if t.ID != id {
			out.Tiles = append(out.Tiles, t)
		}
	}
	return out
}

// Normalized returns a copy with tiles sorted by row then column, so equal
// layouts compare equal.
func (l Layout) Normalized() Layout {
	out := Layout{UpdatedAt: l.UpdatedAt, Tiles: append([]Tile(nil), l.Tiles...)}
	sort.Slice(out.Tiles, func(i, j int) bool {
		if out.Tiles[i].Row != out.Tiles[j].Row {
			return out.Tiles[i].Row < out.Tiles[j].Row
		}
		if out.Tiles[i].Col != out.Tiles[j].Col {
			return out.Tiles[i].Col < out.Tiles[j].Col
		}
		return out.Tiles[i].ID < out.Tiles[j].ID
	})
	return out
}
