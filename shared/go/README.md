# shared/go — Go packages shared by the Windows apps (and their Linux test builds)

Module path: `github.com/r-thirumaran/mac-utility/shared/go/<pkg>` (one Go module at
the repo root). Pure Go, no cgo, cross-compiled with `GOOS=windows GOARCH=amd64`.
The key table loader lives next to its data in `shared/keymap` (section 5).

```
GOTOOLCHAIN=local go test ./shared/... ./shared/keymap/...      # Linux
GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 go vet ./...         # Windows (compile + vet only)
```

**Windows-only code (`tray`, `winutil`, the `webhost` window) is only compile-checked on
the Linux build box.** It is written against the vendored sources of go-webview2,
getlantern/systray and golang.org/x/sys, but it has not been executed on Windows here.

| package | purpose |
|---|---|
| `framing` | u32-BE length-prefixed frames (≤ 1 MiB) — the Edgehop transport |
| `securechan` | Edgehop handshake + ChaCha20-Poly1305 channel (`docs/edgehop-protocol.md` §3–4) |
| `bridge` | JSON-RPC dispatcher behind every web UI (`docs/ARCHITECTURE.md` §4) |
| `httpserve` | static files from an `fs.FS` + `/ws` bridge transport + `/api/` mux |
| `keymap` (`shared/keymap`) | HID ⇄ DOM ⇄ mac ⇄ Windows scan code lookups |
| `mdnsdisc` | Bonjour advertise/browse over grandcat/zeroconf |
| `tray` | notification-area icon + menu (systray on Windows, no-op elsewhere) |
| `webhost` | the desktop app shell: server + token auth + tray + WebView2 window |
| `winutil` | elevation, single instance, autostart, open URL, hidden exec |
| `appdirs` | config/state/log directories per OS, atomic JSON files |
| `applog` | rotating file logger (5 MB, one backup), tee to stderr on a terminal |

---

## 1. framing

Every Edgehop frame is `u32 big-endian length || payload`, payload ≤ `MaxFrame`
(1 MiB). `Write` sends header and payload in one `Write` call; `Read` rejects an
oversized announcement before reading its payload.

```go
const MaxFrame = 1 << 20
var ErrTooLarge error
func Write(w io.Writer, payload []byte) error
func Read(r io.Reader) ([]byte, error)   // io.EOF at a clean end, io.ErrUnexpectedEOF on a short read
```

```go
if err := framing.Write(conn, []byte(`{"t":"ping"}`)); err != nil { … }
payload, err := framing.Read(conn)
if errors.Is(err, framing.ErrTooLarge) { conn.Close() }
```

## 2. securechan

Implements `docs/edgehop-protocol.md` §3–§4 byte-for-byte: X25519 static +
ephemeral keys, `hello`/`hello_ack` with the exact field order
`t, v, id, name, plat, spk, epk, pair` and no whitespace, HKDF-SHA256 keys bound to
the transcript, an optional 6-digit pairing code mixed in as a PSK, and
ChaCha20-Poly1305 frames with per-direction keys and counters (nonce =
`0x00000000 || u64be(counter)`).

```go
type Identity struct{ Priv, Pub [32]byte }
func NewIdentity() (Identity, error)
func IdentityFromPriv(priv [32]byte) Identity
func (Identity) ID() string                 // hex of SHA-256(Pub)[:16]
func IDFromPub(pub [32]byte) string

type PeerInfo struct{ ID, Name, Plat string; StaticPub [32]byte }

type TrustStore interface {
    Trusted(id string) (pub [32]byte, ok bool)
    Trust(info PeerInfo) error
}
type MemoryTrust struct{ … }                // NewMemoryTrust(); Trusted, Trust, Forget, Peers

type Options struct {
    Self       Identity
    Name, Plat string
    Trust      TrustStore                   // required
    Pair       bool                         // initiator: hello.pair (user asked to pair now)
    AskCode    func() (string, error)       // initiator: the code shown on the other machine
    ShowCode   func(code string, peer PeerInfo) // acceptor: display the generated code
    Rand       io.Reader                    // nil = crypto/rand (tests inject fixed bytes)
    Now        func() time.Time             // nil = time.Now (CodeTTL check)
}
func Initiate(conn io.ReadWriter, o Options) (*Channel, PeerInfo, error)
func Accept(conn io.ReadWriter, o Options) (*Channel, PeerInfo, error)

type Channel struct{ … }
func (c *Channel) Send(plaintext []byte) error   // plaintext = type byte || body; > MaxPlaintext → ErrTooLarge
func (c *Channel) Recv() ([]byte, error)         // ErrBadFrame / ErrCounterWrap are fatal: Close
func (c *Channel) Close() error                  // closes conn when it is an io.Closer
func (c *Channel) Peer() PeerInfo
func (c *Channel) IsInitiator() bool
func (c *Channel) Counters() (sent, received uint64)

const Version = 1
const CodeTTL = 120 * time.Second
const MaxPlaintext = framing.MaxFrame - 16
const TypeControl, TypeMouseMove, TypeMouseButton, TypeScroll, TypeKey, TypeKeyText byte = 1, 2, 3, 4, 5, 6
var ErrUnpaired, ErrPairFailed, ErrCodeExpired, ErrBadFrame, ErrCounterWrap, ErrProtocol, ErrClosed, ErrTooLarge error
type RemoteError struct{ Code, Message string }  // an {"t":"error"} from the peer

// Building blocks (also used by the test vectors):
func DerivePSK(code, idA, idB string) [32]byte
func Transcript(helloBytes, helloAckBytes []byte) [32]byte
func DeriveKeys(dh1, dh2, dh3 []byte, psk, transcript [32]byte) (initToAcc, accToInit [32]byte)
func GenerateCode(r io.Reader) (string, error)
func EncodeControl(v any) ([]byte, error)       // TypeControl || JSON
func Split(payload []byte) (typ byte, body []byte, ok bool)
```

Handshake outcomes:

* trusted both ways, `Pair=false` → plain handshake, no code;
* `hello.pair=true` → **pairing handshake**: the acceptor draws a 6-digit code
  (from `Rand`), calls `ShowCode`, answers `hello_ack` with `pair = !known`; the
  initiator calls `AskCode` after `hello_ack`; both derive the PSK; the acceptor
  only sends `auth_ok` and both only call `Trust()` after the first encrypted frame
  authenticated. A wrong code → `ErrPairFailed` on the acceptor (first encrypted
  frame fails) and on the initiator (the acceptor closes instead of sending
  `auth_ok`);
* unknown key with `pair=false` → `{"t":"error","code":"unpaired"}` + `ErrUnpaired`
  on both sides; an acceptor without `ShowCode` also answers `unpaired`;
* the acceptor refuses a code redeemed later than `CodeTTL` with `ErrCodeExpired`.

Rate limiting (5 failures / 10 min) and connection timeouts are the app's job
(set deadlines on the `net.Conn`). Pairing codes are never logged.

```go
ch, peer, err := securechan.Initiate(conn, securechan.Options{
    Self: self, Name: hostname, Plat: "windows", Trust: store, Pair: userWantsToPair,
    AskCode: ui.PromptForCode,
})
if errors.Is(err, securechan.ErrUnpaired) { … show "pair first" … }
go func() {
    for {
        p, err := ch.Recv()
        if err != nil { ch.Close(); return }      // ErrBadFrame etc.
        typ, body, _ := securechan.Split(p)
        …
    }
}()
msg, _ := securechan.EncodeControl(map[string]any{"t": "ping", "ts": time.Now().UnixMilli()})
_ = ch.Send(msg)
```

**Test vectors.** `go test ./shared/go/securechan -update` regenerates
`shared/testdata/edgehop/handshake-vectors.json` from fixed static/ephemeral keys
(a trusted and a pairing handshake) with the exact `hello`/`hello_ack` bytes, DH
outputs, PSK, transcript, both keys and the first ciphertext in each direction.
The normal test run asserts against the file; the Swift implementation is checked
against the same file.

## 3. bridge

```go
type Handler func(ctx context.Context, params json.RawMessage) (any, error)
type Dispatcher struct{ … }
func New() *Dispatcher
func (d *Dispatcher) Register(method string, h Handler)
func (d *Dispatcher) Handle(ctx context.Context, req []byte) (resp []byte)
func (d *Dispatcher) Emit(event string, data any)
func (d *Dispatcher) AddSink(s Sink) (remove func())
func (d *Dispatcher) Methods() []string
func (d *Dispatcher) SinkCount() int

type Sink interface{ Push(eventJSON []byte) }      // must not block
type ChanSink struct{ … }                         // NewChanSink(size); C(), Dropped(), Push
type Error struct{ Code, Message string; Details any }   // implements error
func Errorf(code, format string, a ...any) *Error
func Params[T any](raw json.RawMessage) (T, error) // invalid_params on failure
func EncodeEvent(event string, data any) ([]byte, error)
const CodeNotFound, CodeInvalidParams, CodeInternal, CodeNotAuthorized, CodeNeedsPermission, CodeNotSupported
```

`Handle` echoes the request id unchanged (number or string; `null` when missing or
when the request is not JSON), answers `{"id":…,"result":…}` (a nil result becomes
`{}`) or `{"id":…,"error":{"code","message","details"?}}`. Unknown method →
`not_found`, bad JSON / missing method → `invalid_params`, plain `error` → `internal`,
a handler panic → `internal`. `Emit` encodes once and pushes to every sink; sinks
drop when full (`ChanSink.Dropped()`). All methods are safe for concurrent use.

```go
d := bridge.New()
d.Register("power.apply", func(ctx context.Context, raw json.RawMessage) (any, error) {
    p, err := bridge.Params[struct{ Changes map[string]any `json:"changes"` }](raw)
    if err != nil { return nil, err }
    if !elevated { return nil, bridge.Errorf(bridge.CodeNotAuthorized, "needs admin") }
    return map[string]any{"applied": keys(p.Changes)}, nil
})
d.Emit("vitals.sample", sample)
```

## 4. httpserve

```go
type Options struct {
    Static     fs.FS                      // "/" → index.html; directories → index.html
    Dispatcher *bridge.Dispatcher         // nil = empty
    Auth       func(r *http.Request) bool // nil = allow; sees every request
    API        *http.ServeMux             // mounted at /api/ (full path), may be nil
    AllowCORS  bool                       // ACAO:* + preflight on /api/, any origin on /ws
    OnWS       func(c *Conn)              // after accept, before the first read
}
func New(o Options) http.Handler
func ListenLoopback(h http.Handler) (addr string, srv *http.Server, err error)  // 127.0.0.1:0
func Listen(addr string, h http.Handler) (string, *http.Server, error)        // e.g. "0.0.0.0:7788"

type Conn struct{ … }
func (c *Conn) SendBinary(b []byte) error
func (c *Conn) SendJSON(v any) error
func (c *Conn) Close()                       // idempotent, non-blocking
func (c *Conn) Context() context.Context     // cancelled when the socket closes
func (c *Conn) Request() *http.Request       // the upgrade request (query, cookies)
func (c *Conn) Dropped() uint64
const MaxMessage = 8 << 20
const EventBuffer = 256
```

`/ws` is upgraded with coder/websocket (compression disabled). Every text frame is
handed to `Dispatcher.Handle` on its own goroutine; responses, events and direct
sends go through one write mutex. Each connection registers itself as a
`bridge.Sink` with a 256-frame buffer, so `Dispatcher.Emit` reaches every open
socket. Binary frames from clients are ignored (v1 has none).

```go
mux := http.NewServeMux()
mux.HandleFunc("/api/info", infoHandler)
h := httpserve.New(httpserve.Options{Static: ui.Static, Dispatcher: d, API: mux, AllowCORS: true,
    Auth: func(r *http.Request) bool { return r.URL.Path != "/ws" || tokens.Valid(r.URL.Query().Get("token")) },
    OnWS: func(c *httpserve.Conn) { c.SendJSON(map[string]any{"event": "info", "data": info()}) }})
addr, srv, err := httpserve.Listen("0.0.0.0:7788", h)
```

## 5. keymap (`shared/keymap`, import `github.com/r-thirumaran/mac-utility/shared/keymap`)

Embeds `hid-keymap.json`. Wire values: page 0x07 = usage, page 0x0C = 0x1000+usage,
page 0x01 = 0x2000+usage (play/pause = 0x10CD).

```go
type Key struct {
    Usage, Page, Wire, Mac, WinScan, Evdev int   // Mac -1 = none, WinScan 0 = none
    WinExt bool                                  // E0 prefix
    Dom, Ident string                            // Dom "" for keys without a DOM code
}
type Map struct{ … }
func Default() *Map                            // embedded table, parsed once
func Parse(data []byte) (*Map, error)
func (m *Map) Keys() []Key
func (m *Map) ByWire(wire int) (Key, bool)
func (m *Map) ByDom(dom string) (Key, bool)
func (m *Map) ByMac(code int) (Key, bool)
func (m *Map) ByWinScan(scan int, ext bool) (Key, bool)
func (m *Map) ByEvdev(code int) (Key, bool)
func ModifierBit(wire int) (bit uint8, ok bool) // 0xE0..0xE7 → bit0..bit7
func IsModifier(wire int) bool
const ModLeftControl … ModRightGUI = 0xE0 … 0xE7
```

```go
k, _ := keymap.Default().ByDom("ArrowLeft")   // k.Wire 0x50, k.Mac 123, k.WinScan 0x4B, k.WinExt true
if bit, ok := keymap.ModifierBit(k.Wire); ok { mods |= bit }
```

## 6. mdnsdisc

```go
type Event struct{ Added bool; Instance, Host string; Port int; Addrs []net.IP; TXT map[string]string }
func Advertise(ctx context.Context, service, instance string, port int, txt map[string]string) (stop func(), err error)
func Browse(ctx context.Context, service string, on func(Event)) error   // blocks until ctx ends
func TXT(m map[string]string) []string         // "k=v" in key order
func ParseTXT(records []string) map[string]string
var BrowseCycle = 30 * time.Second
var MissedCycles = 2
```

`Advertise` registers on every multicast interface and withdraws on `stop()` or
when `ctx` ends. `Browse` runs in query cycles of `BrowseCycle`: a service is
reported once when first seen (or when host/port/TXT/addresses change) and with
`Added=false` after it missed `MissedCycles` consecutive cycles — zeroconf v1.0.0
never surfaces goodbye packets, so removal is detected by silence (≈ 1–1.5 min
with the defaults). The resolver falls back to IPv4-only on hosts without IPv6.
The loopback test skips itself when no multicast interface answers within 3 s.

```go
stop, err := mdnsdisc.Advertise(ctx, "_edgehop._tcp", displayName, port,
    map[string]string{"v": "1", "id": self.ID(), "name": displayName, "plat": "windows", "pk": pkB64})
go mdnsdisc.Browse(ctx, "_edgehop._tcp", func(e mdnsdisc.Event) {
    if e.Added { peers.Add(e.TXT["id"], e.Addrs, e.Port) } else { peers.Lost(e.TXT["id"]) }
})
```

## 7. tray

```go
type Item struct{ Title string; OnClick func(); Checked, Disabled, Separator bool }
type Config struct{ Tooltip string; IconICO []byte; Items []Item }
type Tray struct{ … }
func Run(cfg Config, onReady func(*Tray))   // blocks until Quit; main goroutine on Windows
func (t *Tray) SetTooltip(string)
func (t *Tray) SetItems([]Item)
func (t *Tray) Notify(title, body string)
func (t *Tray) Quit()
func (t *Tray) Tooltip() string
func (t *Tray) Items() []Item
func (t *Tray) Click(title string) bool     // invoke an item's OnClick by title (tests, shortcuts)
func (t *Tray) Done() <-chan struct{}
```

Windows (getlantern/systray): `Run` must be on the main goroutine. systray cannot
remove or reorder menu items, so `SetItems` reuses a pool of items **in order**:
titles, checked and disabled states change freely, a shorter list hides the extra
items, a longer list appends new items at the end, and separators stay where the
first `SetItems` (i.e. `Config.Items`) placed them. Design the complete menu in
`Config.Items` and use `SetItems` for state changes. `Notify` raises a WinRT toast
through a hidden PowerShell (attributed to PowerShell because an unpackaged exe has
no AppUserModelID); it is best effort.

Non-Windows: `Run` calls `onReady` immediately and blocks until `Quit`; `Notify`
logs; `Items()`/`Tooltip()`/`Click()` let tests inspect and drive the menu.

```go
tray.Run(tray.Config{Tooltip: "Vitals", IconICO: icon, Items: []tray.Item{
    {Title: "Keep awake", OnClick: toggleAwake},
    {Separator: true},
    {Title: "Quit", OnClick: quit},
}}, func(t *tray.Tray) { t.SetTooltip("Vitals — CPU 12%") })
```

## 8. webhost

The app shell used by every Windows app's `main` (and by the Linux test build).

```go
type Config struct {
    Title         string
    Width, Height int                 // 0 = 1000×700
    IconICO       []byte
    Static        fs.FS
    Dispatcher    *bridge.Dispatcher
    API           *http.ServeMux
    ShowOnStart   bool                // Windows: open the window immediately
    Listen        string              // "" = 127.0.0.1:0; Reach passes "0.0.0.0:7788"
    AllowLAN      bool                // CORS on /api/, any origin on /ws
    Tray          tray.Config         // app items go between "Open <Title>" and "Quit"
    OnTray        func(*tray.Tray)
    NoUI          bool                // serve only; print the URL; no tray/window/browser (--no-ui, tests)
    Token         string              // "" = random 32-byte base64url
    Auth          func(r *http.Request) bool // extra allow rule OR'ed with the token rule (Reach phone tokens)
    OnListen      func(url string)    // the printed URL, for apps and tests
    Stdout        io.Writer           // nil = os.Stdout
}
func Run(ctx context.Context, c Config) error   // nil on ctx cancel or Quit
func NewToken() (string, error)
const CookieName = "mu_token"
```

Behaviour on every platform:

* starts `httpserve` with `Auth` = token in `?token=` **or** in the `mu_token` cookie
  (constant-time compare); `/api/` is not gated (Reach guards it itself); an
  optional `Config.Auth` adds more allowed requests;
* the first `GET /` with the right token sets the `mu_token` cookie
  (`Path=/; HttpOnly; SameSite=Strict`) so later navigations, static assets and the
  WebSocket pass without the query string;
* prints exactly one line `listening on http://127.0.0.1:<port>/?token=<token>` to
  `Stdout` (the UI tests parse it) and calls `OnListen`;
* the page talks to the backend over the WebSocket — no `Bind()` — so Windows and
  Linux run the same code;
* runs the tray with `Open <Title>`, the app's items, `Quit`; `Quit` or a cancelled
  `ctx` ends `Run` after shutting the server down.

Windows: `Run` must be called from the main goroutine (the tray owns it). A
dedicated locked OS thread opens a go-webview2 window (`NewWithOptions`, DataPath
`%LOCALAPPDATA%\<Title>\webview`, centered, window icon from `IconICO`) and navigates
to the URL; "Open <Title>" raises the window or recreates it after the user closed
it. If the WebView2 runtime is missing (`webviewloader.GetInstalledVersion` empty or
`NewWithOptions` returns nil) the URL opens in the default browser via
`ShellExecuteW`. Linux: no window; with `NoUI=false` the URL is handed to `xdg-open`
and failures are ignored; the no-op tray still calls `OnTray`.

```go
func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()
    err := webhost.Run(ctx, webhost.Config{
        Title: "Vitals", IconICO: icon, Static: ui.Static, Dispatcher: d,
        ShowOnStart: !*startHidden, NoUI: *noUI,
        Tray: tray.Config{Items: []tray.Item{{Title: "Keep awake", OnClick: toggle}}},
    })
    if err != nil { log.Fatal(err) }
}
```

## 9. winutil

```go
func IsElevated() bool                                   // CheckTokenMembership(Administrators); root elsewhere
func RelaunchElevated(args ...string) error              // ShellExecuteW "runas" on os.Executable()
func SingleInstance(name string) (release func(), first bool, err error) // CreateMutexW Local\<name>; flock elsewhere
func SetAutostart(appName, exePath string, enabled bool) error // HKCU\…\CurrentVersion\Run
func Autostart(appName string) bool
func OpenURL(url string) error                           // ShellExecuteW "open"; xdg-open / open elsewhere
func RunHidden(name string, args ...string) ([]byte, error) // exec with HideWindow (powercfg, powershell …)
var ErrNotSupported error                                // RelaunchElevated/SetAutostart off Windows
```

```go
release, first, err := winutil.SingleInstance("com.thiru.vitals")
if err == nil && !first { os.Exit(0) }
defer release()
if !winutil.IsElevated() { _ = winutil.RelaunchElevated("--apply-power") ; return }
out, err := winutil.RunHidden("powercfg", "/query")
```

## 10. appdirs

```go
func ConfigDir(app string) (string, error)   // created 0700
func StateDir(app string) (string, error)
func LogDir(app string) (string, error)
func LoadJSON(path string, v any) error       // fs.ErrNotExist when missing
func SaveJSON(path string, v any) error       // 0600, temp file + fsync + rename
```

| OS | config | state / log |
|---|---|---|
| windows | `%APPDATA%\<App>` | `%LOCALAPPDATA%\<App>` |
| darwin | `~/Library/Application Support/<App>` | same / `~/Library/Logs/<App>` |
| linux | `$XDG_CONFIG_HOME/<app>` (`~/.config`) | `$XDG_STATE_HOME/<app>` (`~/.local/state`) |

```go
dir, _ := appdirs.ConfigDir("Edgehop")
path := filepath.Join(dir, "config.json")
var cfg Config
if err := appdirs.LoadJSON(path, &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) { … }
_ = appdirs.SaveJSON(path, cfg)
```

## 11. applog

```go
const MaxSize = 5 << 20
func Open(app string) (*log.Logger, func() error, error)       // <LogDir(app)>/<app>.log
func OpenFile(path string, maxSize int64) (*log.Logger, func() error, error)
```

Rotates when a write would push the file past `maxSize`: the file becomes
`<name>.1` (replacing the previous backup) and a new file starts. When stdout is a
terminal the logger also writes to stderr.

```go
logger, closeLog, err := applog.Open("Vitals")
if err != nil { log.Fatal(err) }
defer closeLog()
logger.Printf("started %s", version)
```
