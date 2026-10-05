package securechan

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"regexp"
	"testing"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/framing"
)

func seedIdentity(label string) Identity {
	return IdentityFromPriv(sha256.Sum256([]byte("securechan-test/" + label)))
}

type side struct {
	ch   *Channel
	peer PeerInfo
	err  error
}

// handshake runs Initiate and Accept concurrently over a net.Pipe and returns
// both outcomes plus the raw pipe ends (so tests can inject frames).
func handshake(t *testing.T, iOpts, aOpts Options) (init, acc side, iConn, aConn net.Conn) {
	t.Helper()
	iConn, aConn = net.Pipe()
	done := make(chan side, 1)
	go func() {
		ch, peer, err := Initiate(iConn, iOpts)
		done <- side{ch, peer, err}
	}()
	ch, peer, err := Accept(aConn, aOpts)
	acc = side{ch, peer, err}
	if acc.err != nil {
		// A failed acceptor closes its connection, like a real server would.
		_ = aConn.Close()
	}
	select {
	case init = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("initiator did not finish")
	}
	return init, acc, iConn, aConn
}

func trustedPair(t *testing.T) (a, b Identity, ta, tb *MemoryTrust) {
	t.Helper()
	a, b = seedIdentity("A"), seedIdentity("B")
	ta, tb = NewMemoryTrust(), NewMemoryTrust()
	if err := ta.Trust(PeerInfo{ID: b.ID(), Name: "B", Plat: "windows", StaticPub: b.Pub}); err != nil {
		t.Fatal(err)
	}
	if err := tb.Trust(PeerInfo{ID: a.ID(), Name: "A", Plat: "macos", StaticPub: a.Pub}); err != nil {
		t.Fatal(err)
	}
	return a, b, ta, tb
}

func exchange(t *testing.T, from, to *Channel, payload []byte) {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- from.Send(payload) }()
	got, err := to.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes want %d", len(got), len(payload))
	}
}

func TestIdentity(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id.Pub == ([32]byte{}) {
		t.Fatal("empty public key")
	}
	if got := IdentityFromPriv(id.Priv); got != id {
		t.Fatal("IdentityFromPriv differs from NewIdentity")
	}
	if ok, _ := regexp.MatchString(`^[0-9a-f]{32}$`, id.ID()); !ok {
		t.Fatalf("bad id %q", id.ID())
	}
	sum := sha256.Sum256(id.Pub[:])
	if id.ID() != IDFromPub(id.Pub) || id.ID()[:2] != hexByte(sum[0]) {
		t.Fatal("ID is not the first 16 bytes of SHA-256(pub)")
	}
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&15]})
}

func TestTrustedHandshakeBothWays(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	for _, dir := range []struct {
		name       string
		init, acc  Identity
		tInit, tAc *MemoryTrust
	}{{"A→B", a, b, ta, tb}, {"B→A", b, a, tb, ta}} {
		t.Run(dir.name, func(t *testing.T) {
			init, acc, _, _ := handshake(t,
				Options{Self: dir.init, Name: "I", Plat: "linux", Trust: dir.tInit},
				Options{Self: dir.acc, Name: "R", Plat: "linux", Trust: dir.tAc})
			if init.err != nil || acc.err != nil {
				t.Fatalf("init err %v, acc err %v", init.err, acc.err)
			}
			defer init.ch.Close()
			defer acc.ch.Close()
			if init.peer.ID != dir.acc.ID() || acc.peer.ID != dir.init.ID() {
				t.Fatalf("peer ids wrong: %s / %s", init.peer.ID, acc.peer.ID)
			}
			if init.peer.Name != "R" || acc.peer.Name != "I" || init.peer.StaticPub != dir.acc.Pub {
				t.Fatalf("peer info wrong: %+v %+v", init.peer, acc.peer)
			}
			if !init.ch.IsInitiator() || acc.ch.IsInitiator() {
				t.Fatal("IsInitiator wrong")
			}
			if init.ch.Peer() != init.peer {
				t.Fatal("Peer() differs")
			}
			exchange(t, init.ch, acc.ch, []byte{TypeMouseMove, 0, 1, 0, 2})
			exchange(t, acc.ch, init.ch, append([]byte{TypeControl}, `{"t":"ping","ts":1}`...))
		})
	}
}

func TestPairingHandshake(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	ta, tb := NewMemoryTrust(), NewMemoryTrust()
	codes := make(chan string, 1)
	var shownPeer PeerInfo
	init, acc, _, _ := handshake(t,
		Options{Self: a, Name: "A", Plat: "macos", Trust: ta, Pair: true, AskCode: func() (string, error) { return <-codes, nil }},
		Options{Self: b, Name: "B", Plat: "windows", Trust: tb, ShowCode: func(code string, peer PeerInfo) {
			if ok, _ := regexp.MatchString(`^[0-9]{6}$`, code); !ok {
				t.Errorf("code %q is not 6 digits", code)
			}
			shownPeer = peer
			codes <- code
		}})
	if init.err != nil || acc.err != nil {
		t.Fatalf("init err %v, acc err %v", init.err, acc.err)
	}
	defer init.ch.Close()
	defer acc.ch.Close()
	if shownPeer.ID != a.ID() || shownPeer.Name != "A" {
		t.Fatalf("ShowCode peer = %+v", shownPeer)
	}
	if pub, ok := ta.Trusted(b.ID()); !ok || pub != b.Pub {
		t.Fatal("initiator did not trust acceptor")
	}
	if pub, ok := tb.Trusted(a.ID()); !ok || pub != a.Pub {
		t.Fatal("acceptor did not trust initiator")
	}
	exchange(t, init.ch, acc.ch, []byte{TypeKey, 0, 4, 1, 0})
	exchange(t, acc.ch, init.ch, []byte{TypeKey, 0, 4, 0, 0})

	// Now that both trust each other a plain handshake works without a code.
	init2, acc2, _, _ := handshake(t,
		Options{Self: b, Trust: tb},
		Options{Self: a, Trust: ta})
	if init2.err != nil || acc2.err != nil {
		t.Fatalf("second handshake: %v / %v", init2.err, acc2.err)
	}
	init2.ch.Close()
	acc2.ch.Close()
}

func TestWrongCodeFailsBothSides(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	ta, tb := NewMemoryTrust(), NewMemoryTrust()
	codes := make(chan string, 1)
	init, acc, _, _ := handshake(t,
		Options{Self: a, Trust: ta, Pair: true, AskCode: func() (string, error) {
			c := <-codes
			if c == "000000" {
				return "000001", nil
			}
			return "000000", nil
		}},
		Options{Self: b, Trust: tb, ShowCode: func(code string, _ PeerInfo) { codes <- code }})
	if !errors.Is(acc.err, ErrPairFailed) {
		t.Fatalf("acceptor err = %v, want ErrPairFailed", acc.err)
	}
	if !errors.Is(init.err, ErrPairFailed) {
		t.Fatalf("initiator err = %v, want ErrPairFailed", init.err)
	}
	if _, ok := ta.Trusted(b.ID()); ok {
		t.Fatal("initiator trusted peer after failed pairing")
	}
	if _, ok := tb.Trusted(a.ID()); ok {
		t.Fatal("acceptor trusted peer after failed pairing")
	}
}

func TestUnknownWithoutPairing(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	init, acc, _, _ := handshake(t,
		Options{Self: a, Trust: NewMemoryTrust()},
		Options{Self: b, Trust: NewMemoryTrust(), ShowCode: func(string, PeerInfo) { t.Error("ShowCode must not be called") }})
	if !errors.Is(acc.err, ErrUnpaired) {
		t.Fatalf("acceptor err = %v, want ErrUnpaired", acc.err)
	}
	if !errors.Is(init.err, ErrUnpaired) {
		t.Fatalf("initiator err = %v, want ErrUnpaired", init.err)
	}
}

func TestAcceptorCannotShowCode(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	init, acc, _, _ := handshake(t,
		Options{Self: a, Trust: NewMemoryTrust(), Pair: true, AskCode: func() (string, error) { return "123456", nil }},
		Options{Self: b, Trust: NewMemoryTrust()})
	if !errors.Is(acc.err, ErrUnpaired) || !errors.Is(init.err, ErrUnpaired) {
		t.Fatalf("errs: acc %v init %v", acc.err, init.err)
	}
}

func TestInitiatorOnlyKnowsPeer(t *testing.T) {
	// The initiator trusts the acceptor but not the other way round and nobody
	// offers to pair: the acceptor must refuse.
	a, b := seedIdentity("A"), seedIdentity("B")
	ta := NewMemoryTrust()
	_ = ta.Trust(PeerInfo{ID: b.ID(), StaticPub: b.Pub})
	init, acc, _, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: NewMemoryTrust()})
	if !errors.Is(acc.err, ErrUnpaired) || !errors.Is(init.err, ErrUnpaired) {
		t.Fatalf("errs: acc %v init %v", acc.err, init.err)
	}
}

func TestAcceptorKnowsInitiatorButNotViceVersa(t *testing.T) {
	// hello.pair=true from a key the acceptor already trusts: still a pairing
	// handshake (the initiator needs the code) with hello_ack.pair=false.
	a, b := seedIdentity("A"), seedIdentity("B")
	ta, tb := NewMemoryTrust(), NewMemoryTrust()
	_ = tb.Trust(PeerInfo{ID: a.ID(), StaticPub: a.Pub})
	codes := make(chan string, 1)
	init, acc, _, _ := handshake(t,
		Options{Self: a, Trust: ta, Pair: true, AskCode: func() (string, error) { return <-codes, nil }},
		Options{Self: b, Trust: tb, ShowCode: func(c string, _ PeerInfo) { codes <- c }})
	if init.err != nil || acc.err != nil {
		t.Fatalf("errs: init %v acc %v", init.err, acc.err)
	}
	init.ch.Close()
	acc.ch.Close()
	if _, ok := ta.Trusted(b.ID()); !ok {
		t.Fatal("initiator did not learn the acceptor's key")
	}
}

func TestCodeExpired(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	codes := make(chan string, 1)
	now := time.Unix(1_700_000_000, 0)
	calls := 0
	init, acc, _, _ := handshake(t,
		Options{Self: a, Trust: NewMemoryTrust(), Pair: true, AskCode: func() (string, error) { return <-codes, nil }},
		Options{Self: b, Trust: NewMemoryTrust(), ShowCode: func(c string, _ PeerInfo) { codes <- c }, Now: func() time.Time {
			calls++
			if calls > 1 {
				return now.Add(CodeTTL + time.Second)
			}
			return now
		}})
	if !errors.Is(acc.err, ErrCodeExpired) {
		t.Fatalf("acceptor err = %v, want ErrCodeExpired", acc.err)
	}
	if init.err == nil {
		t.Fatal("initiator must not succeed when the acceptor refused")
	}
}

func TestCountersIncrement(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	init, acc, _, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init.err != nil || acc.err != nil {
		t.Fatal(init.err, acc.err)
	}
	defer init.ch.Close()
	defer acc.ch.Close()
	// After the handshake: initiator sent auth (1), received auth_ok (1).
	if s, r := init.ch.Counters(); s != 1 || r != 1 {
		t.Fatalf("initiator counters after handshake = %d/%d", s, r)
	}
	if s, r := acc.ch.Counters(); s != 1 || r != 1 {
		t.Fatalf("acceptor counters after handshake = %d/%d", s, r)
	}
	for i := 0; i < 5; i++ {
		exchange(t, init.ch, acc.ch, []byte{TypeScroll, 0, 0, 0, byte(i), 0})
	}
	for i := 0; i < 3; i++ {
		exchange(t, acc.ch, init.ch, []byte{TypeKeyText, 'x'})
	}
	if s, r := init.ch.Counters(); s != 6 || r != 4 {
		t.Fatalf("initiator counters = %d/%d, want 6/4", s, r)
	}
	if s, r := acc.ch.Counters(); s != 4 || r != 6 {
		t.Fatalf("acceptor counters = %d/%d, want 4/6", s, r)
	}
	// Identical plaintexts must produce different ciphertexts (fresh nonce).
	var rec recorder
	c1 := init.ch.sendAEAD.Seal(nil, nonceFor(0), []byte{1}, nil)
	c2 := init.ch.sendAEAD.Seal(nil, nonceFor(1), []byte{1}, nil)
	if bytes.Equal(c1, c2) {
		t.Fatal("ciphertexts with different counters are equal")
	}
	_ = rec
}

type recorder struct{ frames [][]byte }

func TestTamperedFrame(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	init, acc, iConn, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init.err != nil || acc.err != nil {
		t.Fatal(init.err, acc.err)
	}
	defer init.ch.Close()
	defer acc.ch.Close()

	// A valid ciphertext for the initiator's next counter, with one bit flipped.
	ct := init.ch.sendAEAD.Seal(nil, nonceFor(init.ch.sendCtr), []byte{TypeKeyText, 'a'}, nil)
	ct[len(ct)/2] ^= 0x01
	go func() { _ = framing.Write(iConn, ct) }()
	if _, err := acc.ch.Recv(); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("tampered frame: got %v, want ErrBadFrame", err)
	}
	// The channel stays dead afterwards.
	if _, err := acc.ch.Recv(); !errors.Is(err, ErrCounterWrap) && !errors.Is(err, ErrBadFrame) {
		t.Fatalf("after bad frame: %v", err)
	}
}

func TestShortAndReplayedFrames(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	init, acc, iConn, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init.err != nil || acc.err != nil {
		t.Fatal(init.err, acc.err)
	}
	defer init.ch.Close()
	defer acc.ch.Close()
	// Replay: a frame sealed with an old counter must fail on the receiver.
	ct := init.ch.sendAEAD.Seal(nil, nonceFor(0), []byte{TypeControl, '{', '}'}, nil)
	go func() { _ = framing.Write(iConn, ct) }()
	if _, err := acc.ch.Recv(); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("replayed frame: got %v, want ErrBadFrame", err)
	}

	init2, acc2, iConn2, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init2.err != nil || acc2.err != nil {
		t.Fatal(init2.err, acc2.err)
	}
	defer init2.ch.Close()
	defer acc2.ch.Close()
	go func() { _ = framing.Write(iConn2, []byte{1, 2, 3}) }()
	if _, err := acc2.ch.Recv(); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("short frame: got %v, want ErrBadFrame", err)
	}
}

func TestFrameLimit(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	init, acc, iConn, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init.err != nil || acc.err != nil {
		t.Fatal(init.err, acc.err)
	}
	defer init.ch.Close()
	defer acc.ch.Close()

	big := make([]byte, MaxPlaintext)
	big[0] = TypeKeyText
	big[len(big)-1] = 0x55
	exchange(t, init.ch, acc.ch, big)

	if err := init.ch.Send(make([]byte, MaxPlaintext+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized Send: got %v, want ErrTooLarge", err)
	}
	if s, _ := init.ch.Counters(); s != 2 {
		t.Fatalf("rejected Send consumed a counter: %d", s)
	}

	// A raw frame announcing more than 1 MiB is rejected before reading it.
	go func() { _, _ = iConn.Write([]byte{0x00, 0x10, 0x00, 0x01}) }()
	if _, err := acc.ch.Recv(); !errors.Is(err, framing.ErrTooLarge) {
		t.Fatalf("oversized frame: got %v, want ErrTooLarge", err)
	}
}

func TestCloseAndSendAfterClose(t *testing.T) {
	a, b, ta, tb := trustedPair(t)
	init, acc, _, _ := handshake(t, Options{Self: a, Trust: ta}, Options{Self: b, Trust: tb})
	if init.err != nil || acc.err != nil {
		t.Fatal(init.err, acc.err)
	}
	if err := init.ch.Close(); err != nil {
		t.Fatal(err)
	}
	if err := init.ch.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := init.ch.Send([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close = %v", err)
	}
	if _, err := acc.ch.Recv(); err == nil || errors.Is(err, ErrBadFrame) {
		t.Fatalf("peer Recv after close = %v", err)
	}
	acc.ch.Close()
}

func TestHelloWireFormat(t *testing.T) {
	a := seedIdentity("A")
	o := Options{Self: a, Name: "Thiru's Mac <1>", Plat: "macos"}
	var eph [32]byte
	eph[0] = 9
	b, err := encodeJSON(o.selfHello("hello", eph, true))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\{"t":"hello","v":1,"id":"[0-9a-f]{32}","name":"Thiru's Mac <1>","plat":"macos","spk":"[A-Za-z0-9+/]{43}=","epk":"[A-Za-z0-9+/]{43}=","pair":true\}$`)
	if !re.Match(b) {
		t.Fatalf("hello bytes %s do not match the wire format", b)
	}
}

func TestProtocolErrors(t *testing.T) {
	a, b := seedIdentity("A"), seedIdentity("B")
	tb := NewMemoryTrust()
	_ = tb.Trust(PeerInfo{ID: a.ID(), StaticPub: a.Pub})

	cases := []struct {
		name  string
		frame string
		want  error
	}{
		{"not json", `nope`, ErrProtocol},
		{"wrong type", `{"t":"hello_ack","v":1}`, ErrProtocol},
		{"bad version", `{"t":"hello","v":2,"id":"x","spk":"","epk":"","pair":false}`, ErrProtocol},
		{"remote error", `{"t":"error","code":"busy","message":"later"}`, &RemoteError{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iConn, aConn := net.Pipe()
			defer iConn.Close()
			defer aConn.Close()
			go func() {
				_ = framing.Write(iConn, []byte(tc.frame))
				// drain whatever the acceptor answers
				_, _ = io.Copy(io.Discard, iConn)
			}()
			_, _, err := Accept(aConn, Options{Self: b, Trust: tb})
			var re *RemoteError
			if _, isRemote := tc.want.(*RemoteError); isRemote {
				if !errors.As(err, &re) || re.Code != "busy" {
					t.Fatalf("got %v, want RemoteError busy", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("id mismatch", func(t *testing.T) {
		iConn, aConn := net.Pipe()
		defer iConn.Close()
		defer aConn.Close()
		o := Options{Self: a, Name: "A", Plat: "linux"}
		h := o.selfHello("hello", [32]byte{1}, false)
		h.ID = b.ID() // claims B's id with A's key
		frame, _ := encodeJSON(h)
		go func() {
			_ = framing.Write(iConn, frame)
			_, _ = io.Copy(io.Discard, iConn)
		}()
		_, _, err := Accept(aConn, Options{Self: b, Trust: tb})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("got %v, want ErrProtocol", err)
		}
	})
}

func TestMemoryTrust(t *testing.T) {
	m := NewMemoryTrust()
	a, b := seedIdentity("A"), seedIdentity("B")
	_ = m.Trust(PeerInfo{ID: b.ID(), Name: "B", StaticPub: b.Pub})
	_ = m.Trust(PeerInfo{ID: a.ID(), Name: "A", StaticPub: a.Pub})
	if len(m.Peers()) != 2 || m.Peers()[0].ID > m.Peers()[1].ID {
		t.Fatalf("Peers = %+v", m.Peers())
	}
	m.Forget(a.ID())
	if _, ok := m.Trusted(a.ID()); ok {
		t.Fatal("Forget did not remove")
	}
	var zero MemoryTrust
	if err := zero.Trust(PeerInfo{ID: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestDerivePSKSymmetric(t *testing.T) {
	if DerivePSK("123456", "aaa", "bbb") != DerivePSK("123456", "bbb", "aaa") {
		t.Fatal("PSK depends on id order")
	}
	if DerivePSK("123456", "aaa", "bbb") == DerivePSK("123457", "aaa", "bbb") {
		t.Fatal("PSK does not depend on the code")
	}
}
