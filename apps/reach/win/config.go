package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/appdirs"
)

// DefaultPort is the port of docs/reach-api.md §1.
const DefaultPort = 7788

// Pairing is one phone that may open /ws. The token is the secret.
type Pairing struct {
	Device    string    `json:"device"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Config is config.json (docs/ARCHITECTURE.md §8): %APPDATA%\Reach\config.json
// on Windows, $XDG_CONFIG_HOME/reach/config.json on Linux. Written 0600.
type Config struct {
	Name          string    `json:"name"`
	Port          int       `json:"port"`
	AgentID       string    `json:"agentId"`
	Pairings      []Pairing `json:"pairings"`
	LaunchAtLogin bool      `json:"launchAtLogin"`
}

// PublicPairing is a pairing without its token (reach.state).
type PublicPairing struct {
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

// store keeps the config in memory and on disk. All methods are safe for
// concurrent use; every mutation is saved immediately.
type store struct {
	mu   sync.Mutex
	path string
	cfg  Config
	now  func() time.Time
}

// configPath returns <dir>/config.json, creating dir when it is the default.
func configPath(dir string) (string, error) {
	if dir == "" {
		d, err := appdirs.ConfigDir("Reach")
		if err != nil {
			return "", err
		}
		dir = d
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return dir + string(os.PathSeparator) + "config.json", nil
}

// loadStore reads path (a missing file yields defaults) and fills in the
// agent id, name and port when they are absent, saving the result.
func loadStore(path, defaultName string) (*store, error) {
	s := &store{path: path, now: time.Now}
	if err := appdirs.LoadJSON(path, &s.cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	changed := false
	if s.cfg.AgentID == "" {
		id, err := newAgentID()
		if err != nil {
			return nil, err
		}
		s.cfg.AgentID = id
		changed = true
	}
	if s.cfg.Name == "" {
		s.cfg.Name = defaultName
		changed = true
	}
	if s.cfg.Port <= 0 || s.cfg.Port > 65535 {
		s.cfg.Port = DefaultPort
		changed = true
	}
	if s.cfg.Pairings == nil {
		s.cfg.Pairings = []Pairing{}
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func newAgentID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newToken returns 32 random bytes as unpadded base64url (docs/reach-api.md §1).
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (s *store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := appdirs.SaveJSON(s.path, s.cfg); err != nil {
		return fmt.Errorf("save %s: %w", s.path, err)
	}
	return nil
}

// Snapshot returns a copy of the config.
func (s *store) Snapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Pairings = append([]Pairing(nil), s.cfg.Pairings...)
	return c
}

// Update applies fn under the lock and saves.
func (s *store) Update(fn func(c *Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	return s.saveLocked()
}

// AddPairing stores a new token for device, replacing an older pairing of
// the same device name, and returns the token.
func (s *store) AddPairing(device string) (string, error) {
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	kept := s.cfg.Pairings[:0]
	for _, p := range s.cfg.Pairings {
		if p.Device != device {
			kept = append(kept, p)
		}
	}
	s.cfg.Pairings = append(kept, Pairing{Device: device, Token: tok, CreatedAt: now, LastSeen: now})
	return tok, s.saveLocked()
}

// Revoke removes every pairing of device and returns their tokens.
func (s *store) Revoke(device string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	kept := s.cfg.Pairings[:0]
	for _, p := range s.cfg.Pairings {
		if p.Device == device {
			tokens = append(tokens, p.Token)
		} else {
			kept = append(kept, p)
		}
	}
	s.cfg.Pairings = kept
	if len(tokens) == 0 {
		return nil, nil
	}
	return tokens, s.saveLocked()
}

// FindToken looks a phone token up in constant time per entry and returns
// the pairing.
func (s *store) FindToken(token string) (Pairing, bool) {
	if token == "" {
		return Pairing{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.cfg.Pairings {
		if subtle.ConstantTimeCompare([]byte(p.Token), []byte(token)) == 1 {
			return p, true
		}
	}
	return Pairing{}, false
}

// Touch updates LastSeen of the pairing holding token (best effort).
func (s *store) Touch(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Pairings {
		if subtle.ConstantTimeCompare([]byte(s.cfg.Pairings[i].Token), []byte(token)) == 1 {
			s.cfg.Pairings[i].LastSeen = s.now()
			_ = s.saveLocked()
			return
		}
	}
}

// PublicPairings lists the pairings without tokens, newest first.
func (s *store) PublicPairings() []PublicPairing {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PublicPairing, 0, len(s.cfg.Pairings))
	for _, p := range s.cfg.Pairings {
		out = append(out, PublicPairing{Device: p.Device, CreatedAt: p.CreatedAt, LastSeen: p.LastSeen})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
