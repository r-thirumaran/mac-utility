package core

import (
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
	"github.com/r-thirumaran/mac-utility/shared/keymap"
)

// Unit tests of the Node state machine over the in-process stub network
// (no crypto, no sockets). The integration tests cover the real transport.

// stubMachines builds n machines on one stub network, all paired and
// connected, with the given platforms.
func stubMachines(t *testing.T, plats ...string) (*stubNet, []*machine) {
	t.Helper()
	net := newStubNet()
	var ms []*machine
	for i, plat := range plats {
		m := newMachine(t, string(rune('A'+i)))
		m.buildNode(net.sender(m.ID()), plat)
		ms = append(ms, m)
	}
	// Trust everybody first so State() lists them as paired.
	for _, m := range ms {
		for _, o := range ms {
			if o != m {
				if err := m.store.Trust(securechan.PeerInfo{ID: o.ID(), Name: o.name, Plat: o.node.self.Plat, StaticPub: o.id.Pub}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, m := range ms {
		if err := m.node.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.node.Stop)
		net.connect(m.ID(), m.node, securechan.PeerInfo{ID: m.ID(), Name: m.name, Plat: m.node.self.Plat, StaticPub: m.id.Pub})
	}
	for _, m := range ms {
		for _, o := range ms {
			if o != m {
				m, o := m, o
				waitFor(t, m.name+" sees "+o.name, func() bool { return m.online(o) })
			}
		}
	}
	waitDisplays(t, ms...)
	return net, ms
}

func rowLayout(ms ...*machine) Layout { return canvasRow(ms...) }

func TestStubEdgeSwitchAndForwarding(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout on B", func() bool { return len(b.node.Layout().Machines) == 2 })
	a.clip.SetText("copied on A")

	// Pointer to A's right edge in the middle row.
	a.dev.SetCursor(1000, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() && a.active() == b.ID() })
	waitMoveTo(t, b, 1, 540)
	waitFor(t, "A locked", a.dev.Locked)
	if !a.dev.Locked() {
		t.Fatal("A's cursor should be locked while B is active")
	}
	waitFor(t, "clipboard on B", func() bool { txt, _ := b.clip.Text(); return txt == "copied on A" })
	st := a.node.State()
	if st.Role != "sending" || st.Active != b.ID() || st.Self.Active {
		t.Fatalf("A state = role %s active %s", st.Role, short(st.Active))
	}

	// Relative moves, a button and a key are forwarded and injected on B.
	if !a.move(10, -3) {
		t.Fatal("forwarded move must be swallowed")
	}
	waitFor(t, "MoveRelative on B", func() bool {
		c, ok := b.lastCall("MoveRelative")
		return ok && c.Args[0] == 10 && c.Args[1] == -3
	})
	a.dev.Emit(input.Event{Kind: input.MouseButton, Button: input.ButtonLeft, Down: true, ClickCount: 1})
	a.dev.Emit(input.Event{Kind: input.MouseButton, Button: input.ButtonLeft, Down: false, ClickCount: 1})
	a.key(0x04, true)
	a.key(0x04, false)
	a.dev.Emit(input.Event{Kind: input.Scroll, DX: 0, DY: -120, Flags: input.ScrollDiscrete})
	a.dev.Emit(input.Event{Kind: input.KeyText, Text: "é"})
	waitFor(t, "input on B", func() bool {
		return b.count("Button") == 2 && b.count("Key") == 2 && b.count("Scroll") == 1 && b.count("Text") == 1
	})
	if c, _ := b.lastCall("Key"); c.Args[0] != 0x04 || c.Args[1] != 0 {
		t.Fatalf("B last Key = %+v", c)
	}
	if c, _ := b.lastCall("Scroll"); c.Args[1] != -120 || c.Args[2] != 1 {
		t.Fatalf("B Scroll = %+v", c)
	}
	if b.node.State().Role != "receiving" {
		t.Fatalf("B role = %s", b.node.State().Role)
	}

	// Ctrl+Alt+Home on A claims control back; the modifier presses were
	// forwarded, the hotkey itself was swallowed.
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	if !a.key(usageHome, true) {
		t.Fatal("hotkey must be swallowed")
	}
	waitFor(t, "A active again", func() bool { return a.active() == a.ID() && b.active() == a.ID() && !a.dev.Locked() })
	a.key(usageHome, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)
	// Local moves are not forwarded any more.
	before := b.count("MoveRelative")
	a.dev.SetCursor(500, 500)
	if a.move(1, 1) {
		t.Fatal("local move must not be swallowed")
	}
	time.Sleep(20 * time.Millisecond)
	if b.count("MoveRelative") != before {
		t.Fatal("move forwarded after claiming control back")
	}
}

func TestStubCornersButtonsAndOfflineNeighbour(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout on B", func() bool { return len(b.node.Layout().Machines) == 2 })

	// Corner dead zone: no switch in the top 32 px of the edge.
	a.dev.SetCursor(1900, 10)
	a.move(5000, 0)
	time.Sleep(20 * time.Millisecond)
	if a.active() != a.ID() {
		t.Fatal("switched inside the corner dead zone")
	}
	// Button held: no switch (dragging).
	a.dev.SetCursor(1900, 540)
	a.dev.Emit(input.Event{Kind: input.MouseButton, Button: input.ButtonLeft, Down: true, ClickCount: 1})
	a.move(5000, 0)
	time.Sleep(20 * time.Millisecond)
	if a.active() != a.ID() {
		t.Fatal("switched with a button held")
	}
	a.dev.Emit(input.Event{Kind: input.MouseButton, Button: input.ButtonLeft, Down: false, ClickCount: 1})
	// Left edge has no neighbour.
	a.dev.SetCursor(100, 540)
	a.move(-5000, 0)
	time.Sleep(20 * time.Millisecond)
	if a.active() != a.ID() {
		t.Fatal("switched towards an empty cell")
	}
	// Neighbour offline: no switch.
	a.node.PeerDown(b.ID())
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	time.Sleep(20 * time.Millisecond)
	if a.active() != a.ID() {
		t.Fatal("switched to an offline neighbour")
	}
}

func TestStubSwitchDelayFakeClock(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	s := a.node.Settings()
	s.SwitchDelayMs = 200
	if err := a.node.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	if a.active() != a.ID() {
		t.Fatal("switched before the delay")
	}
	// Leaving the edge resets the timer.
	a.clock.Advance(150 * time.Millisecond)
	a.move(-10, 0)
	a.clock.Advance(100 * time.Millisecond)
	a.node.Tick()
	if a.active() != a.ID() {
		t.Fatal("switched after leaving the edge")
	}
	// Back on the edge, dwell 200 ms (fake clock), then a re-check switches.
	a.move(5000, 0)
	a.clock.Advance(250 * time.Millisecond)
	a.node.Tick()
	waitFor(t, "B active after the delay", func() bool { return b.active() == b.ID() })
}

func TestStubThreeNodesAndClaim(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux", "linux")
	a, b, c := ms[0], ms[1], ms[2]
	if err := a.node.SetLayout(rowLayout(a, b, c)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout everywhere", func() bool { return len(b.node.Layout().Machines) == 3 && len(c.node.Layout().Machines) == 3 })

	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return a.active() == b.ID() && b.active() == b.ID() && c.active() == b.ID() })
	// Pushing further moves B's (injected) cursor to its right edge → C.
	a.move(5000, 0)
	waitFor(t, "C active", func() bool { return a.active() == c.ID() && b.active() == c.ID() && c.active() == c.ID() })
	waitMoveTo(t, c, 1, 540)
	waitFor(t, "A and B locked while C is active", func() bool { return a.dev.Locked() && b.dev.Locked() })
	// A now forwards straight to C.
	bBefore := b.count("MoveRelative")
	a.move(7, 0)
	waitFor(t, "move on C", func() bool { cc, ok := c.lastCall("MoveRelative"); return ok && cc.Args[0] == 7 })
	if b.count("MoveRelative") != bBefore {
		t.Fatal("B received a move meant for C")
	}
	// Claim from the UI (Claim(self)) returns control to A.
	if err := a.node.Claim(a.ID()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "A active", func() bool { return a.active() == a.ID() && b.active() == a.ID() && c.active() == a.ID() })
	// Claim(peer) from the UI hands over without an entry edge: cursor centred.
	if err := a.node.Claim(c.ID()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "C active via menu", func() bool { return c.active() == c.ID() && b.active() == c.ID() })
	waitMoveTo(t, c, 960, 540)
	if err := a.node.Claim(a.ID()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "A active again", func() bool { return a.active() == a.ID() && c.active() == a.ID() })
}

func TestStubDroppedActivePeerReturnsControl(t *testing.T) {
	net, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() })
	a.move(3, 3)
	waitFor(t, "forwarded", func() bool { return b.count("MoveRelative") >= 1 })
	net.disconnect(a.ID(), b.ID())
	waitFor(t, "A takes control back", func() bool { return a.active() == a.ID() && !a.dev.Locked() })
	if st := a.node.State(); st.Peers[0].Status != "offline" {
		t.Fatalf("B status = %s", st.Peers[0].Status)
	}
}

func TestStubLivenessTimeout(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() })
	// B stops answering (its clock does not advance, so it never answers in
	// A's time frame): after PeerTimeout on A's clock, A declares B gone.
	a.clock.Advance(PingInterval + time.Second)
	a.node.Tick()
	if a.active() != a.ID() == false && !a.online(b) {
		t.Fatal("B dropped too early")
	}
	a.clock.Advance(PeerTimeout)
	a.node.Tick()
	waitFor(t, "A active after B went silent", func() bool { return a.active() == a.ID() && !a.online(b) })
}

func TestStubStaleActiveIgnoredAndInputGated(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	// At start every machine is active and accepts input (§1).
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(5, 5))
	waitFor(t, "input at start", func() bool { return b.count("MoveRelative") == 1 })
	// A says it is active (seq 2): B stops injecting once the grace window passed.
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{T: TActive, ID: a.ID(), From: b.ID(), Seq: 2}))
	waitFor(t, "B inactive", func() bool { return b.active() == a.ID() })
	b.clock.Advance(ActiveGrace + time.Millisecond)
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(5, 5))
	time.Sleep(10 * time.Millisecond)
	if b.count("MoveRelative") != 1 {
		t.Fatal("input injected while not active")
	}
	b.dev.ClearLog()
	// A fresh `active` naming B (seq 5) makes it active; an older seq is ignored.
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{T: TActive, ID: b.ID(), From: a.ID(), Seq: 5}))
	waitFor(t, "B active", func() bool { return b.active() == b.ID() })
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{T: TActive, ID: a.ID(), From: b.ID(), Seq: 4}))
	time.Sleep(10 * time.Millisecond)
	if b.active() != b.ID() {
		t.Fatal("stale active accepted")
	}
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(5, 5))
	waitFor(t, "input injected", func() bool { return b.count("MoveRelative") == 1 })
	// A later seq hands it back.
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{T: TActive, ID: a.ID(), From: b.ID(), Seq: 6}))
	waitFor(t, "B inactive", func() bool { return b.active() == a.ID() })
	// Within the grace window input is still accepted (clock skew), after it not.
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(1, 1))
	waitFor(t, "grace input", func() bool { return b.count("MoveRelative") == 2 })
	b.clock.Advance(ActiveGrace + time.Millisecond)
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(1, 1))
	time.Sleep(10 * time.Millisecond)
	if b.count("MoveRelative") != 2 {
		t.Fatal("input accepted after the grace window")
	}
}

func TestStubSwapCmdCtrlCrossPlatform(t *testing.T) {
	_, ms := stubMachines(t, "macos", "windows")
	mac, win := ms[0], ms[1]
	if err := mac.node.SetLayout(rowLayout(mac, win)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout", func() bool { return len(win.node.Layout().Machines) == 2 })
	mac.dev.SetCursor(1900, 540)
	mac.move(5000, 0)
	waitFor(t, "win active", func() bool { return win.active() == win.ID() })
	// ⌘C on the Mac keyboard: LGUI down, C down with GUI held.
	mac.key(keymap.ModLeftGUI, true)
	mac.dev.Emit(input.Event{Kind: input.Key, Wire: 0x06, Down: true, Mods: 0x08})
	waitFor(t, "keys on win", func() bool { return win.count("Key") == 2 })
	log := win.dev.Log()
	var keys []input.Call
	for _, c := range log {
		if c.Op == "Key" {
			keys = append(keys, c)
		}
	}
	if keys[0].Args[0] != keymap.ModLeftControl || keys[1].Args[0] != 0x06 || keys[1].Args[2] != 0x01 {
		t.Fatalf("swap not applied: %+v", keys)
	}
	mac.dev.Emit(input.Event{Kind: input.Key, Wire: 0x06, Down: false, Mods: 0x08})
	mac.key(keymap.ModLeftGUI, false)
	// With the swap off the usages pass through.
	s := win.node.Settings()
	s.SwapCmdCtrl = false
	if err := win.node.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	mac.key(keymap.ModLeftGUI, true)
	waitFor(t, "plain GUI", func() bool { c, _ := win.lastCall("Key"); return c.Args[0] == keymap.ModLeftGUI && c.Args[1] == 1 })
	mac.key(keymap.ModLeftGUI, false)
}

func TestStubLayoutNewestWinsAndForget(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout on B", func() bool { return len(b.node.Layout().Machines) == 2 })
	// An older layout from a peer is ignored.
	old := Layout{UpdatedAt: 1, Tiles: []Tile{{ID: a.ID(), Col: 0, Row: 1}}}
	b.node.OnPeerMessage(a.ID(), MustControl(LayoutMsg{T: TLayout, UpdatedAt: old.UpdatedAt, Tiles: old.Tiles}))
	time.Sleep(10 * time.Millisecond)
	if len(b.node.Layout().Tiles) != 2 {
		t.Fatal("older layout replaced the current one")
	}
	// A tiles-only layout from a v1 peer is arranged on the canvas and kept
	// as v2 from there on.
	b.clock.Advance(time.Minute)
	v1 := Layout{UpdatedAt: b.clock.Now().UnixMilli(), Tiles: []Tile{{ID: a.ID(), Col: 0, Row: 0}, {ID: b.ID(), Col: 1, Row: 0}}}
	b.node.OnPeerMessage(a.ID(), MustControl(LayoutMsg{T: TLayout, UpdatedAt: v1.UpdatedAt, Tiles: v1.Tiles}))
	waitFor(t, "v1 layout converted on B", func() bool {
		p, ok := b.node.Layout().Placement(b.ID())
		return ok && p.X == 1920
	})
	// B edits the canvas: its layout is newer and wins on A.
	b.clock.Advance(time.Minute)
	l := Layout{Machines: []Placement{{ID: a.ID(), X: 0, Y: 1080}, {ID: b.ID(), X: 0, Y: 0}}}
	if err := b.node.SetLayout(l); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "layout on A", func() bool {
		p, ok := a.node.Layout().Placement(a.ID())
		return ok && p.Y == 1080
	})
	if _, ok := b.node.Layout().Placement(a.ID()); !ok {
		t.Fatal("placement missing")
	}
	// The derived tiles still describe up/down for a v1 peer.
	if tl, ok := a.node.Layout().Tile(a.ID()); !ok || tl.Row != 1 {
		t.Fatalf("derived tile for A = %+v %v", tl, ok)
	}
	// Forget removes the placement and the peer.
	if err := a.node.Forget(b.ID()); err != nil {
		t.Fatal(err)
	}
	if a.node.Layout().Has(b.ID()) {
		t.Fatal("layout still has B after Forget")
	}
	if len(a.node.State().Peers) != 0 {
		t.Fatalf("peers after Forget: %+v", a.node.State().Peers)
	}
	if _, ok := a.store.Peer(b.ID()); ok {
		t.Fatal("store still trusts B")
	}
}

func TestStateShape(t *testing.T) {
	_, ms := stubMachines(t, "windows", "macos")
	a := ms[0]
	st := a.node.State()
	if st.Self.ID != a.ID() || st.Self.Plat != "windows" || st.Self.Status != "self" || !st.Self.Paired || !st.Self.Active {
		t.Fatalf("self = %+v", st.Self)
	}
	if len(st.Peers) != 1 || st.Peers[0].Plat != "macos" || !st.Peers[0].Paired || st.Peers[0].Status != "online" {
		t.Fatalf("peers = %+v", st.Peers)
	}
	if st.Role != "idle" || st.Active != a.ID() || st.Layout.Tiles == nil || st.Permissions == nil {
		t.Fatalf("state = %+v", st)
	}
	if st.Settings != DefaultSettings() {
		t.Fatalf("settings = %+v", st.Settings)
	}
	// Displays change broadcasts and shows up on the peer.
	a.dev.SetDisplays([]Display{{ID: "x", W: 800, H: 600, Primary: true}, {ID: "y", X: 800, W: 800, H: 600}})
	waitFor(t, "displays on B", func() bool {
		for _, p := range ms[1].node.State().Peers {
			if p.ID == a.ID() && len(p.Displays) == 2 {
				return true
			}
		}
		return false
	})
	if err := a.node.SetName("Renamed"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "name on B", func() bool { return ms[1].node.State().Peers[0].Name == "Renamed" })
}

func TestStubEnterPointHonouredAndSnapped(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	// enter.pt wins over edge+frac (which would land at y = 972).
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{
		T: TActive, ID: b.ID(), From: a.ID(), Seq: 2,
		Enter: &Enter{Edge: EdgeLeft, Frac: 0.9, Pt: &Point{X: 42, Y: 99}},
	}))
	waitMoveTo(t, b, 42, 99)
	// A point off the receiver's desktop is snapped onto a display.
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{
		T: TActive, ID: b.ID(), From: a.ID(), Seq: 3,
		Enter: &Enter{Edge: EdgeLeft, Frac: 0.5, Pt: &Point{X: -500, Y: 9000}},
	}))
	waitMoveTo(t, b, 0, 1079)
	// Without pt a v1 `active` still uses edge + frac.
	b.node.OnPeerMessage(a.ID(), MustControl(ActiveMsg{
		T: TActive, ID: b.ID(), From: a.ID(), Seq: 4,
		Enter: &Enter{Edge: EdgeLeft, Frac: 0.25},
	}))
	waitMoveTo(t, b, 1, 270)
}

func TestStubUnknownControlIsIgnored(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	// §6: an unknown control `t` must be dropped, not answered with an error
	// and not a reason to close the connection (unlike an unknown type byte).
	for _, body := range []any{
		map[string]any{"t": "locate", "extra": 1},
		map[string]any{"t": "future_thing", "x": []int{1, 2}},
		map[string]any{"t": "nope"},
	} {
		b.node.OnPeerMessage(a.ID(), MustControl(body))
	}
	time.Sleep(20 * time.Millisecond)
	if !b.online(a) || !a.online(b) {
		t.Fatal("an unknown control closed the connection")
	}
	// The link still works afterwards.
	b.node.OnPeerMessage(a.ID(), EncodeMouseMove(4, 4))
	waitFor(t, "input still accepted", func() bool { return b.count("MoveRelative") >= 1 })
}

func TestStubShakeToLocate(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	// Active here and not macOS: the overlay is shown locally.
	a.shake()
	waitFor(t, "overlay on A", func() bool { return a.loc.count() == 1 })

	// Forwarding: the shake travels to the active machine instead.
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return a.active() == b.ID() })
	a.clock.Advance(ShakeCooldown)
	a.shake()
	waitFor(t, "overlay on B", func() bool { return b.loc.count() == 1 })
	if a.loc.count() != 1 {
		t.Fatalf("A showed the overlay again (%d)", a.loc.count())
	}

	// Switched off, nothing happens at all.
	s := a.node.Settings()
	s.ShakeToLocate = false
	if err := a.node.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	a.clock.Advance(ShakeCooldown)
	a.shake()
	time.Sleep(30 * time.Millisecond)
	if b.loc.count() != 1 || a.loc.count() != 1 {
		t.Fatalf("locate fired while switched off (A %d, B %d)", a.loc.count(), b.loc.count())
	}
}

func TestStubShakeOnMacOSIsLeftToTheOS(t *testing.T) {
	_, ms := stubMachines(t, "macos", "linux")
	mac := ms[0]
	// macOS has its own shake-to-locate, so an active Mac does nothing.
	mac.shake()
	time.Sleep(30 * time.Millisecond)
	if mac.loc.count() != 0 {
		t.Fatalf("macOS showed its own overlay (%d)", mac.loc.count())
	}
}

func TestStubPeerDisplaysPersistAndDrawOffline(t *testing.T) {
	_, ms := stubMachines(t, "linux", "linux")
	a, b := ms[0], ms[1]
	if err := a.node.SetLayout(rowLayout(a, b)); err != nil {
		t.Fatal(err)
	}
	// B's displays reached A's config, so a fresh node on the same store
	// still knows where B's screens are (and keeps its boundaries).
	waitFor(t, "B's displays stored on A", func() bool {
		p, ok := a.store.Peer(b.ID())
		return ok && len(p.Displays) == 1 && p.Displays[0].W == 1920
	})
	fresh := New(Config{ID: a.ID(), Name: "A2", Plat: "linux", Store: a.store, Capturer: a.dev, Injector: a.dev, Now: a.clock.Now})
	bs := Boundaries(fresh.Layout(), a.ID(), fresh.allDisplays())
	if len(bs) != 1 || bs[0].Target != b.ID() || bs[0].Edge != EdgeRight {
		t.Fatalf("boundaries from the stored displays = %+v", bs)
	}
	// An offline peer is still listed with its last known displays.
	a.node.PeerDown(b.ID())
	st := a.node.State()
	if len(st.Peers) != 1 || st.Peers[0].Status != "offline" || len(st.Peers[0].Displays) != 1 {
		t.Fatalf("offline peer = %+v", st.Peers)
	}
	if st.Layout.Machines == nil {
		t.Fatal("state layout has no machines field")
	}
}
