package core

import (
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
	"github.com/r-thirumaran/mac-utility/shared/keymap"
)

// Integration tests: real Transports on loopback TCP with real securechan
// handshakes (pairing codes and all), Bonjour disabled, fake input devices
// and fake clocks. They exercise the whole path a user sees: pair, lay out,
// push the pointer across an edge, type, come back, lose a peer.

func TestIntegrationTwoNodes(t *testing.T) {
	a := newMachine(t, "Left")
	b := newMachine(t, "Right", Display{ID: "r1", X: 0, Y: 0, W: 2560, H: 1440, Scale: 2, Primary: true})
	startNet(t, a, "windows")
	startNet(t, b, "macos")

	pair(t, a, b)
	// Both stores trust each other with the right keys; the acceptor learned
	// the initiator's listening port from `info.port`.
	if !a.store.IsTrusted(b.ID(), b.id.Pub) || !b.store.IsTrusted(a.ID(), a.id.Pub) {
		t.Fatal("stores do not trust each other after pairing")
	}
	waitFor(t, "B learns A's port", func() bool { return b.node.PeerAddr(a.ID()) == a.addr() })
	if a.node.PeerAddr(b.ID()) != b.addr() {
		t.Fatalf("A's address for B = %s, want %s", a.node.PeerAddr(b.ID()), b.addr())
	}
	// info exchanged: B knows A's displays and vice versa.
	waitFor(t, "displays exchanged", func() bool {
		sa, sb := a.node.State(), b.node.State()
		return len(sa.Peers) == 1 && len(sa.Peers[0].Displays) == 1 && sa.Peers[0].Displays[0].W == 2560 &&
			len(sb.Peers) == 1 && len(sb.Peers[0].Displays) == 1 && sb.Peers[0].Plat == "windows"
	})

	setRowLayout(t, a, b)
	a.clip.SetText("from Left")

	// A's pointer to its right edge at y 270 → B active, cursor on B's left
	// edge at the SAME world row (enter.pt), not at the same union fraction:
	// the canvas is to scale, so 270 on A is 270 on B although B is taller.
	a.dev.SetCursor(1900, 270)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() && a.active() == b.ID() })
	waitMoveTo(t, b, 1, 270)
	waitFor(t, "A locked", a.dev.Locked)
	waitFor(t, "clipboard", func() bool { s, _ := b.clip.Text(); return s == "from Left" })

	// Relative moves and keys reach B's injector in order.
	for i := 1; i <= 5; i++ {
		a.move(i, -i)
	}
	a.key(0x04, true)
	a.key(0x04, false)
	waitFor(t, "moves and keys on B", func() bool { return b.count("MoveRelative") == 5 && b.count("Key") == 2 })
	var moves []input.Call
	for _, c := range b.dev.Log() {
		if c.Op == "MoveRelative" {
			moves = append(moves, c)
		}
	}
	for i, c := range moves {
		if c.Args[0] != i+1 || c.Args[1] != -(i+1) {
			t.Fatalf("move %d = %+v", i, c)
		}
	}
	// Cross-platform (windows → macos) with the default swap: Ctrl becomes GUI on B.
	a.key(keymap.ModLeftControl, true)
	waitFor(t, "swapped modifier on B", func() bool { c, _ := b.lastCall("Key"); return c.Args[0] == keymap.ModLeftGUI && c.Args[1] == 1 })
	a.key(keymap.ModLeftControl, false)

	// Hotkey Ctrl+Alt+Home on A returns control.
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	a.key(usageHome, true)
	waitFor(t, "A active", func() bool { return a.active() == a.ID() && b.active() == a.ID() && !a.dev.Locked() })
	a.key(usageHome, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)

	// Pings keep the link alive across a long (fake) silence on A's clock:
	// A pings, B answers in real time, so A's lastSeen for B refreshes.
	for i := 0; i < 4; i++ {
		a.clock.Advance(PingInterval)
		a.node.Tick()
		time.Sleep(20 * time.Millisecond)
	}
	if !a.online(b) {
		t.Fatal("B dropped although it answered pings")
	}

	// Unpair from A: B's connection goes down and B shows A offline.
	if err := a.node.Forget(b.ID()); err != nil {
		t.Fatal(err)
	}
	a.tr.Unpair(b.ID())
	waitFor(t, "B sees A offline", func() bool { return !b.online(a) })
	if len(a.node.State().Peers) != 0 {
		t.Fatalf("A still lists peers: %+v", a.node.State().Peers)
	}
}

func TestIntegrationThreeNodesChain(t *testing.T) {
	a := newMachine(t, "A")
	b := newMachine(t, "B")
	c := newMachine(t, "C")
	for _, m := range []*machine{a, b, c} {
		startNet(t, m, "linux")
	}
	pair(t, a, b)
	pair(t, a, c)
	pair(t, b, c)
	setRowLayout(t, a, b, c)

	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active everywhere", func() bool { return a.active() == b.ID() && b.active() == b.ID() && c.active() == b.ID() })
	// Keep pushing right: B's injected cursor reaches B's right edge → C.
	a.move(5000, 0)
	waitFor(t, "C active everywhere", func() bool { return a.active() == c.ID() && b.active() == c.ID() && c.active() == c.ID() })
	waitMoveTo(t, c, 1, 540)
	// A forwards straight to C now.
	bMoves := b.count("MoveRelative")
	a.move(4, 2)
	waitFor(t, "move on C", func() bool { cc, ok := c.lastCall("MoveRelative"); return ok && cc.Args[0] == 4 && cc.Args[1] == 2 })
	if b.count("MoveRelative") != bMoves {
		t.Fatal("B received input meant for C")
	}
	a.key(0x05, true)
	a.key(0x05, false)
	waitFor(t, "keys on C", func() bool { return c.count("Key") == 2 })
	// Claim from A (hotkey) comes back through both hops.
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	a.key(usageHome, true)
	waitFor(t, "A active everywhere", func() bool { return a.active() == a.ID() && b.active() == a.ID() && c.active() == a.ID() })
	a.key(usageHome, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)
	// A is free again; B and C keep their cursors parked until they are active.
	waitFor(t, "locks A=free B=parked C=parked", func() bool { return !a.dev.Locked() && b.dev.Locked() && c.dev.Locked() })
	// Hotkey → direction from the active machine: Ctrl+Alt+→ hands to B centred.
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	a.key(usageRight, true)
	waitFor(t, "B active via hotkey", func() bool { return b.active() == b.ID() && c.active() == b.ID() })
	waitMoveTo(t, b, 960, 540)
	a.key(usageRight, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)
}

func TestIntegrationDroppedPeerReturnsControl(t *testing.T) {
	a := newMachine(t, "A")
	b := newMachine(t, "B")
	startNet(t, a, "linux")
	startNet(t, b, "linux")
	pair(t, a, b)
	setRowLayout(t, a, b)
	a.dev.SetCursor(1900, 540)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() })
	a.move(1, 0)
	waitFor(t, "forwarded", func() bool { return b.count("MoveRelative") == 1 })
	b.stop() // B vanishes: the socket closes
	waitFor(t, "A takes control back", func() bool { return a.active() == a.ID() && !a.online(b) && !a.dev.Locked() })
	if st := a.node.State(); len(st.Peers) != 1 || st.Peers[0].Status != "offline" || !st.Peers[0].Paired {
		t.Fatalf("peers = %+v", st.Peers)
	}
}

func TestIntegrationPairingWrongCodeThenRetry(t *testing.T) {
	a := newMachine(t, "A")
	b := newMachine(t, "B")
	startNet(t, a, "linux")
	startNet(t, b, "linux")
	if err := a.tr.PairStart("", b.addr()); err != nil {
		t.Fatal(err)
	}
	var code string
	waitFor(t, "code", func() bool {
		e, ok := b.log.find(func(e Event) bool { return e.Name == "edgehop.pairCode" })
		if ok {
			code = e.Data.(PairCode).Code
		}
		return ok
	})
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	if err := a.tr.PairConfirm("", wrong); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failure on A", func() bool {
		_, ok := a.log.find(func(e Event) bool {
			p, is := e.Data.(PairingState)
			return is && p.Stage == "failed" && p.Error == "wrong_code"
		})
		return ok
	})
	waitFor(t, "failure on B", func() bool {
		_, ok := b.log.find(func(e Event) bool { r, is := e.Data.(PairResult); return is && !r.OK })
		return ok
	})
	if _, ok := a.store.Peer(b.ID()); ok {
		t.Fatal("A trusts B after a failed pairing")
	}
	if _, ok := b.store.Peer(a.ID()); ok {
		t.Fatal("B trusts A after a failed pairing")
	}
	// Second attempt with the right code succeeds.
	pair(t, a, b)
	if _, ok := a.log.find(func(e Event) bool { p, is := e.Data.(PairingState); return is && p.Stage == "paired" }); !ok {
		t.Fatal("no paired event on A")
	}
	if _, ok := b.log.find(func(e Event) bool { r, is := e.Data.(PairResult); return is && r.OK }); !ok {
		t.Fatal("no ok pairResult on B")
	}
}

func TestIntegrationUnpairedIsRefused(t *testing.T) {
	a := newMachine(t, "A")
	b := newMachine(t, "B")
	startNet(t, a, "linux")
	startNet(t, b, "linux")
	if a.ID() > b.ID() { // the smaller id is the one that dials (§2)
		a, b = b, a
	}
	// A trusts B, but B has never paired with A: A's trusted handshake must
	// be answered with {"t":"error","code":"unpaired"} and no connection.
	if err := a.store.Trust(securechan.PeerInfo{ID: b.ID(), Name: "B", Plat: "linux", StaticPub: b.id.Pub}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetLastAddr(b.ID(), b.addr()); err != nil {
		t.Fatal(err)
	}
	a.tr.ensureDialer(b.ID())
	a.tr.Poke(b.ID())
	waitFor(t, "refusal logged", func() bool {
		return b.hasLog("refused unpaired connection") && a.hasLog("no longer trusts this machine")
	})
	if a.online(b) || b.online(a) || a.tr.Connected(b.ID()) {
		t.Fatal("an unpaired peer got a connection")
	}
}

// TestIntegrationBoundaryHopAndLocate is the Go-to-Go proof of layout v2 over
// the real transport: a boundary that covers only part of the shared edge, a
// hop that lands exactly on enter.pt, and a shake that travels as `locate`.
func TestIntegrationBoundaryHopAndLocate(t *testing.T) {
	a := newMachine(t, "A")
	b := newMachine(t, "B", Display{ID: "b1", X: 0, Y: 0, W: 2560, H: 1440, Scale: 1, Primary: true})
	startNet(t, a, "linux")
	startNet(t, b, "linux")
	pair(t, a, b)
	waitDisplays(t, a, b)

	// B sits 600 px lower on the canvas, so only y 600–1080 of A's right edge
	// is a boundary.
	l := Layout{Machines: []Placement{{ID: a.ID(), X: 0, Y: 0}, {ID: b.ID(), X: 1920, Y: 600}}}
	if err := a.node.SetLayout(l); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "canvas on B", func() bool { return len(b.node.Layout().Machines) == 2 })
	if _, ok := b.node.Layout().Tile(a.ID()); !ok {
		t.Fatal("no tiles derived for a v1 peer")
	}

	// Above the boundary the cursor just stops.
	a.dev.SetCursor(1900, 200)
	a.move(5000, 0)
	time.Sleep(30 * time.Millisecond)
	if a.active() != a.ID() {
		t.Fatal("hopped where the edge has no boundary")
	}

	// Inside the boundary: B becomes active with its cursor on the same world
	// row, i.e. 800 − 600 = 200 in B's own coordinates (enter.pt), not at the
	// union fraction a v1 receiver would use.
	a.dev.SetCursor(1900, 800)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() && a.active() == b.ID() })
	waitMoveTo(t, b, 1, 200)

	// A still owns the physical mouse: a shake there asks B for the overlay.
	a.shake()
	waitFor(t, "locate overlay on B", func() bool { return b.loc.count() == 1 })
	if a.loc.count() != 0 {
		t.Fatal("the forwarding machine showed the overlay itself")
	}

	// Back on A (hotkey Home), a shake shows the overlay locally.
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	a.key(usageHome, true)
	waitFor(t, "A active", func() bool { return a.active() == a.ID() })
	a.key(usageHome, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)
	a.clock.Advance(ShakeCooldown)
	// The claim put the pointer back where it was when control left, i.e. on
	// the boundary edge (§6.3); shake well inside, or the shake itself pushes
	// the pointer across again.
	a.dev.SetCursor(960, 400)
	a.shake()
	waitFor(t, "locate overlay on A", func() bool { return a.loc.count() == 1 })

	// Hotkey Ctrl+Alt+→ uses the canvas boundary, not a tile grid.
	a.dev.SetCursor(1900, 800)
	a.key(keymap.ModLeftControl, true)
	a.key(keymap.ModLeftAlt, true)
	a.key(usageRight, true)
	waitFor(t, "B active via hotkey", func() bool { return b.active() == b.ID() })
	waitMoveTo(t, b, 1280, 720) // centre of B's primary display
	a.key(usageRight, false)
	a.key(keymap.ModLeftAlt, false)
	a.key(keymap.ModLeftControl, false)
}

// A wobble back toward the edge just crossed must not bounce control straight
// back; a deliberate return (after moving ReentryGuardPx in) still hops.
func TestIntegrationNoBounceBack(t *testing.T) {
	a := newMachine(t, "Left")
	b := newMachine(t, "Right", Display{ID: "r1", X: 0, Y: 0, W: 2560, H: 1440, Scale: 2, Primary: true})
	startNet(t, a, "windows")
	startNet(t, b, "macos")
	pair(t, a, b)
	waitFor(t, "displays exchanged", func() bool {
		sa, sb := a.node.State(), b.node.State()
		return len(sa.Peers) == 1 && len(sa.Peers[0].Displays) == 1 && len(sb.Peers) == 1 && len(sb.Peers[0].Displays) == 1
	})
	setRowLayout(t, a, b)

	a.dev.SetCursor(1900, 270)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() && a.active() == b.ID() })
	waitMoveTo(t, b, 1, 270)

	// Hand wobbles 3 px back: B's cursor is clamped onto its left edge (x 0).
	a.move(-3, 0)
	waitFor(t, "wobble injected on B", func() bool { return b.count("MoveRelative") == 1 })
	time.Sleep(150 * time.Millisecond)
	if b.active() != b.ID() || a.active() != b.ID() {
		t.Fatalf("control bounced back after a 3 px wobble: a=%s b=%s", a.active(), b.active())
	}

	// Move well inside, then deliberately back across: control returns to A.
	a.move(ReentryGuardPx+20, 0)
	waitFor(t, "moved in on B", func() bool { return b.count("MoveRelative") == 2 })
	a.move(-500, 0)
	waitFor(t, "A active again", func() bool { return a.active() == a.ID() && b.active() == a.ID() })
}
