// Reach agent for Windows (and its Linux test build): serves the phone app
// on the LAN, answers the REST pairing API and drives the laptop over the
// WebSocket bridge (docs/reach-api.md). The same binary hosts the agent's
// own settings page in a WebView2 window through shared/go/webhost.
//
//	reach [--listen 0.0.0.0:7788] [--port 7788] [--name "My laptop"] [--config-dir DIR]
//	      [--no-ui] [--fake] [--debug]
//
// --fake (the default off Windows) uses the recording backend of
// backend_fake.go instead of injecting input.
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/reach/ui"
	"github.com/r-thirumaran/mac-utility/shared/go/applog"
	"github.com/r-thirumaran/mac-utility/shared/go/httpserve"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
	"github.com/r-thirumaran/mac-utility/shared/go/webhost"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
	sharedui "github.com/r-thirumaran/mac-utility/shared/ui"
)

// version is set by the Makefile (-X main.version=$(cat apps/reach/VERSION)).
var version = "dev"

//go:embed icon.ico
var iconICO []byte

type options struct {
	listen    string
	noUI      bool
	name      string
	port      int
	configDir string
	fake      bool
	debug     bool
	exitWith  bool
}

func main() {
	var o options
	flag.StringVar(&o.listen, "listen", "", "bind address for phones (default 0.0.0.0:<port>, port from --port or the config, 7788 at first)")
	flag.BoolVar(&o.noUI, "no-ui", false, "serve only: no tray, no window, no browser")
	flag.StringVar(&o.name, "name", "", "display name shown to phones (saved)")
	flag.IntVar(&o.port, "port", 0, "LAN port (saved)")
	flag.StringVar(&o.configDir, "config-dir", "", "directory of config.json (default the per-user config dir)")
	flag.BoolVar(&o.fake, "fake", runtime.GOOS != "windows", "use the recording fake backend instead of the OS (default on non-Windows)")
	flag.BoolVar(&o.debug, "debug", false, "log every bridge call")
	flag.BoolVar(&o.exitWith, "exit-with-parent", false, "exit when the parent process goes away (test harnesses that run the agent through `go run`)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("Reach", version)
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "reach:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	cfgPath, err := configPath(o.configDir)
	if err != nil {
		return err
	}
	logger, closeLog := openLog(o.configDir)
	defer closeLog()

	release, first, err := winutil.SingleInstance("com.thiru.reach-" + shortHash(cfgPath))
	if err != nil {
		logger.Printf("single instance: %v", err)
	} else if !first {
		logger.Printf("another Reach is already running with %s; exiting", cfgPath)
		fmt.Fprintln(os.Stderr, "reach: already running")
		return nil
	} else {
		defer release()
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "Laptop"
	}
	cfg, err := loadStore(cfgPath, host)
	if err != nil {
		return err
	}
	if o.name != "" || (o.port > 0 && o.port <= 65535) {
		if err := cfg.Update(func(c *Config) {
			if o.name != "" {
				c.Name = o.name
			}
			if o.port > 0 && o.port <= 65535 {
				c.Port = o.port
			}
		}); err != nil {
			return err
		}
	}
	snap := cfg.Snapshot()
	listen := o.listen
	if listen == "" {
		listen = "0.0.0.0:" + strconv.Itoa(snap.Port)
	}

	var backend Backend
	if o.fake {
		backend = newFakeBackend()
	} else {
		b, err := newRealBackend()
		if err != nil {
			return err
		}
		backend = b
	}

	localToken, err := webhost.NewToken()
	if err != nil {
		return err
	}
	a := newAgent(cfg, backend, localToken, version, logger, o.fake, o.debug)
	static := newOverlayFS(ui.Static, map[string]fs.FS{"shared": sharedui.FS})

	// The LAN server: PWA + /api/ + /ws for phones and for the local settings page.
	h := httpserve.New(httpserve.Options{
		Static: static, Dispatcher: a.dLAN, Auth: a.authorize, API: a.apiMux(), AllowCORS: true, OnWS: a.onWS,
	})
	bound, srv, err := httpserve.Listen(listen, a.cookieSetter(h))
	if err != nil {
		return err
	}
	_, portStr, err := net.SplitHostPort(bound)
	if err != nil {
		srv.Close()
		return fmt.Errorf("bound address %q: %w", bound, err)
	}
	a.port, _ = strconv.Atoi(portStr)
	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", a.port, localToken)
	fmt.Printf("listening on %s\n", url)
	logger.Printf("Reach %s (%s backend) listening on %s as %q, config %s", version, backendKind(o.fake), bound, snap.Name, cfgPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a.quit = cancel
	a.noUI = o.noUI
	a.startDiscovery(ctx)
	go a.runningWatcher(ctx)
	if o.exitWith {
		go exitWithParent(ctx, cancel, logger)
	}

	// webhost owns the tray and (on Windows) the WebView2 window; its own
	// loopback server reuses the local token and the same methods.
	err = webhost.Run(ctx, webhost.Config{
		Title: "Reach", IconICO: iconICO, Static: static, Dispatcher: a.dLocal,
		NoUI: o.noUI, Token: localToken, Stdout: io.Discard,
		Tray:   tray.Config{Tooltip: "Reach — " + snap.Name, Items: a.trayItems()},
		OnTray: a.setTray,
	})
	cancel()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	if sErr := srv.Shutdown(shutdownCtx); sErr != nil && !errors.Is(sErr, context.DeadlineExceeded) && !errors.Is(sErr, http.ErrServerClosed) {
		logger.Printf("shutdown: %v", sErr)
	}
	logger.Printf("stopped")
	return err
}

// openLog opens the rotating log (next to config.json when --config-dir is
// given, the per-user log dir otherwise) and falls back to stderr.
func openLog(configDir string) (*log.Logger, func()) {
	var (
		logger *log.Logger
		closer func() error
		err    error
	)
	if configDir != "" {
		logger, closer, err = applog.OpenFile(filepath.Join(configDir, "reach.log"), applog.MaxSize)
	} else {
		logger, closer, err = applog.Open("Reach")
	}
	if err != nil {
		l := log.New(os.Stderr, "", log.LstdFlags)
		l.Printf("log file unavailable (%v); logging to stderr", err)
		return l, func() {}
	}
	return logger, func() { _ = closer() }
}

// exitWithParent cancels the run when the parent process disappears: `go run`
// (which the UI tests use) does not forward SIGTERM to the binary it built,
// so the agent would otherwise outlive the test harness. A reparented
// process sees a different parent pid.
func exitWithParent(ctx context.Context, cancel context.CancelFunc, logger *log.Logger) {
	parent := os.Getppid()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if os.Getppid() != parent {
				logger.Printf("parent process %d went away; exiting", parent)
				cancel()
				return
			}
		}
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

func backendKind(fake bool) string {
	if fake {
		return "fake"
	}
	return "windows"
}
