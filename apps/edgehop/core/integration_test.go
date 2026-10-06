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

	// A's pointer to its right edge at 25 % height → B active, cursor on B's
	// left edge at the same fraction of B's (taller) union.
	a.dev.SetCursor(1900, 270)
	a.move(5000, 0)
	waitFor(t, "B active", func() bool { return b.active() == b.ID() && a.active() == b.ID() })
	waitMoveTo(t, b, 1, 360)
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
