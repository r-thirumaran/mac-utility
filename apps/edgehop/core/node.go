package core

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
	"github.com/r-thirumaran/mac-utility/shared/keymap"
)

// Liveness constants (docs/edgehop-protocol.md §6 ping/pong) and the input
// acceptance window after an `active` naming this machine (§7).
const (
	PingInterval    = 5 * time.Second
	PeerTimeout     = 15 * time.Second
	ActiveGrace     = 250 * time.Millisecond
	receivingWindow = 2 * time.Second
)

// Sender is what the Node needs from the network layer (*Transport implements it).
type Sender interface {
	// Send queues one encrypted frame payload (type byte || body) for peerID.
	Send(peerID string, frame []byte) error
	// Broadcast queues the frame for every connected peer.
	Broadcast(frame []byte)
	// Disconnect drops the connection to peerID (it may reconnect later).
	Disconnect(peerID string)
}

// Locator shows the shake-to-locate overlay around the local cursor
// (docs/edgehop-protocol.md §6.3 `locate`). Platforms without an overlay
// (macOS has its own) leave Config.Locator nil.
type Locator interface {
	ShowLocate()
}

// LocatorFunc adapts a function to Locator.
type LocatorFunc func()

// ShowLocate calls f.
func (f LocatorFunc) ShowLocate() { f() }

// Clipboard is the local text clipboard.
type Clipboard interface {
	Text() (string, bool)
	SetText(string)
}

// MemClipboard is an in-memory Clipboard (tests, Linux).
type MemClipboard struct {
	mu   sync.Mutex
	text string
	set  bool
}

// Text returns the stored text.
func (c *MemClipboard) Text() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text, c.set
}

// SetText stores text.
func (c *MemClipboard) SetText(s string) {
	c.mu.Lock()
	c.text, c.set = s, true
	c.mu.Unlock()
}

// Event is pushed to the UI bridge: Name is the event name
// ("edgehop.state", "edgehop.log", "edgehop.pairCode", …), Data its payload.
type Event struct {
	Name string
	Data any
}

// LogEntry is the payload of edgehop.log.
type LogEntry struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	TS    int64  `json:"ts"`
}

// Machine is one machine as reported in edgehop.state.
type Machine struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Plat     string    `json:"plat"`
	Version  string    `json:"version,omitempty"`
	Status   string    `json:"status"` // self | online | offline | discovered
	Paired   bool      `json:"paired"`
	Active   bool      `json:"active"`
	Addr     string    `json:"addr,omitempty"`
	Displays []Display `json:"displays"`
	LastSeen int64     `json:"lastSeen,omitempty"`
	Secure   bool      `json:"secureInput,omitempty"`
}

// State is the edgehop.state payload (docs/ARCHITECTURE.md §5).
type State struct {
	Self        Machine        `json:"self"`
	Peers       []Machine      `json:"peers"`
	Layout      Layout         `json:"layout"`
	Active      string         `json:"active"`
	Role        string         `json:"role"` // idle | sending | receiving
	Permissions map[string]any `json:"permissions"`
	Settings    Settings       `json:"settings"`
	Port        int            `json:"port"`
}

// Config wires a Node.
type Config struct {
	ID, Name, Plat, Version string
	Store                   *PeerStore
	Capturer                input.Capturer
	Injector                input.Injector
	Transport               Sender
	Clipboard               Clipboard
	// Now overrides the clock (tests). nil = time.Now.
	Now func() time.Time
	// TickInterval runs Tick on a goroutine after Start; 0 = never (tests call Tick).
	TickInterval time.Duration
	// Permissions is reported verbatim in State (macOS badges; empty on Windows).
	Permissions map[string]any
	// Logf receives every log line in addition to the edgehop.log event.
	Logf func(level, msg string)
	// Locator shows the shake-to-locate overlay; nil = no overlay here.
	Locator Locator
}

type peerState struct {
	id, name, plat, version string
	online, discovered      bool
	addr, pk                string
	displays                []Display
	lastSeen, lastPing      time.Time
	secureInput             bool
}

// Node is the Edgehop state machine: it owns `active`, the switch timer, the
// forwarding of physical input and the injection of remote input, and
// produces the state the UI shows. All public methods are safe for
// concurrent use; platform callbacks (OnLocalInput) return quickly because
// everything that could re-enter the node (injection, sends) runs after the
// lock is released.
type Node struct {
	cfg  Config
	now  func() time.Time
	mu   sync.Mutex
	self Machine

	peers    map[string]*peerState
	displays []Display
	layout   Layout
	settings Settings
	hotkey   []uint8 // modifier families that must be held

	bounds      []Boundary // boundary segments of this machine (§6.1)
	boundsValid bool
	shake       *ShakeDetector
	lastCursor  Point

	active            string
	seq               uint32
	buttons           uint8 // physical buttons held (bit = button number)
	mods              uint8 // physical modifiers held (HID byte)
	edgePending       Edge
	edgeSince         time.Time
	entryEdge         Edge  // edge control arrived through; disarmed until the cursor leaves it (ReentryGuardPx)
	entryPoint        Point // where the cursor was placed on arrival
	edgeTimer         *time.Timer
	lastNamedActive   time.Time
	lastRemoteInput   time.Time
	forwardedToActive bool
	lastHandoverFrom  string
	lastRole          string
	port              int

	actions []func()
	events  chan Event
	stopped chan struct{}
	started bool
}

// New builds a Node; call Start to begin capturing.
func New(cfg Config) *Node {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Store == nil {
		cfg.Store = NewMemoryStore()
	}
	if cfg.Clipboard == nil {
		cfg.Clipboard = &MemClipboard{}
	}
	if cfg.Plat == "" {
		cfg.Plat = Platform()
	}
	n := &Node{
		cfg:     cfg,
		now:     cfg.Now,
		self:    Machine{ID: cfg.ID, Name: cfg.Name, Plat: cfg.Plat, Version: cfg.Version, Status: "self", Paired: true},
		peers:   map[string]*peerState{},
		events:  make(chan Event, 512),
		stopped: make(chan struct{}),
	}
	n.active = cfg.ID
	n.layout = cfg.Store.Layout()
	n.settings = cfg.Store.Settings().Normalize()
	n.hotkey = parseHotkey(n.settings.HotkeySwitch)
	n.shake = NewShakeDetector()
	if cfg.Capturer != nil {
		n.displays = cfg.Capturer.Displays()
	}
	for _, p := range cfg.Store.Peers() {
		n.peers[p.ID] = &peerState{id: p.ID, name: p.Name, plat: p.Plat, addr: p.LastAddr, pk: p.StaticPub, displays: p.Displays}
	}
	n.lastCursor = Centre(n.displays)
	// A config file written by v1 (or by a v1 peer) has tiles only: put the
	// machines on the canvas once, here, and write it back (§6 `layout`).
	if l, changed := n.layout.Upgraded(n.allDisplays()); changed {
		n.layout = l.Normalized()
		_ = cfg.Store.SetLayout(n.layout)
	}
	return n
}

// allDisplays is the canvas input: this machine's live displays plus every
// peer's last known ones (live while online, from the config otherwise).
func (n *Node) allDisplays() map[string][]Display {
	out := map[string][]Display{n.cfg.ID: n.displays}
	for id, p := range n.peers {
		if len(p.displays) > 0 {
			out[id] = p.displays
		}
	}
	return out
}

// boundaries returns the cached boundary segments of this machine,
// recomputing them after a layout or display change.
func (n *Node) boundaries() []Boundary {
	if !n.boundsValid {
		n.bounds = Boundaries(n.layout, n.cfg.ID, n.allDisplays())
		n.boundsValid = true
	}
	return n.bounds
}

// invalidateBoundaries forces the next boundaries() to recompute.
func (n *Node) invalidateBoundaries() { n.boundsValid = false }

// ID returns this machine's id.
func (n *Node) ID() string { return n.cfg.ID }

// Events returns the channel the bridge drains (edgehop.state, edgehop.log,
// …). Events are dropped when nobody drains it.
func (n *Node) Events() <-chan Event { return n.events }

// SetPort records the port the transport actually listens on (for State).
func (n *Node) SetPort(port int) {
	n.mu.Lock()
	n.port = port
	n.mu.Unlock()
}

// Start installs the capture callback, watches display changes and starts
// the liveness ticker.
func (n *Node) Start() error {
	n.mu.Lock()
	if n.started {
		n.mu.Unlock()
		return nil
	}
	n.started = true
	n.mu.Unlock()
	if c := n.cfg.Capturer; c != nil {
		c.OnDisplaysChanged(n.displaysChanged)
		if err := c.Start(n.OnLocalInput); err != nil {
			return err
		}
	}
	if n.cfg.TickInterval > 0 {
		go func() {
			t := time.NewTicker(n.cfg.TickInterval)
			defer t.Stop()
			for {
				select {
				case <-n.stopped:
					return
				case <-t.C:
					n.Tick()
				}
			}
		}()
	}
	n.logf("info", "Edgehop %s ready as %q (%s)", n.cfg.Version, n.cfg.Name, n.cfg.ID[:8])
	n.mu.Lock()
	n.emitState()
	n.mu.Unlock()
	return nil
}

// Stop ends capture and the ticker.
func (n *Node) Stop() {
	n.mu.Lock()
	if !n.started {
		n.mu.Unlock()
		return
	}
	n.started = false
	if n.edgeTimer != nil {
		n.edgeTimer.Stop()
	}
	close(n.stopped)
	n.mu.Unlock()
	if n.cfg.Capturer != nil {
		n.cfg.Capturer.LockCursor(false)
		n.cfg.Capturer.Stop()
	}
}

// ---------------------------------------------------------------- helpers

func (n *Node) emit(ev Event) {
	select {
	case n.events <- ev:
	default:
	}
}

func (n *Node) logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if n.cfg.Logf != nil {
		n.cfg.Logf(level, msg)
	}
	n.emit(Event{"edgehop.log", LogEntry{Level: level, Msg: msg, TS: n.now().UnixMilli()}})
}

// after queues fn to run once the lock is released (injection and network
// sends must not run under n.mu: the fake device calls back synchronously).
func (n *Node) after(fn func()) { n.actions = append(n.actions, fn) }

func (n *Node) runActions() {
	acts := n.actions
	n.actions = nil
	n.mu.Unlock()
	for _, a := range acts {
		a()
	}
}

func (n *Node) send(peerID string, frame []byte) {
	if n.cfg.Transport == nil {
		return
	}
	n.after(func() {
		if err := n.cfg.Transport.Send(peerID, frame); err != nil {
			n.logf("debug", "send to %s: %v", short(peerID), err)
		}
	})
}

func (n *Node) broadcast(frame []byte) {
	if n.cfg.Transport == nil {
		return
	}
	n.after(func() { n.cfg.Transport.Broadcast(frame) })
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (n *Node) peerName(id string) string {
	if id == n.cfg.ID {
		return n.self.Name
	}
	if p, ok := n.peers[id]; ok && p.name != "" {
		return p.name
	}
	return short(id)
}

func (n *Node) ensurePeer(id string) *peerState {
	p, ok := n.peers[id]
	if !ok {
		p = &peerState{id: id}
		n.peers[id] = p
	}
	return p
}

func (n *Node) isSelf(id string) bool { return id == n.cfg.ID }

func (n *Node) peerOnline(id string) bool {
	p, ok := n.peers[id]
	return ok && p.online
}

func (n *Node) role() string {
	if !n.isSelf(n.active) {
		return "sending"
	}
	if !n.lastRemoteInput.IsZero() && n.now().Sub(n.lastRemoteInput) < receivingWindow {
		return "receiving"
	}
	return "idle"
}

// ---------------------------------------------------------------- state

// State returns the edgehop.state payload.
func (n *Node) State() State {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.stateLocked()
}

func (n *Node) stateLocked() State {
	self := n.self
	self.Displays = append([]Display{}, n.displays...)
	self.Active = n.isSelf(n.active)
	self.Status = "self"
	paired := map[string]Peer{}
	for _, p := range n.cfg.Store.Peers() {
		paired[p.ID] = p
	}
	var peers []Machine
	for id, p := range n.peers {
		stored, isPaired := paired[id]
		if !isPaired && !(p.discovered && p.addr != "") {
			continue
		}
		m := Machine{ID: id, Name: p.name, Plat: p.plat, Version: p.version, Paired: isPaired, Active: n.active == id, Addr: p.addr, Displays: append([]Display{}, p.displays...), Secure: p.secureInput}
		if m.Name == "" {
			m.Name = stored.Name
		}
		if m.Plat == "" {
			m.Plat = stored.Plat
		}
		if m.Addr == "" {
			m.Addr = stored.LastAddr
		}
		switch {
		case p.online:
			m.Status = "online"
		case isPaired:
			m.Status = "offline"
		default:
			m.Status = "discovered"
		}
		if !p.lastSeen.IsZero() {
			m.LastSeen = p.lastSeen.UnixMilli()
		}
		peers = append(peers, m)
	}
	for id, p := range paired { // paired but never seen since start
		if _, ok := n.peers[id]; !ok {
			peers = append(peers, Machine{ID: id, Name: p.Name, Plat: p.Plat, Status: "offline", Paired: true, Addr: p.LastAddr, Displays: []Display{}})
		}
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Name != peers[j].Name {
			return peers[i].Name < peers[j].Name
		}
		return peers[i].ID < peers[j].ID
	})
	if peers == nil {
		peers = []Machine{}
	}
	perms := n.cfg.Permissions
	if perms == nil {
		perms = map[string]any{}
	}
	layout := n.layout
	if layout.Tiles == nil {
		layout.Tiles = []Tile{}
	}
	if layout.Machines == nil {
		layout.Machines = []Placement{}
	}
	return State{Self: self, Peers: peers, Layout: layout, Active: n.active, Role: n.role(), Permissions: perms, Settings: n.settings, Port: n.port}
}

func (n *Node) emitState() {
	st := n.stateLocked()
	n.lastRole = st.Role
	n.emit(Event{"edgehop.state", st})
}

// Active returns the id of the active machine.
func (n *Node) Active() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.active
}

// Settings returns the current settings.
func (n *Node) Settings() Settings {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.settings
}

// SetSettings stores and applies new settings. A changed port takes effect
// at the next start.
func (n *Node) SetSettings(s Settings) error {
	s = s.Normalize()
	if err := n.cfg.Store.SetSettings(s); err != nil {
		return err
	}
	n.mu.Lock()
	n.settings = s
	n.hotkey = parseHotkey(s.HotkeySwitch)
	if !n.isSelf(n.active) && n.cfg.Capturer != nil {
		lock := s.LockCursorWhileRemote
		n.after(func() { n.cfg.Capturer.LockCursor(lock) })
	}
	n.emitState()
	n.runActions()
	return nil
}

// SetName renames this machine and tells the peers.
func (n *Node) SetName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("edgehop: empty name")
	}
	if err := n.cfg.Store.SetName(name); err != nil {
		return err
	}
	n.mu.Lock()
	n.self.Name = name
	n.cfg.Name = name
	n.broadcast(MustControl(n.infoMsg(TInfo)))
	n.emitState()
	n.runActions()
	return nil
}

// Layout returns the current layout.
func (n *Node) Layout() Layout {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.layout
}

// SetLayout validates, stamps, stores and broadcasts a layout edited here.
// It accepts either model: a canvas (`machines`) has its tiles derived for
// v1 peers, a legacy tile grid is arranged on the canvas (§6 `layout`).
func (n *Node) SetLayout(l Layout) error {
	n.mu.Lock()
	l, _ = l.Upgraded(n.allDisplays())
	if err := l.ValidatePlacement(n.allDisplays()); err != nil {
		n.mu.Unlock()
		return err
	}
	now := n.now().UnixMilli()
	if l.UpdatedAt <= n.layout.UpdatedAt || l.UpdatedAt == 0 {
		l.UpdatedAt = max(now, n.layout.UpdatedAt+1)
	}
	l = l.Normalized()
	if err := n.cfg.Store.SetLayout(l); err != nil {
		n.mu.Unlock()
		return err
	}
	n.layout = l
	n.invalidateBoundaries()
	n.broadcast(MustControl(NewLayoutMsg(l)))
	n.logf("info", "layout saved (%d machines)", len(l.Machines))
	n.emitState()
	n.runActions()
	return nil
}

// Displays returns this machine's monitors.
func (n *Node) Displays() []Display {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Display(nil), n.displays...)
}

func (n *Node) infoMsg(t string) InfoMsg {
	d := n.displays
	if d == nil {
		d = []Display{}
	}
	return InfoMsg{T: t, Name: n.self.Name, Plat: n.self.Plat, Version: n.self.Version, Port: n.port, Displays: d}
}

func (n *Node) displaysChanged() {
	if n.cfg.Capturer == nil {
		return
	}
	d := n.cfg.Capturer.Displays()
	n.mu.Lock()
	n.displays = d
	n.invalidateBoundaries()
	n.broadcast(MustControl(InfoMsg{T: TDisplays, Displays: d}))
	n.logf("info", "displays changed: %d", len(d))
	n.emitState()
	n.runActions()
}

// ---------------------------------------------------------------- transport callbacks

// PeerUp is called by the transport once a connection to peer is
// authenticated: the node sends `info` and the layout.
func (n *Node) PeerUp(info securechan.PeerInfo, addr string) {
	n.mu.Lock()
	p := n.ensurePeer(info.ID)
	p.online = true
	p.name, p.plat = info.Name, info.Plat
	p.pk = PubKeyB64(info.StaticPub)
	if addr != "" {
		p.addr = addr
	}
	p.lastSeen, p.lastPing = n.now(), n.now()
	_ = n.cfg.Store.SetLastAddr(info.ID, addr)
	_ = n.cfg.Store.UpdatePeerInfo(info.ID, info.Name, info.Plat)
	n.send(info.ID, MustControl(n.infoMsg(TInfo)))
	if len(n.layout.Machines) > 0 || len(n.layout.Tiles) > 0 {
		n.send(info.ID, MustControl(NewLayoutMsg(n.layout)))
	}
	n.logf("info", "connected to %s (%s)", info.Name, info.Plat)
	n.emitState()
	n.runActions()
}

// PeerDown is called by the transport when the connection to id is gone.
func (n *Node) PeerDown(id string) {
	n.mu.Lock()
	p, ok := n.peers[id]
	if !ok || !p.online {
		n.mu.Unlock()
		return
	}
	p.online = false
	n.logf("info", "disconnected from %s", n.peerName(id))
	n.peerGone(id)
	n.emitState()
	n.runActions()
}

// peerGone handles the active machine vanishing (§6 ping/pong).
func (n *Node) peerGone(id string) {
	if n.active != id {
		return
	}
	if n.forwardedToActive || n.isSelf(n.lastHandoverFrom) {
		n.logf("warn", "%s vanished while active; taking control back", n.peerName(id))
		n.claimSelf()
		return
	}
	// Nobody here forwarded to it: revert to the start state silently.
	n.active = n.cfg.ID
	n.becomeActiveLocal(nil, 0, 0)
}

// Discovered records a machine seen on the network (Bonjour or a manual
// address). An empty addr means it disappeared.
type Discovered struct {
	ID, Name, Plat, Addr, PK string
}

// Discovered is called by the transport for browse events.
func (n *Node) Discovered(d Discovered) {
	if d.ID == "" || n.isSelf(d.ID) {
		return
	}
	n.mu.Lock()
	p := n.ensurePeer(d.ID)
	if d.Addr == "" {
		p.discovered = false
	} else {
		p.discovered = true
		p.addr = d.Addr
		if d.Name != "" {
			p.name = d.Name
		}
		if d.Plat != "" {
			p.plat = d.Plat
		}
		if d.PK != "" {
			p.pk = d.PK
		}
		_ = n.cfg.Store.SetLastAddr(d.ID, d.Addr)
	}
	n.emitState()
	n.runActions()
}

// PeerAddr returns the best known address of id ("" when unknown).
func (n *Node) PeerAddr(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p, ok := n.peers[id]; ok && p.addr != "" {
		return p.addr
	}
	if p, ok := n.cfg.Store.Peer(id); ok {
		return p.LastAddr
	}
	return ""
}

// Forget drops a peer from the store, the layout and the state.
func (n *Node) Forget(id string) error {
	if err := n.cfg.Store.Forget(id); err != nil {
		return err
	}
	n.mu.Lock()
	n.layout = n.cfg.Store.Layout()
	n.invalidateBoundaries()
	if p, ok := n.peers[id]; ok {
		p.online = false
		if !p.discovered {
			delete(n.peers, id)
		}
	}
	if n.active == id {
		n.active = n.cfg.ID
		n.becomeActiveLocal(nil, 0, 0)
	}
	if n.cfg.Transport != nil {
		n.after(func() { n.cfg.Transport.Disconnect(id) })
	}
	n.logf("info", "unpaired %s", n.peerName(id))
	n.emitState()
	n.runActions()
	return nil
}

// OnPeerMessage routes a decrypted frame from peerID.
func (n *Node) OnPeerMessage(peerID string, payload []byte) {
	msg, err := Decode(payload)
	n.mu.Lock()
	p, known := n.peers[peerID]
	if !known {
		n.mu.Unlock()
		return
	}
	p.lastSeen = n.now()
	if err != nil {
		n.logf("warn", "bad frame from %s: %v", n.peerName(peerID), err)
		n.send(peerID, MustControl(ErrorMsg{T: TError, Code: "protocol", Message: err.Error()}))
		if n.cfg.Transport != nil {
			n.after(func() { n.cfg.Transport.Disconnect(peerID) })
		}
		n.runActions()
		return
	}
	if msg.Type == TypeControl {
		n.handleControl(p, msg)
	} else {
		n.handleInput(p, msg)
	}
	n.runActions()
}

func (n *Node) handleControl(p *peerState, msg Message) {
	switch msg.T {
	case TInfo, TDisplays:
		var m InfoMsg
		if err := msg.ParseControl(&m); err != nil {
			n.logf("warn", "%s: %v", msg.T, err)
			return
		}
		p.displays = m.Displays
		_ = n.cfg.Store.SetPeerDisplays(p.id, m.Displays)
		n.invalidateBoundaries()
		if msg.T == TInfo {
			if m.Name != "" {
				p.name = m.Name
			}
			if m.Plat != "" {
				p.plat = m.Plat
			}
			p.version = m.Version
			_ = n.cfg.Store.UpdatePeerInfo(p.id, p.name, p.plat)
			n.learnPort(p, m.Port)
		}
		n.emitState()
	case TLayout:
		var m LayoutMsg
		if err := msg.ParseControl(&m); err != nil {
			n.logf("warn", "layout: %v", err)
			return
		}
		l := m.Layout()
		if err := l.Validate(); err != nil {
			n.logf("warn", "layout from %s: %v", n.peerName(p.id), err)
			return
		}
		if l.UpdatedAt > n.layout.UpdatedAt {
			// A v1 peer sends tiles only: arrange them on the canvas and keep
			// it as v2 from here on (§6 `layout`).
			l, _ = l.Upgraded(n.allDisplays())
			n.layout = l.Normalized()
			n.invalidateBoundaries()
			_ = n.cfg.Store.SetLayout(n.layout)
			n.logf("info", "layout updated by %s", n.peerName(p.id))
			n.emitState()
		}
	case TActive:
		var m ActiveMsg
		if err := msg.ParseControl(&m); err != nil {
			n.logf("warn", "active: %v", err)
			return
		}
		if m.Seq < n.seq {
			n.logf("debug", "ignoring stale active seq %d < %d", m.Seq, n.seq)
			return
		}
		n.seq = m.Seq
		n.lastHandoverFrom = m.From
		if n.isSelf(m.ID) {
			n.logf("info", "control arrived from %s", n.peerName(m.From))
			n.active = n.cfg.ID
			n.becomeActiveLocal(m.Enter, m.Buttons, m.Modifiers)
		} else {
			n.setRemoteActive(m.ID)
		}
		n.emitState()
	case TClaim:
		var m ClaimMsg
		if err := msg.ParseControl(&m); err != nil {
			n.logf("warn", "claim: %v", err)
			return
		}
		if m.Seq < n.seq {
			return
		}
		n.seq = m.Seq
		id := m.ID
		if id == "" {
			id = p.id
		}
		n.lastHandoverFrom = p.id
		if n.isSelf(id) {
			n.active = n.cfg.ID
			n.becomeActiveLocal(nil, 0, 0)
		} else {
			n.logf("info", "%s took control", n.peerName(id))
			n.setRemoteActive(id)
		}
		n.emitState()
	case TClipboard:
		var m ClipboardMsg
		if err := msg.ParseControl(&m); err != nil {
			return
		}
		if m.Kind == "text" && n.settings.ClipboardSync && n.cfg.Clipboard != nil {
			text := m.Text
			n.after(func() { n.cfg.Clipboard.SetText(text) })
		}
	case TPing:
		var m PingMsg
		if err := msg.ParseControl(&m); err != nil {
			return
		}
		n.send(p.id, MustControl(PingMsg{T: TPong, TS: m.TS}))
	case TPong, TAuth, TAuthOK:
	case TLocate:
		n.showLocate("asked by " + n.peerName(p.id))
	case TSecureInput:
		var m SecureInputMsg
		if err := msg.ParseControl(&m); err != nil {
			return
		}
		p.secureInput = m.On
		n.emitState()
	case TError:
		var m ErrorMsg
		_ = msg.ParseControl(&m)
		n.logf("warn", "%s reports error %s: %s", n.peerName(p.id), m.Code, m.Message)
	default:
		n.logf("debug", "unknown control %q from %s", msg.T, n.peerName(p.id))
	}
}

// learnPort replaces the port of the peer's known address with the port it
// says it listens on (the remote port of an inbound connection is ephemeral).
func (n *Node) learnPort(p *peerState, port int) {
	if port <= 0 || port > 65535 || p.addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(p.addr)
	if err != nil || host == "" {
		return
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if addr == p.addr {
		return
	}
	p.addr = addr
	_ = n.cfg.Store.SetLastAddr(p.id, addr)
}

// setRemoteActive records that id (a peer) is now active.
func (n *Node) setRemoteActive(id string) {
	wasSelf := n.isSelf(n.active)
	n.active = id
	n.forwardedToActive = false
	n.edgePending = ""
	if wasSelf {
		n.releaseModifiers()
		if n.settings.LockCursorWhileRemote && n.cfg.Capturer != nil {
			n.after(func() { n.cfg.Capturer.LockCursor(true) })
		}
	}
}

// becomeActiveLocal performs §6.1 rule 4 after n.active was set to self.
func (n *Node) becomeActiveLocal(enter *Enter, buttons, mods uint8) {
	n.lastNamedActive = n.now()
	n.forwardedToActive = false
	n.edgePending = ""
	var p Point
	switch {
	case enter != nil && enter.Pt != nil:
		// Layout v2: the sender computed the exact point on the canvas.
		p = SnapToDisplays(*enter.Pt, n.displays)
	case enter != nil && enter.Edge.Valid():
		p = EntryPoint(enter.Edge, enter.Frac, n.displays)
	default:
		p = Centre(n.displays)
	}
	n.lastCursor = p
	n.entryEdge, n.entryPoint = "", p
	if enter != nil && enter.Edge.Valid() {
		n.entryEdge = enter.Edge
	}
	cap, inj := n.cfg.Capturer, n.cfg.Injector
	n.after(func() {
		if cap != nil {
			cap.LockCursor(false)
		}
		if inj == nil {
			return
		}
		inj.MoveTo(p.X, p.Y)
		for bit := 0; bit < 8; bit++ {
			if mods&(1<<bit) != 0 {
				inj.Key(keymap.ModLeftControl+bit, true, mods)
			}
		}
		for b := 0; b < 5; b++ {
			if buttons&(1<<b) != 0 {
				inj.Button(input.Button(b), true, 1)
			}
		}
	})
}

// releaseModifiers injects key-ups for the physically held modifiers so the
// local OS does not keep them pressed while the key-ups go to the peer.
func (n *Node) releaseModifiers() {
	mods := n.mods
	inj := n.cfg.Injector
	if mods == 0 || inj == nil {
		return
	}
	n.after(func() {
		for bit := 0; bit < 8; bit++ {
			if mods&(1<<bit) != 0 {
				inj.Key(keymap.ModLeftControl+bit, false, 0)
			}
		}
	})
}

// locateRequested acts on a shake detected from the physical mouse here: on
// the active machine show the overlay locally (macOS has its own
// shake-to-locate, so nothing to do there), otherwise ask the active machine
// to show it (§6.3).
func (n *Node) locateRequested() {
	if n.isSelf(n.active) {
		if n.self.Plat == "macos" {
			return
		}
		n.showLocate("shake")
		return
	}
	n.logf("debug", "locate → %s (shake)", n.peerName(n.active))
	n.send(n.active, MustControl(LocateMsg{T: TLocate}))
}

// showLocate runs the local overlay, if this platform has one.
func (n *Node) showLocate(why string) {
	loc := n.cfg.Locator
	if loc == nil {
		n.logf("debug", "locate (%s): no overlay on this platform", why)
		return
	}
	n.logf("debug", "locate (%s)", why)
	n.after(loc.ShowLocate)
}

func (n *Node) handleInput(p *peerState, msg Message) {
	now := n.now()
	if !n.isSelf(n.active) && now.Sub(n.lastNamedActive) > ActiveGrace {
		return
	}
	n.lastRemoteInput = now
	inj := n.cfg.Injector
	if inj == nil {
		return
	}
	swap := n.settings.SwapCmdCtrl && p.plat != "" && p.plat != n.self.Plat
	switch msg.Type {
	case TypeMouseMove:
		dx, dy := int(msg.MouseMove.DX), int(msg.MouseMove.DY)
		n.after(func() { inj.MoveRelative(dx, dy) })
	case TypeMouseButton:
		m := msg.MouseButton
		n.after(func() { inj.Button(input.Button(m.Button), m.Down, int(m.ClickCount)) })
	case TypeScroll:
		m := msg.Scroll
		n.after(func() { inj.Scroll(int(m.DX), int(m.DY), m.Discrete()) })
	case TypeKey:
		m := msg.Key
		wire := int(m.Usage)
		mods := m.Modifiers
		if swap {
			wire = SwapGUICtrl(wire)
			mods = SwapGUICtrlMods(mods)
		}
		down := m.State != KeyUp
		n.after(func() { inj.Key(wire, down, mods) })
	case TypeKeyText:
		text := msg.KeyText
		n.after(func() { inj.Text(text) })
	}
	if role := n.role(); role != n.lastRole {
		n.emitState()
	}
}

// SwapGUICtrl exchanges the GUI and Control usages (docs/edgehop-protocol.md
// §5 optional modifier swap).
func SwapGUICtrl(wire int) int {
	switch wire {
	case keymap.ModLeftControl:
		return keymap.ModLeftGUI
	case keymap.ModLeftGUI:
		return keymap.ModLeftControl
	case keymap.ModRightControl:
		return keymap.ModRightGUI
	case keymap.ModRightGUI:
		return keymap.ModRightControl
	}
	return wire
}

// SwapGUICtrlMods exchanges the Ctrl and GUI bits of a modifier byte.
func SwapGUICtrlMods(m uint8) uint8 {
	const ctrl, gui = 0x11, 0x88 // bits 0|4 and 3|7
	c := (m & 0x01 << 3) | (m & 0x10 << 3)
	g := (m & 0x08 >> 3) | (m & 0x80 >> 3)
	return (m &^ (ctrl | gui)) | c | g
}

// ---------------------------------------------------------------- local input

// OnLocalInput is the capture callback. Physical events are either consumed
// locally (edge detection, hotkeys) or forwarded to the active machine, in
// which case true is returned so the OS never sees them. Injected events are
// never forwarded, but on the active machine their position still runs
// through edge detection so a remote mouse can push on to a third machine.
func (n *Node) OnLocalInput(ev input.Event) bool {
	n.mu.Lock()
	swallow := n.localInput(ev)
	n.runActions()
	return swallow
}

func buttonBit(b input.Button) uint8 {
	if b > 7 {
		return 0
	}
	return 1 << b
}

func (n *Node) localInput(ev input.Event) bool {
	if ev.Injected {
		if ev.Kind == input.MouseMove {
			n.lastCursor = Point{ev.X, ev.Y}
			if n.isSelf(n.active) {
				n.checkEdge(Point{ev.X, ev.Y})
			}
		}
		return false
	}
	if ev.Kind == input.MouseMove {
		// Shake to locate runs on whichever machine owns the physical mouse,
		// forwarding or not (§6.3).
		if n.shake.Feed(ev.DX, ev.DY, n.now()) && n.settings.ShakeToLocate {
			n.locateRequested()
		}
	}
	switch ev.Kind {
	case input.MouseButton:
		if ev.Down {
			n.buttons |= buttonBit(ev.Button)
		} else {
			n.buttons &^= buttonBit(ev.Button)
		}
	case input.Key:
		if bit, ok := keymap.ModifierBit(ev.Wire); ok {
			if ev.Down {
				n.mods |= bit
			} else {
				n.mods &^= bit
			}
		} else if ev.Mods != 0 || n.mods != 0 {
			// Trust the capturer's view of the modifier state when it has one.
			if ev.Mods != 0 {
				n.mods = ev.Mods
			}
		}
		if ev.Down && !ev.Repeat && n.hotkeyPressed(ev) {
			return true
		}
	}
	if n.isSelf(n.active) {
		if ev.Kind == input.MouseMove || ev.Kind == input.MouseButton {
			n.checkEdge(Point{ev.X, ev.Y})
		}
		return false
	}
	n.forward(ev)
	return true
}

func (n *Node) forward(ev input.Event) {
	var frame []byte
	switch ev.Kind {
	case input.MouseMove:
		if ev.DX == 0 && ev.DY == 0 {
			return
		}
		frame = EncodeMouseMove(ev.DX, ev.DY)
	case input.MouseButton:
		frame = EncodeMouseButton(uint8(ev.Button), ev.Down, ev.ClickCount)
	case input.Scroll:
		frame = EncodeScroll(ev.DX, ev.DY, ev.Flags)
	case input.Key:
		state := KeyUp
		if ev.Down {
			state = KeyDown
			if ev.Repeat {
				state = KeyRepeat
			}
		}
		mods := ev.Mods
		if mods == 0 {
			mods = n.mods
		}
		frame = EncodeKey(ev.Wire, state, mods)
	case input.KeyText:
		frame = EncodeKeyText(ev.Text)
	default:
		return
	}
	n.forwardedToActive = true
	n.send(n.active, frame)
}

// checkEdge implements §6.1 rules 2–3 for the active machine: the pointer
// must sit inside a boundary segment of the canvas, outside the corner dead
// zones, with no button held, for switchDelayMs.
func (n *Node) checkEdge(p Point) {
	n.lastCursor = p
	me, placed := n.layout.Placement(n.cfg.ID)
	if !placed {
		n.edgePending = ""
		return
	}
	hop, ok := HopAt(p, n.displays, me, n.boundaries(), n.settings.EdgeCornerDeadZonePx)
	if n.entryEdge != "" {
		if awayFromEdge(n.entryEdge, n.entryPoint, p) >= ReentryGuardPx {
			n.entryEdge = ""
		} else if ok && hop.Edge == n.entryEdge {
			ok = false // just arrived through this edge: a small wobble back must not bounce control
		}
	}
	if !ok || !n.peerOnline(hop.Target) || n.buttons != 0 {
		n.edgePending = ""
		return
	}
	now := n.now()
	if n.edgePending != hop.Edge {
		n.edgePending = hop.Edge
		n.edgeSince = now
	}
	delay := time.Duration(n.settings.SwitchDelayMs) * time.Millisecond
	if elapsed := now.Sub(n.edgeSince); elapsed < delay {
		remaining := delay - elapsed
		if n.edgeTimer != nil {
			n.edgeTimer.Stop()
		}
		n.edgeTimer = time.AfterFunc(remaining, n.edgeTimerFired)
		return
	}
	pt := hop.Pt
	n.handOver(hop.Target, &Enter{Edge: hop.Enter, Frac: hop.Frac, Pt: &pt})
}

// ReentryGuardPx is how far the cursor must move away from the edge it arrived
// through before that edge can hand control back (protocol §6.3).
const ReentryGuardPx = 40

// awayFromEdge is how far p has moved from the arrival point a, inward from edge e.
func awayFromEdge(e Edge, a, p Point) int {
	switch e {
	case EdgeLeft:
		return p.X - a.X
	case EdgeRight:
		return a.X - p.X
	case EdgeTop:
		return p.Y - a.Y
	case EdgeBottom:
		return a.Y - p.Y
	}
	return ReentryGuardPx
}

// edgeTimerFired re-checks the edge once the switch delay elapsed.
func (n *Node) edgeTimerFired() {
	if n.cfg.Injector == nil {
		return
	}
	x, y := n.cfg.Injector.CursorPos()
	n.mu.Lock()
	if n.edgePending != "" && n.isSelf(n.active) {
		n.checkEdge(Point{x, y})
	}
	n.runActions()
}

// handOver broadcasts `active` naming target and makes this machine inactive.
func (n *Node) handOver(target string, enter *Enter) {
	n.seq++
	msg := ActiveMsg{T: TActive, ID: target, From: n.active, Enter: enter, Buttons: n.buttons, Modifiers: n.mods, Seq: n.seq}
	n.broadcast(MustControl(msg))
	n.lastHandoverFrom = n.active
	if n.settings.ClipboardSync && n.cfg.Clipboard != nil {
		if text, ok := n.cfg.Clipboard.Text(); ok && text != "" {
			n.send(target, MustControl(ClipboardMsg{T: TClipboard, Kind: "text", Text: text}))
		}
	}
	where := "centre"
	if enter != nil {
		where = fmt.Sprintf("%s edge at %.2f", enter.Edge, enter.Frac)
		if enter.Pt != nil {
			where = fmt.Sprintf("%s edge at (%d,%d)", enter.Edge, enter.Pt.X, enter.Pt.Y)
		}
	}
	n.logf("info", "control → %s (%s)", n.peerName(target), where)
	n.setRemoteActive(target)
	n.emitState()
}

// claimSelf broadcasts `claim` and makes this machine active.
func (n *Node) claimSelf() {
	n.seq++
	n.broadcast(MustControl(ClaimMsg{T: TClaim, ID: n.cfg.ID, Seq: n.seq}))
	n.lastHandoverFrom = n.cfg.ID
	n.active = n.cfg.ID
	n.becomeActiveLocal(nil, 0, 0)
	n.logf("info", "control → %s (claimed)", n.self.Name)
	n.emitState()
}

// Claim moves control to targetID: `claim` when it is this machine, otherwise
// an `active` without `enter` (the new machine shows its cursor centred).
func (n *Node) Claim(targetID string) error {
	n.mu.Lock()
	defer n.runActions()
	if n.isSelf(targetID) {
		if !n.isSelf(n.active) {
			n.claimSelf()
		}
		return nil
	}
	if !n.peerOnline(targetID) {
		return fmt.Errorf("edgehop: %s is not connected", n.peerName(targetID))
	}
	if n.active == targetID {
		return nil
	}
	n.handOver(targetID, nil)
	return nil
}

// ---------------------------------------------------------------- hotkeys

// Modifier families (either side) for the hotkey combination.
const (
	famCtrl  uint8 = 0x11
	famShift uint8 = 0x22
	famAlt   uint8 = 0x44
	famGUI   uint8 = 0x88
)

// parseHotkey turns "ctrl+alt" into the modifier families that must be held.
func parseHotkey(s string) []uint8 {
	var fams []uint8
	for _, part := range strings.Split(strings.ToLower(s), "+") {
		switch strings.TrimSpace(part) {
		case "ctrl", "control", "⌃":
			fams = append(fams, famCtrl)
		case "alt", "option", "opt", "⌥":
			fams = append(fams, famAlt)
		case "shift", "⇧":
			fams = append(fams, famShift)
		case "cmd", "command", "gui", "win", "meta", "super", "⌘":
			fams = append(fams, famGUI)
		}
	}
	return fams
}

// HotkeyMatches reports whether mods holds exactly the families of hotkey.
func HotkeyMatches(hotkey string, mods uint8) bool {
	return hotkeyMatch(parseHotkey(hotkey), mods)
}

func hotkeyMatch(fams []uint8, mods uint8) bool {
	if len(fams) == 0 {
		return false
	}
	var all uint8
	for _, f := range fams {
		if mods&f == 0 {
			return false
		}
		all |= f
	}
	return mods&^all == 0
}

// HID usages of the hotkey keys.
const (
	usageRight = 0x4F
	usageLeft  = 0x50
	usageDown  = 0x51
	usageUp    = 0x52
	usageHome  = 0x4A
)

// hotkeyPressed handles §6.2; it returns true when ev was a hotkey.
func (n *Node) hotkeyPressed(ev input.Event) bool {
	mods := ev.Mods
	if mods == 0 {
		mods = n.mods
	}
	if !hotkeyMatch(n.hotkey, mods) {
		return false
	}
	var dir Edge
	switch ev.Wire {
	case usageLeft:
		dir = EdgeLeft
	case usageRight:
		dir = EdgeRight
	case usageUp:
		dir = EdgeTop
	case usageDown:
		dir = EdgeBottom
	case usageHome:
		if !n.isSelf(n.active) {
			n.claimSelf()
		}
		return true
	default:
		return false
	}
	var cursor *Point
	if n.isSelf(n.active) {
		c := n.lastCursor
		cursor = &c
	}
	target, ok := HotkeyTarget(dir, n.active, n.layout, n.allDisplays(), cursor)
	if !ok {
		n.logf("debug", "hotkey %s: no machine that way", dir)
		return true
	}
	if n.isSelf(target) {
		if !n.isSelf(n.active) {
			n.claimSelf()
		}
		return true
	}
	if !n.peerOnline(target) {
		n.logf("debug", "hotkey %s: %s is offline", dir, n.peerName(target))
		return true
	}
	n.handOver(target, nil)
	return true
}

// ---------------------------------------------------------------- liveness

// Tick sends pings, drops silent peers and re-checks a pending edge. Start
// runs it every Config.TickInterval; tests call it directly.
func (n *Node) Tick() {
	var cx, cy int
	if n.cfg.Injector != nil {
		cx, cy = n.cfg.Injector.CursorPos()
	}
	n.mu.Lock()
	now := n.now()
	changed := false
	for id, p := range n.peers {
		if !p.online {
			continue
		}
		if now.Sub(p.lastSeen) > PeerTimeout {
			p.online = false
			n.logf("warn", "%s stopped answering", n.peerName(id))
			if n.cfg.Transport != nil {
				id := id
				n.after(func() { n.cfg.Transport.Disconnect(id) })
			}
			n.peerGone(id)
			changed = true
			continue
		}
		if now.Sub(p.lastPing) >= PingInterval {
			p.lastPing = now
			n.send(id, MustControl(PingMsg{T: TPing, TS: now.UnixMilli()}))
		}
	}
	if n.edgePending != "" && n.isSelf(n.active) {
		n.checkEdge(Point{cx, cy})
	}
	if changed || n.role() != n.lastRole {
		n.emitState()
	}
	n.runActions()
}
