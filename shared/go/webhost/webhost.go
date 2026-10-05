// Package webhost runs the desktop side of a Windows/Linux app: it serves the
// embedded web UI and the bridge WebSocket on a loopback (or LAN) port behind
// a per-run token, shows a tray icon with "Open <Title>" / "Quit", and on
// Windows opens the UI in a WebView2 window (or the default browser when the
// WebView2 runtime is missing). The page always talks to the backend over the
// WebSocket, so the Windows and Linux code paths are identical apart from the
// window itself.
//
// Run prints exactly one line to Config.Stdout (default os.Stdout):
//
//	listening on http://127.0.0.1:<port>/?token=<token>
//
// The UI tests parse that line. The token must be presented as ?token= on /ws
// and on the first GET /, which sets the mu_token cookie so later navigations
// and static assets pass without the query string. /api/ paths are not gated
// by the token (Reach guards them itself).
//
// On Windows, Run must be called from the main goroutine (the tray library
// owns the main thread's message loop).
package webhost

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/httpserve"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
)

// CookieName is the cookie that carries the token after the first GET /.
const CookieName = "mu_token"

// Config describes the app window, server and tray.
type Config struct {
	// Title is the window title and the default tray tooltip ("Vitals").
	Title string
	// Width and Height are the window size in pixels (0 = 1000×700).
	Width, Height int
	// IconICO is the window and tray icon in .ico format.
	IconICO []byte
	// Static is the UI (index.html at its root).
	Static fs.FS
	// Dispatcher serves the bridge methods and emits events.
	Dispatcher *bridge.Dispatcher
	// API is mounted at /api/ (may be nil).
	API *http.ServeMux
	// ShowOnStart opens the window immediately (Windows). Otherwise only the
	// tray icon appears until the user clicks "Open".
	ShowOnStart bool
	// Listen is the bind address; "" = 127.0.0.1 with a random port. Apps that
	// must be reachable from the LAN (Reach) pass "0.0.0.0:7788".
	Listen string
	// AllowLAN enables cross-origin access (CORS on /api/, any origin on /ws)
	// for browsers and phones on other hosts.
	AllowLAN bool
	// Tray supplies the tooltip, icon and the app's own menu items, which are
	// placed between "Open <Title>" and "Quit".
	Tray tray.Config
	// OnTray receives the running tray once it exists (also on Linux, where
	// the tray is a no-op) so the app can update items later.
	OnTray func(*tray.Tray)
	// NoUI serves only: no tray, no window, no browser. Used by tests and the
	// --no-ui flag.
	NoUI bool
	// Token guards /ws and the page; "" generates a random 32-byte base64url token.
	Token string
	// Auth, when set, is an additional allow rule consulted after the token
	// rule fails (for example Reach's phone pairing tokens on /ws).
	Auth func(r *http.Request) bool
	// OnListen is called with the local URL (the printed one) once the server
	// is up, so apps and tests need not parse stdout.
	OnListen func(url string)
	// Stdout receives the "listening on" line; nil means os.Stdout.
	Stdout io.Writer
}

// NewToken returns 32 random bytes as unpadded base64url (43 characters).
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webhost: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Run serves the UI until ctx is cancelled or the user quits from the tray or
// window, then shuts the server down. It returns nil on a normal exit and an
// error when the server cannot start.
func Run(ctx context.Context, c Config) error {
	if c.Title == "" {
		c.Title = "App"
	}
	if c.Dispatcher == nil {
		c.Dispatcher = bridge.New()
	}
	token := c.Token
	if token == "" {
		t, err := NewToken()
		if err != nil {
			return err
		}
		token = t
	}
	inner := httpserve.New(httpserve.Options{
		Static:     c.Static,
		Dispatcher: c.Dispatcher,
		Auth:       tokenAuth(token, c.Auth),
		API:        c.API,
		AllowCORS:  c.AllowLAN,
	})
	addr := c.Listen
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	bound, srv, err := httpserve.Listen(addr, cookieSetter(token, inner))
	if err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(bound)
	if err != nil {
		srv.Close()
		return fmt.Errorf("webhost: bound address %q: %w", bound, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%s/?token=%s", port, token)
	out := c.Stdout
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "listening on %s\n", url)
	if c.OnListen != nil {
		c.OnListen(url)
	}

	runUI(ctx, c, url)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("webhost: shutdown: %w", err)
	}
	return nil
}

// runUI shows the tray and window (unless NoUI) and returns when the app
// should exit: ctx cancelled, or "Quit" clicked.
func runUI(ctx context.Context, c Config, url string) {
	if c.NoUI {
		<-ctx.Done()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	win := newWindow(ctx, c, url)
	tray.Run(trayConfig(c, win.open, cancel), func(t *tray.Tray) {
		win.onReady()
		if c.OnTray != nil {
			c.OnTray(t)
		}
		go func() {
			<-ctx.Done()
			t.Quit()
		}()
	})
	cancel()
	win.close()
}

// trayConfig wraps the app's items with "Open <Title>" and "Quit".
func trayConfig(c Config, open, quit func()) tray.Config {
	cfg := c.Tray
	if cfg.Tooltip == "" {
		cfg.Tooltip = c.Title
	}
	if cfg.IconICO == nil {
		cfg.IconICO = c.IconICO
	}
	items := []tray.Item{{Title: "Open " + c.Title, OnClick: open}, {Separator: true}}
	if len(c.Tray.Items) > 0 {
		items = append(items, c.Tray.Items...)
		items = append(items, tray.Item{Separator: true})
	}
	items = append(items, tray.Item{Title: "Quit", OnClick: quit})
	cfg.Items = items
	return cfg
}

func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// tokenAuth allows /api/ freely and everything else with the token in the
// query string or the cookie, or when extra accepts the request.
func tokenAuth(token string, extra func(*http.Request) bool) func(*http.Request) bool {
	return func(r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			return true
		}
		if q := r.URL.Query().Get("token"); q != "" && tokenEqual(q, token) {
			return true
		}
		if ck, err := r.Cookie(CookieName); err == nil && tokenEqual(ck.Value, token) {
			return true
		}
		return extra != nil && extra(r)
	}
}

// cookieSetter sets the token cookie on a GET / that carries the right token.
func cookieSetter(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			if q := r.URL.Query().Get("token"); q != "" && tokenEqual(q, token) {
				if ck, err := r.Cookie(CookieName); err != nil || ck.Value != token {
					http.SetCookie(w, &http.Cookie{
						Name: CookieName, Value: token, Path: "/",
						HttpOnly: true, SameSite: http.SameSiteStrictMode,
					})
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
