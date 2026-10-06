package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

func TestIdentityAndStoreOnDisk(t *testing.T) {
	dir, err := ConfigDir(filepath.Join(t.TempDir(), "edgehop"))
	if err != nil {
		t.Fatal(err)
	}
	id1, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 || len(id1.ID()) != 32 {
		t.Fatalf("identity not stable: %s vs %s", id1.ID(), id2.ID())
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(filepath.Join(dir, IdentityFile)); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("identity file mode: %v %v", st.Mode(), err)
		}
	}
	pk, err := ParsePubKey(PubKeyB64(id1.Pub))
	if err != nil || pk != id1.Pub {
		t.Fatalf("pubkey round trip: %v", err)
	}
	if _, err := ParsePubKey("nope"); err == nil {
		t.Fatal("bad base64 accepted")
	}

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Settings() != DefaultSettings() {
		t.Fatalf("defaults: %+v", s.Settings())
	}
	other, _ := securechan.NewIdentity()
	info := securechan.PeerInfo{ID: other.ID(), Name: "Other", Plat: "macos", StaticPub: other.Pub}
	if err := s.Trust(info); err != nil {
		t.Fatal(err)
	}
	if err := s.Trust(securechan.PeerInfo{ID: "bogus", Name: "x", StaticPub: other.Pub}); err == nil {
		t.Fatal("id/key mismatch accepted")
	}
	if err := s.SetLastAddr(other.ID(), "10.0.0.2:24800"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLayout(Layout{UpdatedAt: 7, Tiles: []Tile{{ID: id1.ID(), Col: 0, Row: 0}, {ID: other.ID(), Col: 1, Row: 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLayout(Layout{Tiles: []Tile{{ID: "a", Col: 0, Row: 0}, {ID: "b", Col: 0, Row: 0}}}); err == nil {
		t.Fatal("invalid layout stored")
	}
	set := s.Settings()
	set.SwitchDelayMs = 99999 // clamped
	set.Port = -5
	set.HotkeySwitch = " Ctrl + Alt "
	if err := s.SetSettings(set); err != nil {
		t.Fatal(err)
	}
	if err := s.SetName("Desk"); err != nil {
		t.Fatal(err)
	}

	// Reopen: everything persisted, settings normalised.
	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsTrusted(other.ID(), other.Pub) || s2.IsTrusted(other.ID(), id1.Pub) {
		t.Fatal("trust not persisted correctly")
	}
	p, ok := s2.Peer(other.ID())
	if !ok || p.Name != "Other" || p.Plat != "macos" || p.LastAddr != "10.0.0.2:24800" {
		t.Fatalf("peer = %+v", p)
	}
	if l := s2.Layout(); len(l.Tiles) != 2 || l.UpdatedAt != 7 {
		t.Fatalf("layout = %+v", l)
	}
	got := s2.Settings()
	if got.SwitchDelayMs != 5000 || got.Port != DefaultPort || got.HotkeySwitch != "ctrl+alt" {
		t.Fatalf("settings = %+v", got)
	}
	if s2.Name() != "Desk" {
		t.Fatalf("name = %q", s2.Name())
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(s2.Path()); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("config file mode: %v %v", st.Mode(), err)
		}
	}
	// Forget removes the peer and its tile.
	if err := s2.Forget(other.ID()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Peer(other.ID()); ok || s2.Layout().Has(other.ID()) || len(s2.Peers()) != 0 {
		t.Fatal("forget incomplete")
	}
	if err := s2.UpdatePeerInfo("missing", "n", "p"); err != nil {
		t.Fatal(err)
	}
	if Platform() == "" || DefaultMachineName() == "" {
		t.Fatal("platform/name")
	}
}
