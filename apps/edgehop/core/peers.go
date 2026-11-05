package core

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/r-thirumaran/mac-utility/shared/go/appdirs"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

// ConfigFile is the JSON file (mode 0600) holding peers, layout, settings
// and the machine name.
const ConfigFile = "config.json"

// Peer is a paired machine as remembered in the config file. Displays is
// the peer's last known monitor arrangement, kept so an offline machine
// still draws on the canvas and its boundaries still exist (§6 `layout`).
type Peer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Plat      string    `json:"plat"`
	StaticPub string    `json:"staticPub"` // base64
	LastAddr  string    `json:"lastAddr,omitempty"`
	Displays  []Display `json:"displays,omitempty"`
}

// Settings are the user preferences (docs/ARCHITECTURE.md §5
// edgehop.settings.*).
type Settings struct {
	SwitchDelayMs         int    `json:"switchDelayMs"`
	EdgeCornerDeadZonePx  int    `json:"edgeCornerDeadZonePx"`
	SwapCmdCtrl           bool   `json:"swapCmdCtrl"`
	ClipboardSync         bool   `json:"clipboardSync"`
	HotkeySwitch          string `json:"hotkeySwitch"`
	LockCursorWhileRemote bool   `json:"lockCursorWhileRemote"`
	ShakeToLocate         bool   `json:"shakeToLocate"`
	Port                  int    `json:"port"`
}

// DefaultSettings returns the defaults.
func DefaultSettings() Settings {
	return Settings{
		SwitchDelayMs:         0,
		EdgeCornerDeadZonePx:  32,
		SwapCmdCtrl:           true,
		ClipboardSync:         true,
		HotkeySwitch:          "ctrl+alt",
		LockCursorWhileRemote: true,
		ShakeToLocate:         true,
		Port:                  24800,
	}
}

// Normalize clamps out-of-range values and fills blanks with defaults.
func (s Settings) Normalize() Settings {
	d := DefaultSettings()
	if s.SwitchDelayMs < 0 {
		s.SwitchDelayMs = 0
	}
	if s.SwitchDelayMs > 5000 {
		s.SwitchDelayMs = 5000
	}
	if s.EdgeCornerDeadZonePx < 0 {
		s.EdgeCornerDeadZonePx = 0
	}
	if s.EdgeCornerDeadZonePx > 500 {
		s.EdgeCornerDeadZonePx = 500
	}
	if strings.TrimSpace(s.HotkeySwitch) == "" {
		s.HotkeySwitch = d.HotkeySwitch
	}
	s.HotkeySwitch = strings.ToLower(strings.ReplaceAll(s.HotkeySwitch, " ", ""))
	if s.Port < 0 || s.Port > 65535 {
		s.Port = d.Port
	}
	return s
}

// configVersion is the current config.json layout. Version 2 added
// settings.shakeToLocate (default on), peers[].displays and layout.machines.
const configVersion = 2

// configFile is the on-disk document.
type configFile struct {
	Version  int      `json:"version"`
	Name     string   `json:"name,omitempty"`
	Peers    []Peer   `json:"peers"`
	Layout   Layout   `json:"layout"`
	Settings Settings `json:"settings"`
}

// PeerStore is the persistent configuration: trusted peers (it implements
// securechan.TrustStore), the layout and the settings. Every mutation is
// written through to disk. Safe for concurrent use.
type PeerStore struct {
	mu   sync.Mutex
	path string // "" = in-memory only
	cfg  configFile
}

// NewMemoryStore returns a store that is never written to disk (tests).
func NewMemoryStore() *PeerStore {
	return &PeerStore{cfg: configFile{Version: configVersion, Settings: DefaultSettings()}}
}

// OpenStore loads <dir>/config.json, creating defaults when it is missing.
func OpenStore(dir string) (*PeerStore, error) {
	s := &PeerStore{path: filepath.Join(dir, ConfigFile)}
	err := appdirs.LoadJSON(s.path, &s.cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	fresh := s.cfg.Version == 0 && len(s.cfg.Peers) == 0
	if s.cfg.Settings == (Settings{}) {
		s.cfg.Settings = DefaultSettings()
	}
	// A config written before version 2 has no shakeToLocate key, which would
	// unmarshal as false although the feature defaults to on.
	if s.cfg.Version < 2 && !fresh {
		s.cfg.Settings.ShakeToLocate = true
	}
	migrated := s.cfg.Version != configVersion
	s.cfg.Version = configVersion
	s.cfg.Settings = s.cfg.Settings.Normalize()
	if err != nil || migrated {
		if sErr := s.saveLocked(); sErr != nil {
			return nil, sErr
		}
	}
	return s, nil
}

// Path returns the config file path ("" for a memory store).
func (s *PeerStore) Path() string { return s.path }

func (s *PeerStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	return appdirs.SaveJSON(s.path, s.cfg)
}

// ---------------------------------------------------------------- name

// Name returns the stored machine name ("" = use the default).
func (s *PeerStore) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Name
}

// SetName stores the user-chosen machine name.
func (s *PeerStore) SetName(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Name = strings.TrimSpace(name)
	return s.saveLocked()
}

// ---------------------------------------------------------------- peers

// Trusted implements securechan.TrustStore.
func (s *PeerStore) Trusted(id string) ([32]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.Peers {
		if p.ID == id {
			k, err := ParsePubKey(p.StaticPub)
			if err != nil {
				return [32]byte{}, false
			}
			return k, true
		}
	}
	return [32]byte{}, false
}

// Trust implements securechan.TrustStore: it adds or updates the peer,
// keeping its last address.
func (s *PeerStore) Trust(info securechan.PeerInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if securechan.IDFromPub(info.StaticPub) != info.ID {
		return fmt.Errorf("edgehop: peer %s: id does not match key", info.ID)
	}
	pub := PubKeyB64(info.StaticPub)
	for i, p := range s.cfg.Peers {
		if p.ID == info.ID {
			s.cfg.Peers[i].Name = info.Name
			s.cfg.Peers[i].Plat = info.Plat
			s.cfg.Peers[i].StaticPub = pub
			return s.saveLocked()
		}
	}
	s.cfg.Peers = append(s.cfg.Peers, Peer{ID: info.ID, Name: info.Name, Plat: info.Plat, StaticPub: pub})
	sort.Slice(s.cfg.Peers, func(i, j int) bool { return s.cfg.Peers[i].ID < s.cfg.Peers[j].ID })
	return s.saveLocked()
}

// IsTrusted reports whether id is paired with exactly this key.
func (s *PeerStore) IsTrusted(id string, pub [32]byte) bool {
	k, ok := s.Trusted(id)
	return ok && subtle.ConstantTimeCompare(k[:], pub[:]) == 1
}

// Forget removes a paired peer (and its layout tile).
func (s *PeerStore) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.cfg.Peers[:0]
	for _, p := range s.cfg.Peers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	s.cfg.Peers = out
	s.cfg.Layout = s.cfg.Layout.Without(id)
	return s.saveLocked()
}

// Peer returns the stored record of id.
func (s *PeerStore) Peer(id string) (Peer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

// Peers lists the paired machines sorted by id.
func (s *PeerStore) Peers() []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Peer(nil), s.cfg.Peers...)
}

// SetLastAddr remembers where id was last reached ("host:port").
func (s *PeerStore) SetLastAddr(id, addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.cfg.Peers {
		if p.ID == id {
			if p.LastAddr == addr {
				return nil
			}
			s.cfg.Peers[i].LastAddr = addr
			return s.saveLocked()
		}
	}
	return nil
}

// SetPeerDisplays remembers a peer's monitor arrangement.
func (s *PeerStore) SetPeerDisplays(id string, displays []Display) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.cfg.Peers {
		if p.ID != id {
			continue
		}
		if sameDisplays(p.Displays, displays) {
			return nil
		}
		s.cfg.Peers[i].Displays = append([]Display(nil), displays...)
		return s.saveLocked()
	}
	return nil
}

func sameDisplays(a, b []Display) bool {
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

// UpdatePeerInfo refreshes the name and platform of a paired peer.
func (s *PeerStore) UpdatePeerInfo(id, name, plat string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.cfg.Peers {
		if p.ID == id {
			if p.Name == name && p.Plat == plat {
				return nil
			}
			s.cfg.Peers[i].Name = name
			s.cfg.Peers[i].Plat = plat
			return s.saveLocked()
		}
	}
	return nil
}

// ---------------------------------------------------------------- layout

// Layout returns the stored layout.
func (s *PeerStore) Layout() Layout {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.cfg.Layout
	l.Machines = append([]Placement(nil), l.Machines...)
	l.Tiles = append([]Tile(nil), l.Tiles...)
	return l
}

// SetLayout stores l (after Validate).
func (s *PeerStore) SetLayout(l Layout) error {
	if err := l.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Layout = l.Normalized()
	return s.saveLocked()
}

// ---------------------------------------------------------------- settings

// Settings returns the stored settings.
func (s *PeerStore) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Settings
}

// SetSettings stores the (normalized) settings.
func (s *PeerStore) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Settings = v.Normalize()
	return s.saveLocked()
}
