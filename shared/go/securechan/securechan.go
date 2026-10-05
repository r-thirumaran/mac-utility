// Package securechan implements the Edgehop handshake and encrypted channel of
// docs/edgehop-protocol.md §3–§4: X25519 (static + ephemeral keys on both
// sides), HKDF-SHA256 key derivation bound to the exact handshake bytes, an
// optional 6-digit pairing code mixed in as a PSK, and ChaCha20-Poly1305
// frames with per-direction keys and counters carried over the length-prefixed
// framing of package framing.
//
// The two entry points are Initiate (the side that connects) and Accept (the
// side that listens). Both return a *Channel once the peer has proven it holds
// the matching keys (and, for a pairing handshake, the code shown on the
// acceptor), together with the peer's identity.
//
// The package is transport-agnostic: it works over any io.ReadWriter. It does
// not implement timeouts, the 120 s pairing-code display or the pairing rate
// limit; those belong to the application (set deadlines on the net.Conn).
package securechan

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"

	"github.com/r-thirumaran/mac-utility/shared/go/framing"
)

// Version is the protocol version carried in hello/hello_ack.
const Version = 1

// CodeTTL is how long a pairing code stays valid after the acceptor generated
// it (docs/edgehop-protocol.md §4: "shown in its UI for 120 s").
const CodeTTL = 120 * time.Second

// Encrypted message type bytes (docs/edgehop-protocol.md §5). A Channel
// payload is one of these bytes followed by the message body.
const (
	TypeControl     byte = 0x01
	TypeMouseMove   byte = 0x02
	TypeMouseButton byte = 0x03
	TypeScroll      byte = 0x04
	TypeKey         byte = 0x05
	TypeKeyText     byte = 0x06
)

// MaxPlaintext is the largest payload Send accepts: a full frame minus the
// 16-byte Poly1305 tag.
const MaxPlaintext = framing.MaxFrame - chacha20poly1305.Overhead

// Errors returned by the handshake and the channel.
var (
	// ErrUnpaired: the peer's static key is unknown and neither side offered to pair.
	ErrUnpaired = errors.New("securechan: peer not paired")
	// ErrPairFailed: the first encrypted frame of a pairing handshake did not
	// decrypt — wrong code, or the handshake was tampered with.
	ErrPairFailed = errors.New("securechan: pairing failed")
	// ErrCodeExpired: the pairing code was redeemed more than CodeTTL after it was shown.
	ErrCodeExpired = errors.New("securechan: pairing code expired")
	// ErrBadFrame: a frame failed authentication or was malformed; the caller must Close.
	ErrBadFrame = errors.New("securechan: bad frame")
	// ErrCounterWrap: the 64-bit nonce counter is exhausted; the caller must Close.
	ErrCounterWrap = errors.New("securechan: counter wrap")
	// ErrProtocol: the peer sent something the protocol does not allow at this point.
	ErrProtocol = errors.New("securechan: protocol error")
	// ErrClosed: Send/Recv after Close.
	ErrClosed = errors.New("securechan: channel closed")
	// ErrTooLarge is framing.ErrTooLarge: the payload does not fit in one frame.
	ErrTooLarge = framing.ErrTooLarge
)

// RemoteError is an {"t":"error"} message received from the peer.
type RemoteError struct {
	Code    string
	Message string
}

// Error implements error.
func (e *RemoteError) Error() string {
	if e.Message == "" {
		return "securechan: remote error " + e.Code
	}
	return "securechan: remote error " + e.Code + ": " + e.Message
}

// Identity is a long-term X25519 key pair.
type Identity struct {
	Priv, Pub [32]byte
}

// NewIdentity generates a fresh identity from crypto/rand.
func NewIdentity() (Identity, error) {
	var priv [32]byte
	if _, err := io.ReadFull(rand.Reader, priv[:]); err != nil {
		return Identity{}, fmt.Errorf("securechan: generate identity: %w", err)
	}
	return IdentityFromPriv(priv), nil
}

// IdentityFromPriv rebuilds an identity (and its public key) from a stored
// private key.
func IdentityFromPriv(priv [32]byte) Identity {
	id := Identity{Priv: priv}
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err == nil {
		copy(id.Pub[:], pub)
	}
	return id
}

// ID returns the machine id: lowercase hex of the first 16 bytes of
// SHA-256(static public key).
func (id Identity) ID() string { return IDFromPub(id.Pub) }

// IDFromPub derives the machine id of a static public key.
func IDFromPub(pub [32]byte) string {
	sum := sha256.Sum256(pub[:])
	return hex.EncodeToString(sum[:16])
}

// PeerInfo identifies a remote machine.
type PeerInfo struct {
	ID        string
	Name      string
	Plat      string
	StaticPub [32]byte
}

// TrustStore remembers the static keys of paired machines.
type TrustStore interface {
	// Trusted returns the stored public key for id.
	Trusted(id string) (pub [32]byte, ok bool)
	// Trust stores info as a paired machine (replacing any older entry).
	Trust(info PeerInfo) error
}

// MemoryTrust is an in-memory TrustStore, safe for concurrent use.
type MemoryTrust struct {
	mu    sync.Mutex
	peers map[string]PeerInfo
}

// NewMemoryTrust returns an empty MemoryTrust.
func NewMemoryTrust() *MemoryTrust { return &MemoryTrust{peers: map[string]PeerInfo{}} }

// Trusted implements TrustStore.
func (m *MemoryTrust) Trusted(id string) ([32]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.peers[id]
	return p.StaticPub, ok
}

// Trust implements TrustStore.
func (m *MemoryTrust) Trust(info PeerInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peers == nil {
		m.peers = map[string]PeerInfo{}
	}
	m.peers[info.ID] = info
	return nil
}

// Forget removes id.
func (m *MemoryTrust) Forget(id string) {
	m.mu.Lock()
	delete(m.peers, id)
	m.mu.Unlock()
}

// Peers lists the stored machines sorted by id.
func (m *MemoryTrust) Peers() []PeerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PeerInfo, 0, len(m.peers))
	for _, p := range m.peers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Options configures one handshake.
type Options struct {
	// Self is this machine's long-term identity.
	Self Identity
	// Name and Plat are advertised in hello/hello_ack ("macos", "windows", "linux").
	Name, Plat string
	// Trust holds the paired machines; it is consulted and, after a successful
	// pairing handshake, updated. Required.
	Trust TrustStore
	// Pair (initiator only) sets hello.pair: the user asked to pair with this
	// machine now. The acceptor ignores it; it pairs whenever hello.pair is
	// true and ShowCode is set.
	Pair bool
	// AskCode (initiator) asks the user for the code shown on the other
	// machine. Required for a pairing handshake.
	AskCode func() (string, error)
	// ShowCode (acceptor) displays the freshly generated code and the peer
	// asking to pair. Required for the acceptor to accept pairing requests;
	// when nil, unknown peers get an "unpaired" error.
	ShowCode func(code string, peer PeerInfo)
	// Rand supplies the ephemeral private key (32 bytes) and, on the acceptor,
	// 4 bytes for the pairing code. nil means crypto/rand; tests inject fixed bytes.
	Rand io.Reader
	// Now overrides the clock used for the CodeTTL check. nil means time.Now.
	Now func() time.Time
}

func (o *Options) rand() io.Reader {
	if o.Rand != nil {
		return o.Rand
	}
	return rand.Reader
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// hello is the plaintext handshake message. Field order is the wire order
// (t, v, id, name, plat, spk, epk, pair); encoding/json keeps struct order.
type hello struct {
	T    string `json:"t"`
	V    int    `json:"v"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Plat string `json:"plat"`
	SPK  string `json:"spk"`
	EPK  string `json:"epk"`
	Pair bool   `json:"pair"`
}

type errorMsg struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// control is the minimal shape of any plaintext or control message.
type control struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// encodeJSON marshals without HTML escaping and without a trailing newline,
// so the bytes are the canonical, whitespace-free form shared with Swift.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeError(conn io.Writer, code, msg string) {
	b, err := encodeJSON(errorMsg{T: "error", Code: code, Message: msg})
	if err == nil {
		_ = framing.Write(conn, b)
	}
}

func readRand(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, fmt.Errorf("securechan: random: %w", err)
	}
	return b, nil
}

func newEphemeral(r io.Reader) (priv, pub [32]byte, err error) {
	b, err := readRand(r, 32)
	if err != nil {
		return priv, pub, err
	}
	copy(priv[:], b)
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return priv, pub, fmt.Errorf("securechan: ephemeral key: %w", err)
	}
	copy(pub[:], p)
	return priv, pub, nil
}

func (o *Options) selfHello(t string, ephPub [32]byte, pair bool) hello {
	return hello{
		T:    t,
		V:    Version,
		ID:   o.Self.ID(),
		Name: o.Name,
		Plat: o.Plat,
		SPK:  base64.StdEncoding.EncodeToString(o.Self.Pub[:]),
		EPK:  base64.StdEncoding.EncodeToString(ephPub[:]),
		Pair: pair,
	}
}

// parseHello validates a received hello/hello_ack of the expected type and
// returns the decoded message and the peer's two public keys.
func parseHello(payload []byte, wantT string) (h hello, spk, epk [32]byte, err error) {
	var probe control
	if jErr := json.Unmarshal(payload, &probe); jErr != nil {
		return h, spk, epk, fmt.Errorf("%w: malformed handshake JSON: %v", ErrProtocol, jErr)
	}
	if probe.T == "error" {
		if probe.Code == "unpaired" {
			return h, spk, epk, ErrUnpaired
		}
		return h, spk, epk, &RemoteError{Code: probe.Code, Message: probe.Message}
	}
	if probe.T != wantT {
		return h, spk, epk, fmt.Errorf("%w: expected %q, got %q", ErrProtocol, wantT, probe.T)
	}
	if jErr := json.Unmarshal(payload, &h); jErr != nil {
		return h, spk, epk, fmt.Errorf("%w: malformed %s: %v", ErrProtocol, wantT, jErr)
	}
	if h.V != Version {
		return h, spk, epk, fmt.Errorf("%w: unsupported version %d", ErrProtocol, h.V)
	}
	if spk, err = decodeKey(h.SPK); err != nil {
		return h, spk, epk, fmt.Errorf("%w: spk: %v", ErrProtocol, err)
	}
	if epk, err = decodeKey(h.EPK); err != nil {
		return h, spk, epk, fmt.Errorf("%w: epk: %v", ErrProtocol, err)
	}
	if IDFromPub(spk) != h.ID {
		return h, spk, epk, fmt.Errorf("%w: id does not match static key", ErrProtocol)
	}
	return h, spk, epk, nil
}

func decodeKey(s string) ([32]byte, error) {
	var k [32]byte
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return k, err
	}
	if len(b) != 32 {
		return k, fmt.Errorf("key is %d bytes, want 32", len(b))
	}
	copy(k[:], b)
	return k, nil
}

// isTrusted reports whether the store knows id with exactly this key.
func isTrusted(store TrustStore, id string, spk [32]byte) bool {
	pub, ok := store.Trusted(id)
	return ok && subtle.ConstantTimeCompare(pub[:], spk[:]) == 1
}

// DerivePSK computes the pairing pre-shared key of §4 from the 6-digit code
// and the two machine ids (in either order).
func DerivePSK(code, idA, idB string) [32]byte {
	lo, hi := idA, idB
	if hi < lo {
		lo, hi = hi, lo
	}
	var psk [32]byte
	r := hkdf.New(sha256.New, []byte(code), []byte("edgehop-pair"), []byte(lo+hi))
	_, _ = io.ReadFull(r, psk[:])
	return psk
}

// Transcript hashes the two handshake messages exactly as they were sent.
func Transcript(helloBytes, helloAckBytes []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte("edgehop-v1"))
	h.Write(helloBytes)
	h.Write(helloAckBytes)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// DeriveKeys runs the §4 key schedule and returns key_init→acc and key_acc→init.
func DeriveKeys(dh1, dh2, dh3 []byte, psk [32]byte, transcript [32]byte) (initToAcc, accToInit [32]byte) {
	ikm := make([]byte, 0, 32*4)
	ikm = append(ikm, dh1...)
	ikm = append(ikm, dh2...)
	ikm = append(ikm, dh3...)
	ikm = append(ikm, psk[:]...)
	r := hkdf.New(sha256.New, ikm, transcript[:], []byte("edgehop-v1-keys"))
	var okm [64]byte
	_, _ = io.ReadFull(r, okm[:])
	copy(initToAcc[:], okm[:32])
	copy(accToInit[:], okm[32:])
	return initToAcc, accToInit
}

// dh wraps X25519 and rejects the all-zero shared secret (low-order peer point).
func dh(priv, pub [32]byte) ([]byte, error) {
	out, err := curve25519.X25519(priv[:], pub[:])
	if err != nil {
		return nil, fmt.Errorf("%w: x25519: %v", ErrProtocol, err)
	}
	return out, nil
}

// GenerateCode draws a 6-digit pairing code from r (4 bytes).
func GenerateCode(r io.Reader) (string, error) {
	b, err := readRand(r, 4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(b)%1000000), nil
}

// EncodeControl builds a control payload: TypeControl followed by the JSON of v.
func EncodeControl(v any) ([]byte, error) {
	b, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return append([]byte{TypeControl}, b...), nil
}

// Split separates a received payload into its type byte and body.
func Split(payload []byte) (typ byte, body []byte, ok bool) {
	if len(payload) == 0 {
		return 0, nil, false
	}
	return payload[0], payload[1:], true
}

// Initiate performs the initiator side of the handshake over conn and returns
// the encrypted channel and the peer's identity. With o.Pair false the
// acceptor must already trust this machine and vice versa, otherwise
// ErrUnpaired. In a pairing handshake o.AskCode is called after hello_ack
// arrives; a wrong code surfaces as ErrPairFailed. The caller owns conn and
// must close it on error.
func Initiate(conn io.ReadWriter, o Options) (*Channel, PeerInfo, error) {
	if o.Trust == nil {
		return nil, PeerInfo{}, errors.New("securechan: Options.Trust is required")
	}
	ephPriv, ephPub, err := newEphemeral(o.rand())
	if err != nil {
		return nil, PeerInfo{}, err
	}
	helloBytes, err := encodeJSON(o.selfHello("hello", ephPub, o.Pair))
	if err != nil {
		return nil, PeerInfo{}, fmt.Errorf("securechan: encode hello: %w", err)
	}
	if err := framing.Write(conn, helloBytes); err != nil {
		return nil, PeerInfo{}, err
	}
	ackBytes, err := framing.Read(conn)
	if err != nil {
		return nil, PeerInfo{}, fmt.Errorf("securechan: read hello_ack: %w", err)
	}
	ack, spk, epk, err := parseHello(ackBytes, "hello_ack")
	if err != nil {
		return nil, PeerInfo{}, err
	}
	peer := PeerInfo{ID: ack.ID, Name: ack.Name, Plat: ack.Plat, StaticPub: spk}
	known := isTrusted(o.Trust, ack.ID, spk)
	pairing := o.Pair || ack.Pair
	if !pairing && !known {
		writeError(conn, "unpaired", "")
		return nil, peer, ErrUnpaired
	}
	var psk [32]byte
	if pairing {
		if o.AskCode == nil {
			writeError(conn, "unpaired", "no pairing code available")
			return nil, peer, fmt.Errorf("%w: pairing requested but Options.AskCode is nil", ErrUnpaired)
		}
		code, err := o.AskCode()
		if err != nil {
			return nil, peer, fmt.Errorf("securechan: ask code: %w", err)
		}
		psk = DerivePSK(code, o.Self.ID(), ack.ID)
	}
	dh1, err := dh(ephPriv, epk)
	if err != nil {
		return nil, peer, err
	}
	dh2, err := dh(o.Self.Priv, epk)
	if err != nil {
		return nil, peer, err
	}
	dh3, err := dh(ephPriv, spk)
	if err != nil {
		return nil, peer, err
	}
	sendKey, recvKey := DeriveKeys(dh1, dh2, dh3, psk, Transcript(helloBytes, ackBytes))
	ch, err := newChannel(conn, sendKey, recvKey, peer, true)
	if err != nil {
		return nil, peer, err
	}
	auth, _ := EncodeControl(control{T: "auth"})
	if err := ch.Send(auth); err != nil {
		return nil, peer, err
	}
	reply, err := ch.Recv()
	if err != nil {
		if pairing && (errors.Is(err, ErrBadFrame) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe)) {
			return nil, peer, fmt.Errorf("%w: %v", ErrPairFailed, err)
		}
		return nil, peer, err
	}
	if err := expectControl(reply, "auth_ok"); err != nil {
		return nil, peer, err
	}
	if pairing {
		if err := o.Trust.Trust(peer); err != nil {
			return nil, peer, fmt.Errorf("securechan: trust: %w", err)
		}
	}
	return ch, peer, nil
}

// Accept performs the acceptor side of the handshake. A hello from an unknown
// key with pair=false is answered with {"t":"error","code":"unpaired"} and
// ErrUnpaired. A hello with pair=true starts a pairing handshake: a 6-digit
// code is generated, shown through o.ShowCode and mixed into the keys; the
// initiator's first encrypted frame then proves it knew the code
// (ErrPairFailed otherwise). The peer is stored in o.Trust only after that
// proof. The caller owns conn and must close it on error.
func Accept(conn io.ReadWriter, o Options) (*Channel, PeerInfo, error) {
	if o.Trust == nil {
		return nil, PeerInfo{}, errors.New("securechan: Options.Trust is required")
	}
	helloBytes, err := framing.Read(conn)
	if err != nil {
		return nil, PeerInfo{}, fmt.Errorf("securechan: read hello: %w", err)
	}
	h, spk, epk, err := parseHello(helloBytes, "hello")
	if err != nil {
		if !errors.Is(err, ErrUnpaired) {
			writeError(conn, "protocol", err.Error())
		}
		return nil, PeerInfo{}, err
	}
	peer := PeerInfo{ID: h.ID, Name: h.Name, Plat: h.Plat, StaticPub: spk}
	known := isTrusted(o.Trust, h.ID, spk)
	pairing := h.Pair
	if !pairing && !known {
		writeError(conn, "unpaired", "")
		return nil, peer, ErrUnpaired
	}
	if pairing && o.ShowCode == nil {
		writeError(conn, "unpaired", "pairing not available")
		return nil, peer, fmt.Errorf("%w: pairing requested but Options.ShowCode is nil", ErrUnpaired)
	}
	var (
		psk     [32]byte
		shownAt time.Time
	)
	if pairing {
		code, err := GenerateCode(o.rand())
		if err != nil {
			return nil, peer, err
		}
		psk = DerivePSK(code, o.Self.ID(), h.ID)
		shownAt = o.now()
		o.ShowCode(code, peer)
	}
	ephPriv, ephPub, err := newEphemeral(o.rand())
	if err != nil {
		return nil, peer, err
	}
	ackBytes, err := encodeJSON(o.selfHello("hello_ack", ephPub, !known))
	if err != nil {
		return nil, peer, fmt.Errorf("securechan: encode hello_ack: %w", err)
	}
	if err := framing.Write(conn, ackBytes); err != nil {
		return nil, peer, err
	}
	dh1, err := dh(ephPriv, epk)
	if err != nil {
		return nil, peer, err
	}
	dh2, err := dh(ephPriv, spk)
	if err != nil {
		return nil, peer, err
	}
	dh3, err := dh(o.Self.Priv, epk)
	if err != nil {
		return nil, peer, err
	}
	recvKey, sendKey := DeriveKeys(dh1, dh2, dh3, psk, Transcript(helloBytes, ackBytes))
	ch, err := newChannel(conn, sendKey, recvKey, peer, false)
	if err != nil {
		return nil, peer, err
	}
	first, err := ch.Recv()
	if err != nil {
		if pairing && errors.Is(err, ErrBadFrame) {
			return nil, peer, fmt.Errorf("%w: %v", ErrPairFailed, err)
		}
		return nil, peer, err
	}
	if err := expectControl(first, "auth"); err != nil {
		return nil, peer, err
	}
	if pairing && o.now().Sub(shownAt) > CodeTTL {
		return nil, peer, ErrCodeExpired
	}
	authOK, _ := EncodeControl(control{T: "auth_ok"})
	if err := ch.Send(authOK); err != nil {
		return nil, peer, err
	}
	if pairing {
		if err := o.Trust.Trust(peer); err != nil {
			return nil, peer, fmt.Errorf("securechan: trust: %w", err)
		}
	}
	return ch, peer, nil
}

func expectControl(payload []byte, wantT string) error {
	typ, body, ok := Split(payload)
	if !ok || typ != TypeControl {
		return fmt.Errorf("%w: expected control %q", ErrProtocol, wantT)
	}
	var c control
	if err := json.Unmarshal(body, &c); err != nil {
		return fmt.Errorf("%w: malformed control: %v", ErrProtocol, err)
	}
	if c.T == "error" {
		return &RemoteError{Code: c.Code, Message: c.Message}
	}
	if c.T != wantT {
		return fmt.Errorf("%w: expected %q, got %q", ErrProtocol, wantT, c.T)
	}
	return nil
}

// Channel is an established encrypted connection. Send and Recv are each
// safe for concurrent use and independent of each other (separate locks and
// counters). After Recv returns ErrBadFrame or ErrCounterWrap the channel is
// compromised and the caller must Close it.
type Channel struct {
	conn      io.ReadWriter
	peer      PeerInfo
	initiator bool

	sendMu   sync.Mutex
	sendAEAD cipher.AEAD
	sendCtr  uint64
	sendDead bool

	recvMu   sync.Mutex
	recvAEAD cipher.AEAD
	recvCtr  uint64
	recvDead bool

	closeMu sync.Mutex
	closed  bool
}

func newChannel(conn io.ReadWriter, sendKey, recvKey [32]byte, peer PeerInfo, initiator bool) (*Channel, error) {
	s, err := chacha20poly1305.New(sendKey[:])
	if err != nil {
		return nil, err
	}
	r, err := chacha20poly1305.New(recvKey[:])
	if err != nil {
		return nil, err
	}
	return &Channel{conn: conn, peer: peer, initiator: initiator, sendAEAD: s, recvAEAD: r}, nil
}

// Peer returns the authenticated identity of the other side.
func (c *Channel) Peer() PeerInfo { return c.peer }

// IsInitiator reports whether this side ran Initiate.
func (c *Channel) IsInitiator() bool { return c.initiator }

func nonceFor(ctr uint64) []byte {
	var n [chacha20poly1305.NonceSize]byte
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n[:]
}

// Send encrypts plaintext (type byte + body) with this direction's key and
// next counter and writes it as one frame. Payloads above MaxPlaintext return
// ErrTooLarge without consuming a counter.
func (c *Channel) Send(plaintext []byte) error {
	if len(plaintext) > MaxPlaintext {
		return ErrTooLarge
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.isClosed() {
		return ErrClosed
	}
	if c.sendDead {
		return ErrCounterWrap
	}
	ct := c.sendAEAD.Seal(nil, nonceFor(c.sendCtr), plaintext, nil)
	if c.sendCtr == ^uint64(0) {
		c.sendDead = true
	} else {
		c.sendCtr++
	}
	return framing.Write(c.conn, ct)
}

// Recv reads one frame and returns its decrypted payload. Authentication
// failure returns ErrBadFrame, an exhausted counter ErrCounterWrap; both are
// fatal for the connection.
func (c *Channel) Recv() ([]byte, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	if c.isClosed() {
		return nil, ErrClosed
	}
	if c.recvDead {
		return nil, ErrCounterWrap
	}
	ct, err := framing.Read(c.conn)
	if err != nil {
		return nil, err
	}
	if len(ct) < chacha20poly1305.Overhead {
		c.recvDead = true
		return nil, fmt.Errorf("%w: frame shorter than the tag", ErrBadFrame)
	}
	pt, err := c.recvAEAD.Open(nil, nonceFor(c.recvCtr), ct, nil)
	if err != nil {
		c.recvDead = true
		return nil, ErrBadFrame
	}
	if c.recvCtr == ^uint64(0) {
		c.recvDead = true
	} else {
		c.recvCtr++
	}
	return pt, nil
}

// Counters returns the number of frames sent and received so far (for tests
// and diagnostics).
func (c *Channel) Counters() (sent, received uint64) {
	c.sendMu.Lock()
	sent = c.sendCtr
	c.sendMu.Unlock()
	c.recvMu.Lock()
	received = c.recvCtr
	c.recvMu.Unlock()
	return sent, received
}

func (c *Channel) isClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closed
}

// Close marks the channel closed and closes the underlying connection when it
// implements io.Closer. It is idempotent.
func (c *Channel) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()
	if cl, ok := c.conn.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}
