// Edgehop for Windows (and the Linux test build): one mouse and keyboard
// across Macs and PCs. A tray app with an embedded web UI; the state machine
// lives in apps/edgehop/core, the hooks in apps/edgehop/input.
//
//	edgehop [--listen 127.0.0.1:0] [--no-ui] [--name "Desk PC"] [--port 24800]
//	        [--fake-input] [--fake-displays 1920x1080;2560x1440+1920+0]
//	        [--config-dir DIR] [--peer host:port] [--debug]
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"log"

	"github.com/r-thirumaran/mac-utility/apps/edgehop/core"
	"github.com/r-thirumaran/mac-utility/apps/edgehop/input"
	"github.com/r-thirumaran/mac-utility/apps/edgehop/ui"
	"github.com/r-thirumaran/mac-utility/shared/go/applog"
	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
	"github.com/r-thirumaran/mac-utility/shared/go/tray"
	"github.com/r-thirumaran/mac-utility/shared/go/webhost"
	"github.com/r-thirumaran/mac-utility/shared/go/winutil"
	sharedui "github.com/r-thirumaran/mac-utility/shared/ui"
)

// version is set by the Makefile (-X main.version=$(cat apps/edgehop/VERSION)).
var version = "dev"

//go:embed icon.ico
var iconICO []byte

// staticFS composes the Edgehop UI with the shared kit mounted at /shared/.
func staticFS() fs.FS {
	return newOverlayFS(ui.Static, map[string]fs.FS{"shared": sharedui.FS})
}

func main() {
	input.SetDPIAware() // before any window or hook exists
	var (
		listen       = flag.String("listen", "", "UI server address (default 127.0.0.1:0)")
		noUI         = flag.Bool("no-ui", false, "serve the UI only; no tray, window or browser")
		name         = flag.String("name", "", "this machine's display name (stored)")
		port         = flag.Int("port", -1, "TCP port for peers (0 = any free port; default from settings, 24800)")
		fakeInput    = flag.Bool("fake-input", runtime.GOOS != "windows", "in-memory input device instead of the OS hooks")
		fakeDisplays = flag.String("fake-displays", "", "fake monitors, e.g. 1920x1080;2560x1440+1920+0")
		configDir    = flag.String("config-dir", "", "config directory (default per OS)")
		peer         = flag.String("peer", "", "host:port of a machine to pair with at start")
		noMDNS       = flag.Bool("no-mdns", false, "do not advertise or browse Bonjour (tests)")
		debug        = flag.Bool("debug", false, "log debug lines")
		showVersion  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println("Edgehop", version)
		return
	}
	opts := options{listen: *listen, noUI: *noUI, name: *name, port: *port, fakeInput: *fakeInput, fakeDisplays: *fakeDisplays, configDir: *configDir, peer: *peer, debug: *debug, noMDNS: *noMDNS}
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "edgehop:", err)
		os.Exit(1)
	}
}

// options are the command-line flags.
type options struct {
	listen, name, fakeDisplays, configDir, peer string
	port                                        int
	noUI, fakeInput, debug, noMDNS              bool
}

func run(o options) error {
	listen, noUI, name, port, fakeInput, fakeDisplays, configDir, peer, debug := o.listen, o.noUI, o.name, o.port, o.fakeInput, o.fakeDisplays, o.configDir, o.peer, o.debug
	dir, err := core.ConfigDir(configDir)
	if err != nil {
		return err
	}
	// One instance per config dir: tests run several on one box.
	instance := "com.thiru.edgehop"
	if configDir != "" {
		h := fnv.New32a()
		_, _ = h.Write([]byte(dir))
		instance += "." + strconv.FormatUint(uint64(h.Sum32()), 16)
	}
	release, first, err := winutil.SingleInstance(instance)
	if err == nil && !first {
		return fmt.Errorf("Edgehop is already running")
	}
	if err == nil {
		defer release()
	}

	logger := applogOrStderr(dir, configDir != "")
	logger.Printf("Edgehop %s starting (%s/%s)", version, runtime.GOOS, runtime.GOARCH)

	store, err := core.OpenStore(dir)
	if err != nil {
		return err
	}
	self, err := core.LoadIdentity(dir)
	if err != nil {
		return err
	}
	if name != "" {
		if err := store.SetName(name); err != nil {
			return err
		}
	}
	machineName := store.Name()
	if machineName == "" {
		machineName = core.DefaultMachineName()
	}
	settings := store.Settings()
	if port < 0 {
		port = settings.Port
	}

	var dev input.Device
	var fake *input.FakeDevice
	if fakeInput {
		displays, err := parseFakeDisplays(fakeDisplays)
		if err != nil {
			return err
		}
		fake = input.NewFake(displays...)
		dev = fake
	} else {
		dev, err = input.NewNative()
		if err != nil {
			return err
		}
	}

	exe, _ := os.Executable()
	d := bridge.New()
	a := &app{store: store, fake: fake, d: d, logger: logger, version: version, exe: exe, debug: debug}
	a.tr = core.NewTransport(core.TransportConfig{
		Self: self, Name: machineName, Plat: core.Platform(), Store: store,
		Port: port, DisableMDNS: o.noMDNS, Emit: a.handleEvent, Logf: a.nodeLogf,
	})
	a.node = core.New(core.Config{
		ID: self.ID(), Name: machineName, Plat: core.Platform(), Version: version,
		Store: store, Capturer: dev, Injector: dev, Transport: a.tr, Clipboard: newClipboard(),
		TickInterval: time.Second, Logf: a.nodeLogf,
	})
	a.tr.Bind(a.node)
	a.register()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	a.quit = cancel

	if err := a.tr.Start(ctx); err != nil {
		return err
	}
	defer a.tr.Stop()
	a.node.SetPort(a.tr.Port())
	if err := a.node.Start(); err != nil {
		return fmt.Errorf("input capture: %w", err)
	}
	defer a.node.Stop()
	go a.pump()
	if peer != "" {
		go func() {
			time.Sleep(200 * time.Millisecond)
			if err := a.tr.PairStart("", peer); err != nil {
				a.logf("warn", "pair with %s: %v", peer, err)
			}
		}()
	}

	err = webhost.Run(ctx, webhost.Config{
		Title: "Edgehop", IconICO: iconICO, Static: staticFS(), Dispatcher: d,
		Width: 1000, Height: 720, ShowOnStart: false, Listen: listen, NoUI: noUI,
		Tray:   tray.Config{Tooltip: "Edgehop — " + machineName, Items: a.trayItems()},
		OnTray: a.setTray,
	})
	logger.Printf("Edgehop stopping")
	return err
}

// applogOrStderr opens the rotating log in the per-OS log dir, or next to
// the config when --config-dir was given (tests), falling back to stderr.
func applogOrStderr(dir string, inConfigDir bool) *log.Logger {
	var (
		l   *log.Logger
		err error
	)
	if inConfigDir {
		l, _, err = applog.OpenFile(filepath.Join(dir, "edgehop.log"), applog.MaxSize)
	} else {
		l, _, err = applog.Open(core.AppName)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "edgehop: log:", err)
		return log.New(os.Stderr, "", log.LstdFlags)
	}
	return l
}

// parseFakeDisplays parses "WxH[+X+Y][@scale][*]" entries separated by ';'
// (a trailing '*' marks the primary; the first entry is primary by default).
func parseFakeDisplays(spec string) ([]input.Display, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var out []input.Display
	for i, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		d := input.Display{ID: "fake-" + strconv.Itoa(i+1), Scale: 1}
		if strings.HasSuffix(part, "*") {
			d.Primary = true
			part = strings.TrimSuffix(part, "*")
		}
		if at := strings.IndexByte(part, '@'); at >= 0 {
			sc, err := strconv.ParseFloat(part[at+1:], 64)
			if err != nil || sc <= 0 {
				return nil, fmt.Errorf("fake-displays: bad scale in %q", part)
			}
			d.Scale = sc
			part = part[:at]
		}
		size := part
		if plus := strings.IndexByte(part, '+'); plus >= 0 {
			size = part[:plus]
			pos := strings.Split(part[plus+1:], "+")
			if len(pos) != 2 {
				return nil, fmt.Errorf("fake-displays: bad position in %q", part)
			}
			x, errX := strconv.Atoi(pos[0])
			y, errY := strconv.Atoi(pos[1])
			if errX != nil || errY != nil {
				return nil, fmt.Errorf("fake-displays: bad position in %q", part)
			}
			d.X, d.Y = x, y
		}
		w, h, ok := strings.Cut(strings.ToLower(size), "x")
		if !ok {
			return nil, fmt.Errorf("fake-displays: bad size in %q", part)
		}
		var errW, errH error
		d.W, errW = strconv.Atoi(w)
		d.H, errH = strconv.Atoi(h)
		if errW != nil || errH != nil || d.W <= 0 || d.H <= 0 {
			return nil, fmt.Errorf("fake-displays: bad size in %q", part)
		}
		out = append(out, d)
	}
	hasPrimary := false
	for _, d := range out {
		hasPrimary = hasPrimary || d.Primary
	}
	if !hasPrimary && len(out) > 0 {
		out[0].Primary = true
	}
	return out, nil
}
