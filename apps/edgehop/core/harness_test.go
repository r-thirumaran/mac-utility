package core

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

// Test harness: machines with a fake clock, a fake input device, an
// in-memory store, a Node and (for the integration tests) a real Transport
// on loopback TCP with Bonjour disabled.

const waitTimeout = 15 * time.Second

// fakeClock is a settable clock for the Node (switch delay, liveness).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// eventLog collects every UI event a machine produced.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *eventLog) all() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

// find returns the first event satisfying pred.
func (l *eventLog) find(pred func(Event) bool) (Event, bool) {
	for _, e := range l.all() {
		if pred(e) {
			return e, true
		}
	}
	return Event{}, false
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// machine is one simulated computer.
type machine struct {
	t     *testing.T
	name  string
	id    securechan.Identity
	store *PeerStore
	dev   *input.FakeDevice
	clip  *MemClipboard
	clock *fakeClock
	node  *Node
	tr    *Transport
	log   *eventLog
	loc   *locateSpy
	stop  func()

	linesMu sync.Mutex
	lines   []string // log lines from the node and the transport
}

func (m *machine) logf(level, msg string) {
	m.t.Logf("[%s %s] %s", m.name, level, msg)
	m.linesMu.Lock()
	m.lines = append(m.lines, msg)
	m.linesMu.Unlock()
}

// hasLog reports whether any log line contains s.
func (m *machine) hasLog(s string) bool {
	m.linesMu.Lock()
	defer m.linesMu.Unlock()
	for _, l := range m.lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// newMachine builds a machine without a transport (unit tests wire a stub).
func newMachine(t *testing.T, name string, displays ...Display) *machine {
	t.Helper()
	id, err := securechan.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m := &machine{t: t, name: name, id: id, store: NewMemoryStore(), clock: newFakeClock(), log: &eventLog{}, loc: &locateSpy{}}
	m.dev = input.NewFake(displays...)
	m.clip = &MemClipboard{}
	return m
}

func (m *machine) buildNode(sender Sender, plat string) {
	m.node = New(Config{
		ID: m.id.ID(), Name: m.name, Plat: plat, Version: "test",
		Store: m.store, Capturer: m.dev, Injector: m.dev, Transport: sender, Clipboard: m.clip,
		Now: m.clock.Now, Locator: m.loc,
	})
	go func() {
		for e := range m.node.Events() {
			m.log.add(e)
		}
	}()
}

// startNet gives the machine a real Transport on loopback and starts it.
func startNet(t *testing.T, m *machine, plat string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m.tr = NewTransport(TransportConfig{
		Self: m.id, Name: m.name, Plat: plat, Store: m.store, Port: 0, DisableMDNS: true,
		Emit: m.log.add,
		Logf: m.logf,
	})
	m.buildNode(m.tr, plat)
	m.tr.cfg.Handler = m.node
	m.tr.cfg.AddrFor = m.node.PeerAddr
	m.node.cfg.Logf = m.logf
	if err := m.tr.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	m.node.SetPort(m.tr.Port())
	if err := m.node.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	m.stop = func() {
		once.Do(func() {
			m.node.Stop()
			m.tr.Stop()
			cancel()
		})
	}
	t.Cleanup(m.stop)
}

func (m *machine) addr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(m.tr.Port()))
}

func (m *machine) ID() string { return m.id.ID() }

func (m *machine) active() string { return m.node.Active() }

func (m *machine) online(peer *machine) bool {
	for _, p := range m.node.State().Peers {
		if p.ID == peer.ID() && p.Status == "online" {
			return true
		}
	}
	return false
}

// pair runs a pairing handshake from a (initiator, by address) to b and
// waits until both sides report each other online.
func pair(t *testing.T, a, b *machine) {
	t.Helper()
	seen := len(b.log.all())
	if err := a.tr.PairStart("", b.addr()); err != nil {
		t.Fatal(err)
	}
	var code string
	waitFor(t, fmt.Sprintf("pair code on %s", b.name), func() bool {
		for _, e := range b.log.all()[seen:] {
			if e.Name == "edgehop.pairCode" {
				code = e.Data.(PairCode).Code
				return true
			}
		}
		return false
	})
	if len(code) != 6 {
		t.Fatalf("code %q", code)
	}
	if err := a.tr.PairConfirm("", code); err != nil {
		t.Fatal(err)
	}
	waitFor(t, fmt.Sprintf("%s and %s online", a.name, b.name), func() bool { return a.online(b) && b.online(a) })
}

// canvasRow lays the machines out left to right on the world canvas, each
// one's desktop union touching the previous one's right edge (layout v2).
func canvasRow(ms ...*machine) Layout {
	l := Layout{}
	x := 0
	for _, m := range ms {
		u := Union(m.dev.Displays())
		l.Machines = append(l.Machines, Placement{ID: m.ID(), X: x - u.X, Y: -u.Y})
		x += u.W
	}
	return l
}

// waitDisplays waits until every machine knows every other's monitors: the
// canvas and its boundary segments are derived from them (§6 `layout`).
func waitDisplays(t *testing.T, ms ...*machine) {
	t.Helper()
	for _, m := range ms {
		for _, o := range ms {
			if o == m {
				continue
			}
			m, o := m, o
			waitFor(t, m.name+" knows "+o.name+"'s displays", func() bool {
				for _, p := range m.node.State().Peers {
					if p.ID == o.ID() && len(p.Displays) > 0 {
						return true
					}
				}
				return false
			})
		}
	}
}

// setRowLayout publishes a canvas row from the first machine and waits until
// every machine has it.
func setRowLayout(t *testing.T, ms ...*machine) {
	t.Helper()
	waitDisplays(t, ms...)
	if err := ms[0].node.SetLayout(canvasRow(ms...)); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		m := m
		waitFor(t, "layout on "+m.name, func() bool { return len(m.node.Layout().Machines) == len(ms) })
	}
}

// locateSpy counts the shake-to-locate overlays a machine was asked to show.
type locateSpy struct {
	mu sync.Mutex
	n  int
}

func (l *locateSpy) ShowLocate() {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *locateSpy) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// shake feeds the horizontal reversals of §6.3 as physical mouse moves: five
// 60-unit strokes alternating direction, i.e. four counting reversals.
func (m *machine) shake() {
	for _, dx := range []int{60, -60, 60, -60, 60} {
		m.move(dx, 0)
	}
}

// move feeds a physical relative mouse move.
func (m *machine) move(dx, dy int) bool {
	return m.dev.Emit(input.Event{Kind: input.MouseMove, DX: dx, DY: dy})
}

// key feeds a physical key press or release.
func (m *machine) key(wire int, down bool) bool {
	return m.dev.Emit(input.Event{Kind: input.Key, Wire: wire, Down: down})
}

func (m *machine) count(op string) int { return m.dev.Count(op) }

// waitMoveTo waits until the machine's injector was asked to warp the
// cursor to (x, y) (the entry point of a hand-over).
func waitMoveTo(t *testing.T, m *machine, x, y int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("MoveTo(%d,%d) on %s", x, y, m.name), func() bool {
		c, ok := m.lastCall("MoveTo")
		return ok && c.Args[0] == x && c.Args[1] == y
	})
}

// lastCall returns the most recent recorded injector call with op.
func (m *machine) lastCall(op string) (input.Call, bool) {
	log := m.dev.Log()
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Op == op {
			return log[i], true
		}
	}
	return input.Call{}, false
}

// ---------------------------------------------------------------- stub network

// stubNet links Nodes in-process with ordered, asynchronous delivery and
// no crypto, for the unit tests of the state machine.
type stubNet struct {
	mu    sync.Mutex
	nodes map[string]*Node
	infos map[string]securechan.PeerInfo
	links map[string]map[string]*stubLink // from → to
	down  map[string]bool
}

type stubLink struct {
	ch chan []byte
}

func newStubNet() *stubNet {
	return &stubNet{nodes: map[string]*Node{}, infos: map[string]securechan.PeerInfo{}, links: map[string]map[string]*stubLink{}, down: map[string]bool{}}
}

// sender is the per-node Sender view of the stub network.
type stubSender struct {
	net *stubNet
	id  string
}

func (s *stubSender) Send(peerID string, frame []byte) error { return s.net.send(s.id, peerID, frame) }
func (s *stubSender) Broadcast(frame []byte) {
	s.net.mu.Lock()
	peers := make([]string, 0, len(s.net.nodes))
	for id := range s.net.nodes {
		if id != s.id {
			peers = append(peers, id)
		}
	}
	s.net.mu.Unlock()
	for _, p := range peers {
		_ = s.net.send(s.id, p, frame)
	}
}
func (s *stubSender) Disconnect(peerID string) { s.net.disconnect(s.id, peerID) }

func (n *stubNet) sender(id string) *stubSender { return &stubSender{net: n, id: id} }

// connect registers node and tells every existing node about it (PeerUp both ways).
func (n *stubNet) connect(id string, node *Node, info securechan.PeerInfo) {
	n.mu.Lock()
	others := map[string]*Node{}
	for oid, o := range n.nodes {
		others[oid] = o
	}
	n.nodes[id] = node
	n.infos[id] = info
	n.mu.Unlock()
	for oid, o := range others {
		o.PeerUp(info, "stub:"+id)
		node.PeerUp(n.infos[oid], "stub:"+oid)
	}
}

func (n *stubNet) link(from, to string) *stubLink {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.links[from] == nil {
		n.links[from] = map[string]*stubLink{}
	}
	l := n.links[from][to]
	if l == nil {
		l = &stubLink{ch: make(chan []byte, 4096)}
		n.links[from][to] = l
		target := n.nodes[to]
		go func() {
			for frame := range l.ch {
				target.OnPeerMessage(from, frame)
			}
		}()
	}
	return l
}

func (n *stubNet) send(from, to string, frame []byte) error {
	n.mu.Lock()
	_, ok := n.nodes[to]
	down := n.down[from+">"+to] || n.down[to+">"+from]
	n.mu.Unlock()
	if !ok || down {
		return fmt.Errorf("stub: %s not connected", to)
	}
	n.link(from, to).ch <- frame
	return nil
}

// disconnect severs both directions and reports PeerDown on both sides.
func (n *stubNet) disconnect(a, b string) {
	n.mu.Lock()
	if n.down[a+">"+b] {
		n.mu.Unlock()
		return
	}
	n.down[a+">"+b] = true
	na, nb := n.nodes[a], n.nodes[b]
	n.mu.Unlock()
	if na != nil {
		na.PeerDown(b)
	}
	if nb != nil {
		nb.PeerDown(a)
	}
}
