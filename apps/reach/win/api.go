package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Pairing rules of docs/reach-api.md §1.
const (
	codeTTL       = 120 * time.Second
	maxFailures   = 5
	failureWindow = 10 * time.Minute
	lockoutPeriod = 10 * time.Minute
	maxDeviceName = 64
	maxBody       = 4 << 10
)

// pairer holds the outstanding pairing code and the strike counter.
type pairer struct {
	mu          sync.Mutex
	code        string
	expires     time.Time
	device      string
	failures    []time.Time
	lockedUntil time.Time
	now         func() time.Time
	rand        io.Reader
}

func newPairer() *pairer {
	return &pairer{now: time.Now, rand: rand.Reader}
}

// pairResult is the outcome of redeem.
type pairResult int

const (
	pairOK pairResult = iota
	pairBadCode
	pairLocked
)

// sixDigits draws a uniformly random 6-digit code (never logged).
func sixDigits(r io.Reader) (string, error) {
	var b [4]byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", fmt.Errorf("pairing code: %w", err)
		}
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		// Rejection sampling keeps the distribution uniform.
		if v < 4294000000 {
			return fmt.Sprintf("%06d", v%1000000), nil
		}
	}
}

// locked reports whether pairing is locked out and until when.
func (p *pairer) locked() (bool, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lockedLocked(), p.lockedUntil
}

func (p *pairer) lockedLocked() bool { return p.now().Before(p.lockedUntil) }

// request returns the code to show for device: the outstanding one while it
// is valid (so the laptop's "Show pairing code" and the phone's request
// agree), otherwise a fresh one. ok is false during a lockout.
func (p *pairer) request(device string) (code string, expires time.Time, ok bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lockedLocked() {
		return "", p.lockedUntil, false, nil
	}
	now := p.now()
	if p.code == "" || !now.Before(p.expires) {
		c, err := sixDigits(p.rand)
		if err != nil {
			return "", time.Time{}, false, err
		}
		p.code = c
		p.expires = now.Add(codeTTL)
	}
	if device != "" {
		p.device = device
	}
	return p.code, p.expires, true, nil
}

// redeem checks code. A wrong, missing or expired code is a strike; the
// fifth strike within failureWindow starts a lockout. A right code is
// consumed.
func (p *pairer) redeem(code string) pairResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.lockedLocked() {
		return pairLocked
	}
	if p.code != "" && now.Before(p.expires) && code == p.code {
		p.code, p.expires, p.device, p.failures = "", time.Time{}, "", nil
		return pairOK
	}
	kept := p.failures[:0]
	for _, t := range p.failures {
		if now.Sub(t) < failureWindow {
			kept = append(kept, t)
		}
	}
	p.failures = append(kept, now)
	if len(p.failures) >= maxFailures {
		p.lockedUntil = now.Add(lockoutPeriod)
		p.failures = nil
		p.code, p.expires = "", time.Time{}
		return pairLocked
	}
	return pairBadCode
}

// apiMux serves /api/info, /api/peers, /api/pair/request and /api/pair
// (docs/reach-api.md §1). None of them needs a token; httpserve adds CORS.
func (a *agent) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET")
			return
		}
		writeJSON(w, http.StatusOK, a.infoPublic())
	})
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET")
			return
		}
		writeJSON(w, http.StatusOK, a.disc.list())
	})
	mux.HandleFunc("/api/pair/request", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var body struct {
			Device string `json:"device"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		device := cleanDevice(body.Device)
		if device == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		code, expires, ok, err := a.pair.request(device)
		if err != nil {
			a.logf("pair request: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "locked", "retryAfterSec": int(time.Until(expires).Seconds())})
			return
		}
		a.logf("pairing requested by %q", device)
		a.showCode(code, expires, device)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/pair", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		var body struct {
			Device string `json:"device"`
			Code   string `json:"code"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		device := cleanDevice(body.Device)
		if device == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		switch a.pair.redeem(strings.TrimSpace(body.Code)) {
		case pairLocked:
			_, until := a.pair.locked()
			a.logf("pairing locked after repeated wrong codes (device %q)", device)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "locked", "retryAfterSec": int(time.Until(until).Seconds())})
		case pairBadCode:
			a.logf("wrong pairing code from %q", device)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "bad_code"})
		case pairOK:
			token, err := a.cfg.AddPairing(device)
			if err != nil {
				a.logf("pair: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
				return
			}
			a.logf("paired %q", device)
			a.emitLocal("reach.paired", map[string]any{"device": device})
			if a.tray != nil {
				a.tray.Notify("Reach", device+" is now paired")
			}
			writeJSON(w, http.StatusOK, map[string]string{"token": token})
		}
	})
	return mux
}

// cleanDevice trims and bounds a device name.
func cleanDevice(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > maxDeviceName {
		s = s[:maxDeviceName]
	}
	return s
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil || json.Unmarshal(body, v) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
