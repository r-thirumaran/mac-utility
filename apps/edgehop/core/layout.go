package core

import (
	"fmt"
	"sort"
)

// GridSize is the number of columns and rows the legacy tile editor offers.
const GridSize = 5

// LayoutMaxOffset bounds a placement offset (docs/edgehop-protocol.md §6
// `layout`, canvas rule: |x|, |y| ≤ 1 000 000).
const LayoutMaxOffset = 1_000_000

// Fallback size of a machine whose displays are not known yet (used only to
// arrange a legacy tile layout on the canvas).
const (
	fallbackMachineW = 1920
	fallbackMachineH = 1080
)

// Tile places a machine in the legacy layout grid (v1 peers and old config
// files). v2 senders derive it from Machines.
type Tile struct {
	ID  string `json:"id"`
	Col int    `json:"col"`
	Row int    `json:"row"`
}

// Placement places one machine's displays on the shared world canvas: the
// world rect of display d is (X+d.X, Y+d.Y, d.W, d.H). A machine's displays
// move together; Edgehop never moves one monitor alone.
type Placement struct {
	ID string `json:"id"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
}

// Layout is the group's screen canvas (docs/edgehop-protocol.md §6
// `layout`). Machines is the v2 model; Tiles is kept in step for v1 peers.
// The newest UpdatedAt (ms since the Unix epoch) wins when layouts conflict.
type Layout struct {
	UpdatedAt int64       `json:"updatedAt"`
	Machines  []Placement `json:"machines"`
	Tiles     []Tile      `json:"tiles"`
}

// Validate checks the structure of both models: no machine twice, offsets
// within LayoutMaxOffset, and (for tiles) no cell twice. Cross-machine
// display overlap needs the displays and lives in ValidatePlacement.
func (l Layout) Validate() error {
	ids := map[string]bool{}
	for _, m := range l.Machines {
		if m.ID == "" {
			return fmt.Errorf("layout: placement without id")
		}
		if ids[m.ID] {
			return fmt.Errorf("layout: %s appears twice", m.ID)
		}
		ids[m.ID] = true
		if m.X < -LayoutMaxOffset || m.X > LayoutMaxOffset || m.Y < -LayoutMaxOffset || m.Y > LayoutMaxOffset {
			return fmt.Errorf("layout: %s at (%d,%d) is off the canvas", m.ID, m.X, m.Y)
		}
	}
	tileIDs := map[string]bool{}
	cells := map[[2]int]string{}
	for _, t := range l.Tiles {
		if t.ID == "" {
			return fmt.Errorf("layout: tile without id")
		}
		if tileIDs[t.ID] {
			return fmt.Errorf("layout: %s appears twice", t.ID)
		}
		tileIDs[t.ID] = true
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

// ValidatePlacement rejects a canvas on which two displays of DIFFERENT
// machines overlap (positive-area intersection). Machines whose displays are
// not known are skipped: they contribute no display to the canvas.
func (l Layout) ValidatePlacement(displays map[string][]Display) error {
	if err := l.Validate(); err != nil {
		return err
	}
	type worldDisp struct {
		machine string
		r       Rect
	}
	var all []worldDisp
	for _, m := range l.Machines {
		for _, d := range displays[m.ID] {
			r := WorldRect(m, d)
			if r.Empty() {
				continue
			}
			all = append(all, worldDisp{m.ID, r})
		}
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].machine == all[j].machine {
				continue
			}
			if overlaps(all[i].r, all[j].r) {
				return fmt.Errorf("layout: %s and %s overlap on the canvas", all[i].machine, all[j].machine)
			}
		}
	}
	return nil
}

// overlaps reports a positive-area intersection.
func overlaps(a, b Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// WorldRect returns the world rectangle of display d of the machine placed
// at p.
func WorldRect(p Placement, d Display) Rect { return Rect{p.X + d.X, p.Y + d.Y, d.W, d.H} }

// WorldUnion returns the world bounding box of a machine's displays. A
// machine with no known displays gets the fallback size at its offset so it
// still occupies a sensible area of the canvas.
func WorldUnion(p Placement, displays []Display) Rect {
	u := Union(displays)
	if u.Empty() {
		return Rect{p.X, p.Y, fallbackMachineW, fallbackMachineH}
	}
	return Rect{p.X + u.X, p.Y + u.Y, u.W, u.H}
}

// Placement returns the canvas placement of id.
func (l Layout) Placement(id string) (Placement, bool) {
	for _, m := range l.Machines {
		if m.ID == id {
			return m, true
		}
	}
	return Placement{}, false
}

// Tile returns the legacy tile of id.
func (l Layout) Tile(id string) (Tile, bool) {
	for _, t := range l.Tiles {
		if t.ID == id {
			return t, true
		}
	}
	return Tile{}, false
}

// Has reports whether id is placed in the layout (either model).
func (l Layout) Has(id string) bool {
	if _, ok := l.Placement(id); ok {
		return true
	}
	_, ok := l.Tile(id)
	return ok
}

// IDs lists every machine the layout places, sorted.
func (l Layout) IDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range l.Machines {
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m.ID)
		}
	}
	for _, t := range l.Tiles {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Neighbour returns the machine directly adjacent to id on the given edge of
// the legacy tile grid. v2 uses boundary segments (see Boundaries) instead;
// this is kept for v1 peers and for a layout that has only tiles.
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
	for _, m := range l.Machines {
		if m.ID != id {
			out.Machines = append(out.Machines, m)
		}
	}
	for _, t := range l.Tiles {
		if t.ID != id {
			out.Tiles = append(out.Tiles, t)
		}
	}
	return out
}

// Normalized returns a copy with placements sorted by id and tiles by row
// then column, so equal layouts compare equal.
func (l Layout) Normalized() Layout {
	out := Layout{
		UpdatedAt: l.UpdatedAt,
		Machines:  append([]Placement(nil), l.Machines...),
		Tiles:     append([]Tile(nil), l.Tiles...),
	}
	sort.Slice(out.Machines, func(i, j int) bool { return out.Machines[i].ID < out.Machines[j].ID })
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

// ---------------------------------------------------------------- conversion

// TilesFromMachines derives the v1 tile grid from a canvas (§6 `layout`):
// machines whose world unions overlap vertically form one row, rows are
// ordered by their topmost union centre, and within a row the column is the
// rank by x centre. It only has to preserve left/right and up/down roughly.
func TilesFromMachines(machines []Placement, displays map[string][]Display) []Tile {
	if len(machines) == 0 {
		return nil
	}
	type entry struct {
		id     string
		u      Rect
		cx, cy int
	}
	es := make([]entry, 0, len(machines))
	for _, m := range machines {
		u := WorldUnion(m, displays[m.ID])
		es = append(es, entry{m.ID, u, u.X + u.W/2, u.Y + u.H/2})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].u.Y != es[j].u.Y {
			return es[i].u.Y < es[j].u.Y
		}
		if es[i].cx != es[j].cx {
			return es[i].cx < es[j].cx
		}
		return es[i].id < es[j].id
	})
	// Greedy rows: a machine joins the current row while its union overlaps
	// the row's vertical extent.
	var rows [][]entry
	hi := 0
	for _, e := range es {
		if len(rows) == 0 || e.u.Y >= hi {
			rows = append(rows, []entry{e})
			hi = e.u.Y + e.u.H
			continue
		}
		r := len(rows) - 1
		rows[r] = append(rows[r], e)
		if e.u.Y+e.u.H > hi {
			hi = e.u.Y + e.u.H
		}
	}
	var out []Tile
	for row, members := range rows {
		sort.Slice(members, func(i, j int) bool {
			if members[i].cx != members[j].cx {
				return members[i].cx < members[j].cx
			}
			return members[i].id < members[j].id
		})
		for col, e := range members {
			out = append(out, Tile{ID: e.id, Col: col, Row: row})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Row != out[j].Row {
			return out[i].Row < out[j].Row
		}
		return out[i].Col < out[j].Col
	})
	return out
}

// MachinesFromTiles arranges a legacy tile grid on the canvas (§6 `layout`):
// rows ascending, each row stacked under the previous ones, machines placed
// left to right by column with their world unions touching.
func MachinesFromTiles(tiles []Tile, displays map[string][]Display) []Placement {
	if len(tiles) == 0 {
		return nil
	}
	byRow := map[int][]Tile{}
	var rows []int
	for _, t := range tiles {
		if _, ok := byRow[t.Row]; !ok {
			rows = append(rows, t.Row)
		}
		byRow[t.Row] = append(byRow[t.Row], t)
	}
	sort.Ints(rows)
	var out []Placement
	y := 0
	for _, row := range rows {
		ts := byRow[row]
		sort.Slice(ts, func(i, j int) bool {
			if ts[i].Col != ts[j].Col {
				return ts[i].Col < ts[j].Col
			}
			return ts[i].ID < ts[j].ID
		})
		x, tallest := 0, 0
		for _, t := range ts {
			u := Union(displays[t.ID])
			if u.Empty() {
				u = Rect{0, 0, fallbackMachineW, fallbackMachineH}
			}
			// Place the machine so its union's top-left lands on (x, y).
			out = append(out, Placement{ID: t.ID, X: x - u.X, Y: y - u.Y})
			x += u.W
			if u.H > tallest {
				tallest = u.H
			}
		}
		y += tallest
	}
	return out
}

// Upgraded returns the layout in v2 form: Machines filled from Tiles when it
// only has tiles (a v1 peer or an old config file), Tiles derived from
// Machines otherwise. The second result says whether anything changed.
func (l Layout) Upgraded(displays map[string][]Display) (Layout, bool) {
	switch {
	case len(l.Machines) == 0 && len(l.Tiles) > 0:
		l.Machines = MachinesFromTiles(l.Tiles, displays)
		return l, true
	case len(l.Machines) > 0:
		tiles := TilesFromMachines(l.Machines, displays)
		if sameTiles(tiles, l.Tiles) {
			return l, false
		}
		l.Tiles = tiles
		return l, true
	}
	return l, false
}

func sameTiles(a, b []Tile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
