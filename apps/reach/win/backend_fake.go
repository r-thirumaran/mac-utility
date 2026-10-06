package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/r-thirumaran/mac-utility/shared/go/bridge"
)

// fakeBackend implements Backend without touching the OS. It records every
// injection in a ring buffer (reach.debug.actions), renders a synthetic
// 16:9 frame with a timestamp and a marker at the pointer, lists the
// .desktop files of /usr/share/applications (a fixed set when there are
// none) and keeps a fixed running list that focus/quit mutate. It is the
// Linux test backend and the --fake mode of every platform.
type fakeBackend struct {
	mu            sync.Mutex
	actions       []Action
	start         time.Time
	cx, cy        float64
	down          map[string]bool
	clipboard     string
	volume        int
	muted         bool
	launchAtLogin bool
	running       []RunningApp
	appsDir       string
	now           func() time.Time
}

const (
	fakeW         = 1280
	fakeH         = 720
	maxActions    = 512
	fakeAppsDir   = "/usr/share/applications"
	fakePIDBase   = 4200
	fakeMaxActive = 3
)

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		start:   time.Now(),
		cx:      fakeW / 2,
		cy:      fakeH / 2,
		down:    map[string]bool{},
		volume:  50,
		appsDir: fakeAppsDir,
		now:     time.Now,
		running: []RunningApp{
			{ID: "fake:editor", Name: "Editor", PID: fakePIDBase + 42, Active: true},
			{ID: "fake:browser", Name: "Browser", PID: fakePIDBase + 43},
			{ID: "fake:terminal", Name: "Terminal", PID: fakePIDBase + 44},
		},
	}
}

func (f *fakeBackend) record(kind string, kv ...any) {
	a := Action{"kind": kind, "t": f.now().UnixMilli()}
	for i := 0; i+1 < len(kv); i += 2 {
		a[kv[i].(string)] = kv[i+1]
	}
	f.mu.Lock()
	f.actions = append(f.actions, a)
	if len(f.actions) > maxActions {
		f.actions = f.actions[len(f.actions)-maxActions:]
	}
	f.mu.Unlock()
}

// Actions returns the recorded injections, oldest first.
func (f *fakeBackend) Actions() []Action {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Action(nil), f.actions...)
}

// ClearActions empties the buffer.
func (f *fakeBackend) ClearActions() {
	f.mu.Lock()
	f.actions = f.actions[:0]
	f.mu.Unlock()
}

func (f *fakeBackend) Platform() string             { return "linux" }
func (f *fakeBackend) Permissions() map[string]bool { return map[string]bool{} }

func (f *fakeBackend) HostInfo() (string, string, int64) {
	host, _ := os.Hostname()
	if host == "" {
		host = "fake"
	}
	name := os.Getenv("USER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	up := int64(time.Since(f.start).Seconds())
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				up = int64(v)
			}
		}
	}
	return host, name, up
}

func (f *fakeBackend) Displays() ([]Display, error) {
	return []Display{{Index: 0, W: fakeW, H: fakeH, Scale: 1, Primary: true, Name: "Fake Display"}}, nil
}

// Capture renders the synthetic frame: a hue gradient, the time with
// milliseconds in big digits, and a ring + crosshair at the pointer.
func (f *fakeBackend) Capture(display int) (*image.RGBA, error) {
	if display != 0 {
		return nil, bridge.Errorf(bridge.CodeInvalidParams, "no display %d", display)
	}
	f.mu.Lock()
	cx, cy := int(f.cx), int(f.cy)
	f.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, fakeW, fakeH))
	pix := img.Pix
	for y := 0; y < fakeH; y++ {
		ly := 255 - y*110/fakeH
		for x := 0; x < fakeW; x++ {
			o := y*img.Stride + x*4
			r, g, b := hueRGB(float64(x)/fakeW, float64(ly)/255)
			pix[o], pix[o+1], pix[o+2], pix[o+3] = r, g, b, 0xff
		}
	}
	now := f.now()
	stamp := fmt.Sprintf("%02d:%02d:%02d.%03d", now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6)
	drawDigits(img, 40, 40, 8, stamp, color.RGBA{255, 255, 255, 255})
	drawDigits(img, 40, 40+5*8+16, 4, "REACH FAKE "+strconv.Itoa(fakeW)+"X"+strconv.Itoa(fakeH), color.RGBA{20, 20, 30, 255})
	drawMarker(img, cx, cy)
	return img, nil
}

// hueRGB maps h in 0..1 around the colour wheel at the given lightness.
func hueRGB(h, l float64) (uint8, uint8, uint8) {
	h6 := h * 6
	i := int(h6) % 6
	fr := h6 - float64(int(h6))
	var r, g, b float64
	switch i {
	case 0:
		r, g, b = 1, fr, 0
	case 1:
		r, g, b = 1-fr, 1, 0
	case 2:
		r, g, b = 0, 1, fr
	case 3:
		r, g, b = 0, 1-fr, 1
	case 4:
		r, g, b = fr, 0, 1
	default:
		r, g, b = 1, 0, 1-fr
	}
	// Pastel: mix with white, then scale by lightness.
	r, g, b = (r+1.2)/2.2*l, (g+1.2)/2.2*l, (b+1.2)/2.2*l
	return uint8(r * 255), uint8(g * 255), uint8(b * 255)
}

// glyphs is a 3×5 bitmap font for the characters of the overlay text.
var glyphs = map[rune][5]uint8{
	'0': {0b111, 0b101, 0b101, 0b101, 0b111}, '1': {0b010, 0b110, 0b010, 0b010, 0b111},
	'2': {0b111, 0b001, 0b111, 0b100, 0b111}, '3': {0b111, 0b001, 0b111, 0b001, 0b111},
	'4': {0b101, 0b101, 0b111, 0b001, 0b001}, '5': {0b111, 0b100, 0b111, 0b001, 0b111},
	'6': {0b111, 0b100, 0b111, 0b101, 0b111}, '7': {0b111, 0b001, 0b001, 0b010, 0b010},
	'8': {0b111, 0b101, 0b111, 0b101, 0b111}, '9': {0b111, 0b101, 0b111, 0b001, 0b111},
	':': {0b000, 0b010, 0b000, 0b010, 0b000}, '.': {0b000, 0b000, 0b000, 0b000, 0b010},
	' ': {0, 0, 0, 0, 0}, 'A': {0b010, 0b101, 0b111, 0b101, 0b101}, 'C': {0b111, 0b100, 0b100, 0b100, 0b111},
	'E': {0b111, 0b100, 0b110, 0b100, 0b111}, 'F': {0b111, 0b100, 0b110, 0b100, 0b100},
	'H': {0b101, 0b101, 0b111, 0b101, 0b101}, 'K': {0b101, 0b101, 0b110, 0b101, 0b101},
	'R': {0b110, 0b101, 0b110, 0b101, 0b101}, 'X': {0b101, 0b101, 0b010, 0b101, 0b101},
}

// drawDigits renders text with glyphs at (x, y), each cell scale px.
func drawDigits(img *image.RGBA, x, y, scale int, text string, c color.RGBA) {
	for _, r := range text {
		g, ok := glyphs[r]
		if !ok {
			g = glyphs[' ']
		}
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if g[row]&(1<<(2-col)) == 0 {
					continue
				}
				fillRect(img, x+col*scale, y+row*scale, scale, scale, c)
			}
		}
		x += 4 * scale
	}
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			if image.Pt(xx, yy).In(img.Rect) {
				img.SetRGBA(xx, yy, c)
			}
		}
	}
}

// drawMarker draws a 24 px ring with a crosshair at the pointer.
func drawMarker(img *image.RGBA, cx, cy int) {
	white := color.RGBA{255, 255, 255, 255}
	dark := color.RGBA{30, 30, 40, 255}
	for dy := -14; dy <= 14; dy++ {
		for dx := -14; dx <= 14; dx++ {
			d := dx*dx + dy*dy
			switch {
			case d <= 14*14 && d >= 11*11:
				img.SetRGBA(cx+dx, cy+dy, dark)
			case d < 11*11 && d >= 8*8:
				img.SetRGBA(cx+dx, cy+dy, white)
			}
		}
	}
	fillRect(img, cx-24, cy-1, 48, 3, dark)
	fillRect(img, cx-1, cy-24, 3, 48, dark)
}

func (f *fakeBackend) MouseMove(dx, dy float64) error {
	f.mu.Lock()
	f.cx = clampF(f.cx+dx, 0, fakeW-1)
	f.cy = clampF(f.cy+dy, 0, fakeH-1)
	f.mu.Unlock()
	f.record("mouse.move", "dx", dx, "dy", dy)
	return nil
}

func (f *fakeBackend) MouseMoveTo(display int, x, y float64) error {
	if display != 0 {
		return bridge.Errorf(bridge.CodeInvalidParams, "no display %d", display)
	}
	f.mu.Lock()
	f.cx = clampF(x*fakeW, 0, fakeW-1)
	f.cy = clampF(y*fakeH, 0, fakeH-1)
	f.mu.Unlock()
	f.record("mouse.moveTo", "display", display, "x", x, "y", y)
	return nil
}

func (f *fakeBackend) MouseButton(button string, down bool) error {
	f.mu.Lock()
	f.down[button] = down
	f.mu.Unlock()
	kind := "mouse.up"
	if down {
		kind = "mouse.down"
	}
	f.record(kind, "button", button)
	return nil
}

func (f *fakeBackend) MouseClick(button string, count int) error {
	f.mu.Lock()
	x, y := f.cx/fakeW, f.cy/fakeH
	f.mu.Unlock()
	f.record("mouse.click", "button", button, "count", count, "x", x, "y", y)
	return nil
}

func (f *fakeBackend) Scroll(dx, dy float64) error {
	f.record("mouse.scroll", "dx", dx, "dy", dy)
	return nil
}

func (f *fakeBackend) TypeText(text string) error {
	f.record("key.text", "text", text)
	return nil
}

func (f *fakeBackend) Key(key string, mods []string, down, up bool) error {
	k, ok := resolveKey(key)
	if !ok {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown key %q", key)
	}
	if _, ok := resolveMods(mods); !ok {
		return bridge.Errorf(bridge.CodeInvalidParams, "unknown modifier in %v", mods)
	}
	if mods == nil {
		mods = []string{}
	}
	switch {
	case down && up:
		f.record("key.press", "key", k.Name, "mods", mods, "scan", k.Scan, "ext", k.Ext, "vk", k.VK)
	case down:
		f.record("key.down", "key", k.Name, "scan", k.Scan)
	default:
		f.record("key.up", "key", k.Name, "scan", k.Scan)
	}
	return nil
}

func (f *fakeBackend) Media(action string) error {
	f.mu.Lock()
	switch action {
	case "volumeUp":
		f.volume = clampI(f.volume+2, 0, 100)
	case "volumeDown":
		f.volume = clampI(f.volume-2, 0, 100)
	case "mute":
		f.muted = !f.muted
	}
	f.mu.Unlock()
	f.record("media", "action", action)
	return nil
}

func (f *fakeBackend) VolumeGet() (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.volume, f.muted, nil
}

func (f *fakeBackend) VolumeSet(level int) error {
	f.mu.Lock()
	f.volume = clampI(level, 0, 100)
	f.mu.Unlock()
	f.record("volume.set", "level", level)
	return nil
}

// AppsList reads the .desktop files of appsDir; without any it answers a
// fixed set so the UI always has something to show.
func (f *fakeBackend) AppsList(icons bool) ([]AppInfo, error) {
	var apps []AppInfo
	entries, _ := filepath.Glob(filepath.Join(f.appsDir, "*.desktop"))
	seen := map[string]bool{}
	for _, path := range entries {
		name, ok := desktopName(path)
		if !ok || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		apps = append(apps, AppInfo{ID: path, Name: name})
	}
	if len(apps) == 0 {
		for _, n := range []string{"Calculator", "Mail", "Music", "Notes", "Photos", "Terminal"} {
			apps = append(apps, AppInfo{ID: "fake:" + strings.ToLower(n), Name: n})
		}
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	if icons {
		for i := range apps {
			apps[i].IconB64 = fakeIcon(apps[i].Name)
		}
	}
	return apps, nil
}

// desktopName returns the Name of an application .desktop file, false for
// hidden entries and non-applications.
func desktopName(path string) (string, bool) {
	fh, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer fh.Close()
	var name string
	inEntry := false
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			name = strings.TrimSpace(v)
		case "NoDisplay", "Hidden":
			if strings.TrimSpace(v) == "true" {
				return "", false
			}
		case "Type":
			if strings.TrimSpace(v) != "Application" {
				return "", false
			}
		}
	}
	return name, name != ""
}

// fakeIcon is a 64 px PNG: a rounded tile whose colour comes from the name.
func fakeIcon(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	r, g, b := hueRGB(float64(h.Sum32()%360)/360, 0.85)
	tile := color.RGBA{r, g, b, 255}
	dark := color.RGBA{uint8(int(r) * 2 / 3), uint8(int(g) * 2 / 3), uint8(int(b) * 2 / 3), 255}
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if !inRoundedRect(x, y, 64, 64, 14) {
				continue
			}
			if x+y > 64 {
				img.SetRGBA(x, y, dark)
			} else {
				img.SetRGBA(x, y, tile)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func inRoundedRect(x, y, w, h, r int) bool {
	cx, cy := x, y
	if x < r {
		cx = r
	} else if x >= w-r {
		cx = w - r - 1
	}
	if y < r {
		cy = r
	} else if y >= h-r {
		cy = h - r - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func (f *fakeBackend) AppsRunning() ([]RunningApp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RunningApp(nil), f.running...), nil
}

func (f *fakeBackend) AppLaunch(id string) error {
	if id == "" {
		return bridge.Errorf(bridge.CodeInvalidParams, "missing id")
	}
	f.record("apps.launch", "id", id)
	return nil
}

func (f *fakeBackend) AppFocus(id string, pid int) error {
	f.mu.Lock()
	found := false
	for i := range f.running {
		match := (pid != 0 && f.running[i].PID == pid) || (id != "" && f.running[i].ID == id)
		if match {
			found = true
		}
		f.running[i].Active = match
	}
	f.mu.Unlock()
	if !found {
		return bridge.Errorf(bridge.CodeNotFound, "no running app %q / pid %d", id, pid)
	}
	f.record("apps.focus", "id", id, "pid", pid)
	return nil
}

func (f *fakeBackend) AppQuit(pid int, force bool) error {
	f.mu.Lock()
	kept := f.running[:0]
	found := false
	for _, r := range f.running {
		if r.PID == pid {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	f.running = kept
	f.mu.Unlock()
	if !found {
		return bridge.Errorf(bridge.CodeNotFound, "no process %d", pid)
	}
	f.record("apps.quit", "pid", pid, "force", force)
	return nil
}

func (f *fakeBackend) WindowsList() ([]WindowInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]WindowInfo, 0, len(f.running))
	for _, r := range f.running {
		out = append(out, WindowInfo{ID: "w" + strconv.Itoa(r.PID), Title: r.Name + " — Untitled", App: r.Name, PID: r.PID, Display: 0})
	}
	return out, nil
}

func (f *fakeBackend) WindowFocus(id string) error {
	pid, err := strconv.Atoi(strings.TrimPrefix(id, "w"))
	if err != nil {
		return bridge.Errorf(bridge.CodeInvalidParams, "bad window id %q", id)
	}
	if err := f.AppFocus("", pid); err != nil {
		return err
	}
	f.record("windows.focus", "id", id)
	return nil
}

func (f *fakeBackend) Power(action string) error {
	f.record("power", "action", action)
	return nil
}

func (f *fakeBackend) ClipboardGet() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clipboard, nil
}

func (f *fakeBackend) ClipboardSet(text string) error {
	f.mu.Lock()
	f.clipboard = text
	f.mu.Unlock()
	f.record("clipboard.set", "text", text)
	return nil
}

func (f *fakeBackend) OpenURL(url string) error {
	f.record("open.url", "url", url)
	return nil
}

func (f *fakeBackend) LaunchAtLogin() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launchAtLogin
}

func (f *fakeBackend) SetLaunchAtLogin(enabled bool) error {
	f.mu.Lock()
	f.launchAtLogin = enabled
	f.mu.Unlock()
	f.record("launchAtLogin", "enabled", enabled)
	return nil
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
