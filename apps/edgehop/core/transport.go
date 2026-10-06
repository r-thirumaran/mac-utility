package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/mdnsdisc"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

// Network constants (docs/edgehop-protocol.md §2, §4).
const (
	ServiceType      = "_edgehop._tcp"
	DefaultPort      = 24800
	handshakeTimeout = 15 * time.Second
	pairingTimeout   = securechan.CodeTTL + 30*time.Second
	dialTimeout      = 10 * time.Second
	minBackoff       = 1 * time.Second
	maxBackoff       = 30 * time.Second
	outQueue         = 4096
	pairFailLimit    = 5
	pairFailWindow   = 10 * time.Minute
)

// Handler receives transport events; *Node implements it.
type Handler interface {
	PeerUp(info securechan.PeerInfo, addr string)
	PeerDown(id string)
	OnPeerMessage(id string, payload []byte)
	Discovered(d Discovered)
}

// TransportConfig wires a Transport.
type TransportConfig struct {
	Self    securechan.Identity
	Name    string
	Plat    string
	Store   *PeerStore
	Handler Handler
	// Port is the preferred listening port; 0 or a busy port falls back to
	// any free port. The bound port is what Bonjour advertises.
	Port int
	// DisableMDNS skips Bonjour (tests use manual addresses).
	DisableMDNS bool
	// Emit receives UI events (edgehop.pairCode, edgehop.pairing).
	Emit func(Event)
	Logf func(level, msg string)
	// AddrFor resolves the best address of a peer ("" = unknown); nil uses
	// the store's lastAddr.
	AddrFor func(id string) string
}

// PairingState is the payload of the edgehop.pairing event.
type PairingState struct {
	Stage   string `json:"stage"` // connecting | waiting_code | paired | failed | cancelled
	PeerID  string `json:"peerId,omitempty"`
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PairCode is the payload of the edgehop.pairCode event (acceptor side).
type PairCode struct {
	Code string  `json:"code"`
	Peer Machine `json:"peer"`
	TTL  int     `json:"ttlSec"`
}

// PairResult is the payload of the edgehop.pairResult event (acceptor side).
type PairResult struct {
	PeerID string `json:"peerId"`
	Name   string `json:"name,omitempty"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type peerConn struct {
	id     string
	addr   string
	ch     *securechan.Channel
	conn   net.Conn
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	drops  atomic.Uint64
	closed atomic.Bool
}

func (c *peerConn) close() {
	c.once.Do(func() {
		c.closed.Store(true)
		_ = c.ch.Close()
		close(c.done)
	})
}

type pairing struct {
	peerID, addr string
	code         chan string
	cancel       chan struct{}
}

type dialer struct {
	id   string
	stop chan struct{}
	kick chan struct{}
}

// Transport owns the listener, Bonjour, the per-peer connections with their
// reconnect loops, and the pairing flow.
type Transport struct {
	cfg    TransportConfig
	ctx    context.Context
	cancel context.CancelFunc
	ln     net.Listener
	port   int
	wg     sync.WaitGroup

	mu       sync.Mutex
	conns    map[string]*peerConn
	dialers  map[string]*dialer
	pairing  *pairing
	failures map[string][]time.Time
	stopMDNS func()
}

// NewTransport builds a Transport; call Start.
func NewTransport(cfg TransportConfig) *Transport {
	if cfg.Plat == "" {
		cfg.Plat = Platform()
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, string) {}
	}
	if cfg.Emit == nil {
		cfg.Emit = func(Event) {}
	}
	return &Transport{cfg: cfg, conns: map[string]*peerConn{}, dialers: map[string]*dialer{}, failures: map[string][]time.Time{}}
}

func (t *Transport) logf(level, format string, a ...any) {
	t.cfg.Logf(level, fmt.Sprintf(format, a...))
}

// Start listens, advertises, browses and starts the reconnect loops of the
// paired peers this machine must connect to (smaller id).
func (t *Transport) Start(ctx context.Context) error {
	t.ctx, t.cancel = context.WithCancel(ctx)
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(t.cfg.Port))
	if err != nil {
		t.logf("warn", "port %d busy, using any free port: %v", t.cfg.Port, err)
		ln, err = net.Listen("tcp", ":0")
		if err != nil {
			return fmt.Errorf("edgehop: listen: %w", err)
		}
	}
	t.ln = ln
	t.port = ln.Addr().(*net.TCPAddr).Port
	t.logf("info", "listening on tcp port %d", t.port)
	t.wg.Add(1)
	go t.acceptLoop()
	if !t.cfg.DisableMDNS {
		t.startMDNS()
	}
	for _, p := range t.cfg.Store.Peers() {
		t.ensureDialer(p.ID)
	}
	go func() {
		<-t.ctx.Done()
		_ = ln.Close()
	}()
	return nil
}

// Stop closes everything and waits for the goroutines.
func (t *Transport) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Lock()
	for _, c := range t.conns {
		c.close()
	}
	for _, d := range t.dialers {
		close(d.stop)
	}
	t.dialers = map[string]*dialer{}
	if t.stopMDNS != nil {
		t.stopMDNS()
	}
	t.mu.Unlock()
	t.wg.Wait()
}

// Port returns the bound TCP port.
func (t *Transport) Port() int { return t.port }

// ID returns this machine's id.
func (t *Transport) ID() string { return t.cfg.Self.ID() }

// ---------------------------------------------------------------- Bonjour

func (t *Transport) txt() map[string]string {
	return map[string]string{"v": "1", "id": t.ID(), "name": t.cfg.Name, "plat": t.cfg.Plat, "pk": PubKeyB64(t.cfg.Self.Pub)}
}

func (t *Transport) startMDNS() {
	stop, err := mdnsdisc.Advertise(t.ctx, ServiceType, t.cfg.Name, t.port, t.txt())
	if err != nil {
		t.logf("warn", "bonjour advertise: %v", err)
	} else {
		t.mu.Lock()
		t.stopMDNS = stop
		t.mu.Unlock()
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		err := mdnsdisc.Browse(t.ctx, ServiceType, t.onBrowse)
		if err != nil && t.ctx.Err() == nil {
			t.logf("warn", "bonjour browse: %v", err)
		}
	}()
}

func (t *Transport) onBrowse(e mdnsdisc.Event) {
	id := e.TXT["id"]
	if id == "" || id == t.ID() {
		return
	}
	d := Discovered{ID: id, Name: e.TXT["name"], Plat: e.TXT["plat"], PK: e.TXT["pk"]}
	if d.Name == "" {
		d.Name = e.Instance
	}
	if e.Added {
		var ip net.IP
		for _, a := range e.Addrs {
			if a4 := a.To4(); a4 != nil {
				ip = a4
				break
			}
		}
		if ip == nil && len(e.Addrs) > 0 {
			ip = e.Addrs[0]
		}
		if ip == nil {
			return
		}
		d.Addr = net.JoinHostPort(ip.String(), strconv.Itoa(e.Port))
		t.logf("debug", "discovered %s at %s", d.Name, d.Addr)
	}
	t.cfg.Handler.Discovered(d)
	if e.Added {
		t.ensureDialer(id)
		t.Poke(id)
	}
}

// ---------------------------------------------------------------- connections

func (t *Transport) acceptLoop() {
	defer t.wg.Done()
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			t.logf("warn", "accept: %v", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		t.wg.Add(1)
		go t.handleInbound(conn)
	}
}

func (t *Transport) options() securechan.Options {
	return securechan.Options{Self: t.cfg.Self, Name: t.cfg.Name, Plat: t.cfg.Plat, Trust: t.cfg.Store}
}

func (t *Transport) handleInbound(conn net.Conn) {
	defer t.wg.Done()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
	}
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	var (
		pairPeer securechan.PeerInfo
		paired   bool
	)
	opts := t.options()
	opts.ShowCode = func(code string, peer securechan.PeerInfo) {
		paired = true
		pairPeer = peer
		if t.pairingRateLimited(peer.ID) {
			t.logf("warn", "pairing with %s refused: too many failed attempts", peer.Name)
			_ = conn.Close()
			return
		}
		_ = conn.SetDeadline(time.Now().Add(pairingTimeout))
		t.cfg.Emit(Event{"edgehop.pairCode", PairCode{Code: code, Peer: Machine{ID: peer.ID, Name: peer.Name, Plat: peer.Plat}, TTL: int(securechan.CodeTTL / time.Second)}})
	}
	ch, info, err := securechan.Accept(conn, opts)
	if err != nil {
		_ = conn.Close()
		if paired {
			t.recordPairFailure(pairPeer.ID)
			t.cfg.Emit(Event{"edgehop.pairResult", PairResult{PeerID: pairPeer.ID, Name: pairPeer.Name, OK: false, Error: pairError(err)}})
			t.logf("warn", "pairing with %s failed: %v", pairPeer.Name, err)
		} else if !errors.Is(err, securechan.ErrUnpaired) {
			t.logf("debug", "inbound handshake from %s: %v", conn.RemoteAddr(), err)
		} else {
			t.logf("info", "refused unpaired connection from %s", conn.RemoteAddr())
		}
		return
	}
	if paired {
		t.cfg.Emit(Event{"edgehop.pairResult", PairResult{PeerID: info.ID, Name: info.Name, OK: true}})
		t.logf("info", "paired with %s", info.Name)
		t.ensureDialer(info.ID)
	}
	_ = conn.SetDeadline(time.Time{})
	t.run(ch, conn, info, t.inboundAddr(info.ID, conn))
}

// inboundAddr guesses where an inbound peer listens: the address Bonjour
// (or the user) gave for it, else its host with the default port — the
// remote port of an inbound connection is ephemeral and useless for dialing.
func (t *Transport) inboundAddr(id string, conn net.Conn) string {
	if t.cfg.AddrFor != nil {
		if a := t.cfg.AddrFor(id); a != "" {
			return a
		}
	}
	if p, ok := t.cfg.Store.Peer(id); ok && p.LastAddr != "" {
		return p.LastAddr
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(DefaultPort))
}

// run registers the channel (closing any older connection to the same
// peer), tells the handler, pumps frames until the connection ends and
// reports PeerDown. It blocks.
func (t *Transport) run(ch *securechan.Channel, conn net.Conn, info securechan.PeerInfo, addr string) {
	pc := &peerConn{id: info.ID, addr: addr, ch: ch, conn: conn, out: make(chan []byte, outQueue), done: make(chan struct{})}
	t.mu.Lock()
	if old, ok := t.conns[info.ID]; ok {
		t.logf("debug", "replacing older connection to %s", info.Name)
		old.close()
	}
	t.conns[info.ID] = pc
	t.mu.Unlock()
	t.cfg.Handler.PeerUp(info, addr)

	go func() { // writer
		for {
			select {
			case <-pc.done:
				return
			case frame := <-pc.out:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ch.Send(frame); err != nil {
					pc.close()
					return
				}
			}
		}
	}()
	for { // reader
		_ = conn.SetReadDeadline(time.Now().Add(PeerTimeout + 5*time.Second))
		payload, err := ch.Recv()
		if err != nil {
			if t.ctx.Err() == nil && !pc.closed.Load() {
				t.logf("debug", "connection to %s ended: %v", info.Name, err)
			}
			break
		}
		t.cfg.Handler.OnPeerMessage(info.ID, payload)
	}
	pc.close()
	t.mu.Lock()
	if t.conns[info.ID] == pc {
		delete(t.conns, info.ID)
	}
	replaced := t.conns[info.ID] != nil
	t.mu.Unlock()
	if !replaced {
		t.cfg.Handler.PeerDown(info.ID)
	}
}

// Send implements Transport for the Node: non-blocking; frames are dropped
// when the peer's queue is full (the connection is unhealthy anyway).
func (t *Transport) Send(peerID string, frame []byte) error {
	t.mu.Lock()
	pc := t.conns[peerID]
	t.mu.Unlock()
	if pc == nil {
		return fmt.Errorf("edgehop: %s is not connected", short(peerID))
	}
	select {
	case pc.out <- frame:
		return nil
	default:
		if n := pc.drops.Add(1); n == 1 || n%1000 == 0 {
			t.logf("warn", "send queue to %s full (%d dropped)", short(peerID), n)
		}
		return fmt.Errorf("edgehop: send queue full")
	}
}

// Broadcast queues frame for every connected peer.
func (t *Transport) Broadcast(frame []byte) {
	t.mu.Lock()
	conns := make([]*peerConn, 0, len(t.conns))
	for _, c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	for _, c := range conns {
		select {
		case c.out <- frame:
		default:
			c.drops.Add(1)
		}
	}
}

// Disconnect closes the connection to peerID; the reconnect loop (if this
// machine is the initiator) will dial again after its backoff.
func (t *Transport) Disconnect(peerID string) {
	t.mu.Lock()
	pc := t.conns[peerID]
	t.mu.Unlock()
	if pc != nil {
		pc.close()
	}
}

// Connected reports whether peerID has a live connection.
func (t *Transport) Connected(peerID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.conns[peerID]
	return ok
}

// ---------------------------------------------------------------- reconnect

// isInitiator applies §2: the lexicographically smaller id connects.
func (t *Transport) isInitiator(peerID string) bool { return t.ID() < peerID }

func (t *Transport) addrFor(id string) string {
	if t.cfg.AddrFor != nil {
		if a := t.cfg.AddrFor(id); a != "" {
			return a
		}
	}
	if p, ok := t.cfg.Store.Peer(id); ok {
		return p.LastAddr
	}
	return ""
}

// ensureDialer starts the reconnect loop for a paired peer when this machine
// is the initiator.
func (t *Transport) ensureDialer(id string) {
	if !t.isInitiator(id) || t.ctx == nil {
		return
	}
	t.mu.Lock()
	if _, ok := t.dialers[id]; ok {
		t.mu.Unlock()
		return
	}
	d := &dialer{id: id, stop: make(chan struct{}), kick: make(chan struct{}, 1)}
	t.dialers[id] = d
	t.mu.Unlock()
	t.wg.Add(1)
	go t.dialLoop(d)
}

// Poke makes the reconnect loop of id (if any) try again right now, for
// example when Bonjour just announced it.
func (t *Transport) Poke(id string) {
	t.mu.Lock()
	d := t.dialers[id]
	t.mu.Unlock()
	if d != nil {
		select {
		case d.kick <- struct{}{}:
		default:
		}
	}
}

func (t *Transport) dialLoop(d *dialer) {
	defer t.wg.Done()
	backoff := minBackoff
	wait := func(dur time.Duration) bool {
		select {
		case <-d.stop:
			return false
		case <-t.ctx.Done():
			return false
		case <-time.After(dur):
			return true
		case <-d.kick:
			return true
		}
	}
	for {
		t.mu.Lock()
		existing := t.conns[d.id]
		t.mu.Unlock()
		if existing != nil {
			select { // wait for the current connection to end
			case <-existing.done:
			case <-d.stop:
				return
			case <-t.ctx.Done():
				return
			}
			if !wait(minBackoff) {
				return
			}
			continue
		}
		if _, paired := t.cfg.Store.Peer(d.id); !paired {
			return
		}
		addr := t.addrFor(d.id)
		if addr == "" {
			if !wait(maxBackoff) {
				return
			}
			continue
		}
		ok := t.dialOnce(d.id, addr, backoff == minBackoff)
		if ok {
			backoff = minBackoff
		} else {
			backoff = min(backoff*2, maxBackoff)
		}
		if !wait(backoff) {
			return
		}
	}
}

// dialOnce connects to a paired peer with a trusted handshake and runs the
// connection until it ends. It returns false when the attempt failed.
func (t *Transport) dialOnce(id, addr string, verbose bool) bool {
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(t.ctx, "tcp", addr)
	if err != nil {
		if verbose {
			t.logf("debug", "dial %s (%s): %v", short(id), addr, err)
		}
		return false
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
	}
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	ch, info, err := securechan.Initiate(conn, t.options())
	if err != nil {
		_ = conn.Close()
		if errors.Is(err, securechan.ErrUnpaired) {
			t.logf("warn", "%s no longer trusts this machine; pair again", short(id))
		} else if verbose {
			t.logf("debug", "handshake with %s: %v", short(id), err)
		}
		return false
	}
	if info.ID != id {
		t.logf("warn", "%s answered at %s instead of %s", short(info.ID), addr, short(id))
		_ = conn.Close()
		return false
	}
	_ = conn.SetDeadline(time.Time{})
	t.run(ch, conn, info, addr)
	return true
}

// ---------------------------------------------------------------- pairing

// PairStart connects to a machine with a pairing handshake. peerID may be
// empty when address is given. The handshake asks for the code through
// PairConfirm; progress is reported through edgehop.pairing events.
func (t *Transport) PairStart(peerID, address string) error {
	if t.ctx == nil {
		return errors.New("edgehop: transport not started")
	}
	addr := address
	if addr == "" {
		addr = t.addrFor(peerID)
	}
	if addr == "" {
		return fmt.Errorf("edgehop: no address known for %s", short(peerID))
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(DefaultPort))
	}
	t.mu.Lock()
	if t.pairing != nil {
		t.mu.Unlock()
		return errors.New("edgehop: a pairing is already in progress")
	}
	p := &pairing{peerID: peerID, addr: addr, code: make(chan string, 1), cancel: make(chan struct{})}
	t.pairing = p
	t.mu.Unlock()
	t.wg.Add(1)
	go t.runPairing(p)
	return nil
}

// PairConfirm supplies the code shown on the other machine.
func (t *Transport) PairConfirm(peerID, code string) error {
	t.mu.Lock()
	p := t.pairing
	t.mu.Unlock()
	if p == nil {
		return errors.New("edgehop: no pairing in progress")
	}
	if peerID != "" && p.peerID != "" && peerID != p.peerID {
		return errors.New("edgehop: code is for a different pairing")
	}
	select {
	case p.code <- code:
		return nil
	default:
		return errors.New("edgehop: code already submitted")
	}
}

// PairCancel aborts the pairing in progress.
func (t *Transport) PairCancel() {
	t.mu.Lock()
	p := t.pairing
	t.mu.Unlock()
	if p != nil {
		select {
		case <-p.cancel:
		default:
			close(p.cancel)
		}
	}
}

func (t *Transport) runPairing(p *pairing) {
	defer t.wg.Done()
	defer func() {
		t.mu.Lock()
		if t.pairing == p {
			t.pairing = nil
		}
		t.mu.Unlock()
	}()
	emit := func(stage, errText string) {
		t.cfg.Emit(Event{"edgehop.pairing", PairingState{Stage: stage, PeerID: p.peerID, Address: p.addr, Error: errText}})
	}
	emit("connecting", "")
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(t.ctx, "tcp", p.addr)
	if err != nil {
		t.logf("warn", "pairing: cannot reach %s: %v", p.addr, err)
		emit("failed", "unreachable")
		return
	}
	_ = conn.SetDeadline(time.Now().Add(pairingTimeout))
	opts := t.options()
	opts.Pair = true
	opts.AskCode = func() (string, error) {
		emit("waiting_code", "")
		select {
		case code := <-p.code:
			return code, nil
		case <-p.cancel:
			return "", errors.New("cancelled")
		case <-t.ctx.Done():
			return "", t.ctx.Err()
		case <-time.After(securechan.CodeTTL):
			return "", securechan.ErrCodeExpired
		}
	}
	ch, info, err := securechan.Initiate(conn, opts)
	if err != nil {
		_ = conn.Close()
		msg := pairError(err)
		t.logf("warn", "pairing with %s failed: %v", p.addr, err)
		if msg == "cancelled" {
			emit("cancelled", "")
		} else {
			emit("failed", msg)
		}
		return
	}
	_ = conn.SetDeadline(time.Time{})
	t.logf("info", "paired with %s", info.Name)
	t.cfg.Emit(Event{"edgehop.pairing", PairingState{Stage: "paired", PeerID: info.ID, Name: info.Name, Address: p.addr}})
	t.ensureDialer(info.ID)
	t.run(ch, conn, info, p.addr)
}

// Unpair stops reconnecting to id and drops its connection (the store is
// the Node's business).
func (t *Transport) Unpair(id string) {
	t.mu.Lock()
	if d, ok := t.dialers[id]; ok {
		close(d.stop)
		delete(t.dialers, id)
	}
	pc := t.conns[id]
	t.mu.Unlock()
	if pc != nil {
		pc.close()
	}
}

func pairError(err error) string {
	switch {
	case errors.Is(err, securechan.ErrPairFailed):
		return "wrong_code"
	case errors.Is(err, securechan.ErrCodeExpired):
		return "code_expired"
	case errors.Is(err, securechan.ErrUnpaired):
		return "refused"
	case err != nil && err.Error() == "securechan: ask code: cancelled":
		return "cancelled"
	}
	var re *securechan.RemoteError
	if errors.As(err, &re) {
		return re.Code
	}
	return "failed"
}

func (t *Transport) pairingRateLimited(peerID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	keep := t.failures[peerID][:0]
	for _, ts := range t.failures[peerID] {
		if now.Sub(ts) < pairFailWindow {
			keep = append(keep, ts)
		}
	}
	t.failures[peerID] = keep
	return len(keep) >= pairFailLimit
}

func (t *Transport) recordPairFailure(peerID string) {
	t.mu.Lock()
	t.failures[peerID] = append(t.failures[peerID], time.Now())
	t.mu.Unlock()
}
