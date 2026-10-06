// Vitals for Windows: a Task-Manager-style monitor (CPU, memory, GPU,
// processes, network, disk, battery) that also configures power behaviour.
// The exe is a tray app whose UI (apps/vitals/ui) runs in a WebView2 window
// and talks to this process over the bridge (docs/ARCHITECTURE.md §4–5).
//
// The same program builds on Linux as the test backend: real /proc stats,
// an honest "unsupported" power backend or, with --fake-power, an in-memory
// laptop model so the Playwright tests can exercise the Power page.
//
//	vitals [--listen 127.0.0.1:0] [--no-ui] [--debug] [--fake-power] [--interval 1s]
//	vitals --power-action sleep        (run one action and exit; used by scheduled tasks)
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/r-thirumaran/mac-utility/apps/vitals/ui"
	"github.com/r-thirumaran/mac-utility/shared/go/applog"
	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
	"github.com/r-thirumaran/mac-utility/shared/go/webhost"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
	sharedui "github.com/r-thirumaran/mac-utility/shared/ui"
)

// version is set by the Makefile: -ldflags "-X main.version=$(cat apps/vitals/VERSION)".
var version = "dev"

//go:embed icon.ico
var iconICO []byte

const (
	appName      = "Vitals"
	instanceName = "com.thiru.vitals"
	windowWidth  = 1040
	windowHeight = 720
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

// staticFS composes the UI: the app's own files at the root and the shared
// kit at /shared/ (index.html references ./shared/base.css).
func staticFS() fs.FS {
	return newOverlayFS(ui.Static, map[string]fs.FS{"shared": sharedui.FS})
}

// run is main without os.Exit so it can be driven from tests.
func run(args []string, stdout io.Writer) int {
	fl := flag.NewFlagSet("vitals", flag.ContinueOnError)
	fl.SetOutput(os.Stderr)
	listen := fl.String("listen", "", "bind address (default 127.0.0.1 with a random port)")
	noUI := fl.Bool("no-ui", false, "serve only: print the URL, no tray, window or browser")
	debug := fl.Bool("debug", false, "verbose logging")
	fakePower := fl.Bool("fake-power", false, "simulate a laptop's power settings (tests)")
	interval := fl.Duration("interval", time.Second, "default sampling interval")
	powerAction := fl.String("power-action", "", "run one power action (sleep|displayOff|lock|restart|shutdown) and exit")
	showVersion := fl.Bool("version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "%s %s\n", appName, version)
		return 0
	}

	logger, closeLog, err := applog.Open(appName)
	if err != nil {
		logger = log.New(os.Stderr, "", log.LstdFlags)
		closeLog = func() error { return nil }
		logger.Printf("log file unavailable: %v", err)
	}
	defer closeLog()
	logger.Printf("%s %s starting (%s/%s, pid %d)", appName, version, runtime.GOOS, runtime.GOARCH, os.Getpid())

	// A scheduled task ("Vitals\<id>" → vitals --power-action sleep) runs
	// one action without touching the running instance.
	if *powerAction != "" {
		if err := newPower(*fakePower).Action(*powerAction); err != nil {
			logger.Printf("power action %s: %v", *powerAction, err)
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		return 0
	}

	// One instance per user on Windows. A relaunch (power.relaunchElevated)
	// starts while the old instance is still exiting, so wait a little.
	if runtime.GOOS == "windows" {
		release, first := acquireInstance(instanceName, 3*time.Second, logger)
		if !first {
			logger.Printf("another instance is running; exiting")
			return 0
		}
		defer release()
	}

	stats, err := newStats()
	if err != nil {
		logger.Printf("stats: %v", err)
		fmt.Fprintf(os.Stderr, "vitals: %v\n", err)
		return 1
	}
	defer stats.Close()
	power := newPower(*fakePower)
	if *fakePower {
		logger.Printf("using the simulated power backend")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	a := newApp(appOptions{
		Stats: stats, Power: power, FakePower: *fakePower, Log: logger, Debug: *debug,
		Version: version, Interval: *interval, Quit: cancel,
	})
	d := bridge.New()
	a.register(d)
	defer a.sampler.Close()

	menu := newTrayMenu(logger)
	err = webhost.Run(ctx, webhost.Config{
		Title:       appName,
		Width:       windowWidth,
		Height:      windowHeight,
		IconICO:     iconICO,
		Static:      staticFS(),
		Dispatcher:  d,
		ShowOnStart: true,
		Listen:      *listen,
		NoUI:        *noUI,
		Tray:        tray.Config{Tooltip: appName, IconICO: iconICO, Items: menu.items()},
		OnTray:      menu.attach,
		OnListen: func(url string) {
			logger.Printf("listening on %s", strings.SplitN(url, "?", 2)[0])
		},
		Stdout: stdout,
	})
	if err != nil {
		logger.Printf("webhost: %v", err)
		fmt.Fprintf(os.Stderr, "vitals: %v\n", err)
		return 1
	}
	logger.Printf("exiting")
	return 0
}

// acquireInstance takes the single-instance lock, retrying until timeout so
// an elevated relaunch can take over from the instance that is exiting.
func acquireInstance(name string, timeout time.Duration, logger *log.Logger) (release func(), first bool) {
	deadline := time.Now().Add(timeout)
	for {
		rel, first, err := winutil.SingleInstance(name)
		if err != nil {
			logger.Printf("single instance: %v (continuing)", err)
			return func() {}, true
		}
		if first {
			return rel, true
		}
		rel()
		if time.Now().After(deadline) {
			return func() {}, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// trayMenu owns the app's tray items ("Open Vitals" and "Quit" are added by
// webhost around them).
type trayMenu struct {
	log  *log.Logger
	tray *tray.Tray
}

const autostartTitle = "Launch at login"

func newTrayMenu(logger *log.Logger) *trayMenu { return &trayMenu{log: logger} }

func (m *trayMenu) items() []tray.Item {
	return []tray.Item{{
		Title:    autostartTitle,
		Checked:  winutil.Autostart(appName),
		Disabled: runtime.GOOS != "windows",
		OnClick:  m.toggleAutostart,
	}}
}

func (m *trayMenu) attach(t *tray.Tray) { m.tray = t }

// toggleAutostart flips the HKCU Run entry and refreshes the check mark.
func (m *trayMenu) toggleAutostart() {
	exe, err := os.Executable()
	if err != nil {
		m.log.Printf("autostart: executable path: %v", err)
		return
	}
	enable := !winutil.Autostart(appName)
	if err := winutil.SetAutostart(appName, exe, enable); err != nil {
		m.log.Printf("autostart %v: %v", enable, err)
		return
	}
	m.log.Printf("launch at login: %v", enable)
	if m.tray == nil {
		return
	}
	items := m.tray.Items()
	for i := range items {
		if items[i].Title == autostartTitle {
			items[i].Checked = enable
		}
	}
	m.tray.SetItems(items)
}
