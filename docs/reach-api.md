# Reach agent API — version 1

Reach puts a small agent on every laptop (macOS: Swift app, Windows: Go exe) and
a phone app that finds the agents on the local network and drives them. This is
the contract between the agents and the phone UI (`apps/reach/ui`). Both agents
MUST implement it identically; the Linux build of the Go agent implements it
with recording fakes for tests.

## 1. Transport

* The agent listens on TCP port **7788** (configurable) on all interfaces and
  advertises Bonjour/mDNS `_reach._tcp` with TXT `v=1 id=<agentId> name=<name>
  plat=<macos|windows|linux> port=<port>`.
* `GET /` and all static paths serve the phone UI (the PWA). The same files are
  shipped inside the Capacitor app, which then talks to the agent over the
  network only.
* `GET /api/info` → `{"id","name","platform","version","pairingRequired":true,
  "caps":["screen","apps","media","power","clipboard"]}` — no auth.
* `GET /api/peers` → `[{"id","name","platform","host","port"}]` — every other
  Reach agent this agent currently sees on the LAN, so the phone needs only one
  address to find them all. No auth.
* `POST /api/pair` body `{"device":"Thiru's iPhone","code":"123456"}` →
  `{"token":"…"}` or 403 `{"error":"bad_code"}`. The agent shows the code in its
  menu/tray UI (and as a desktop notification) for 120 s after the phone asks
  for it with `POST /api/pair/request {"device":…}` → 204. Five wrong codes in
  10 minutes lock pairing for 10 minutes.
* WebSocket `GET /ws?token=<token>` — all control traffic. Rejected with 401 if
  the token is unknown or revoked. Frames are the bridge JSON described in
  `docs/ARCHITECTURE.md` §4 (`{id, method, params}` → `{id, result|error}`, and
  server-pushed `{event, data}`); binary WebSocket frames carry screen images
  (section 4).
* CORS: `Access-Control-Allow-Origin: *` on `/api/*` so the Capacitor app
  (origins `capacitor://localhost`, `http://localhost`) and a PWA served by one
  agent can talk to another agent.
* Plaintext on the LAN is accepted for v1 (documented in the README); tokens are
  random 32-byte base64url strings, stored by the agent with the device name,
  revocable from the agent's settings UI.

## 2. Methods

Coordinates: `display` is an index into `screen.displays`; `x`,`y` are
fractions 0..1 of that display. Keys: `key` is a DOM `code` name from
`shared/keymap/hid-keymap.json` (`"KeyA"`, `"Enter"`, `"ArrowLeft"`, `"F5"`,
`"MetaLeft"`) or a media name (`"MediaPlayPause"`, `"MediaTrackNext"`,
`"MediaTrackPrevious"`, `"AudioVolumeUp"`, `"AudioVolumeDown"`,
`"AudioVolumeMute"`, `"BrightnessUp"`, `"BrightnessDown"`). `mods` is an array
of `"ctrl"|"shift"|"alt"|"meta"`; `"cmdOrCtrl"` means ⌘ on macOS and Ctrl on
Windows.

| method | params | result |
|---|---|---|
| `info` | — | same as `/api/info` plus `{"hostname","user","uptimeSec"}` |
| `screen.displays` | — | `[{index, w, h, scale, primary, name}]` |
| `screen.frame` | `{display, maxWidth (≤1280), quality (0.1–0.9)}` | `{"display", "w", "h", "jpegB64"}` — one-shot |
| `screen.stream.start` | `{display, maxWidth, quality, fps (1–10)}` | `{}`; then binary frames (section 4) until `screen.stream.stop` or the socket closes |
| `screen.stream.stop` | — | `{}` |
| `mouse.move` | `{dx, dy}` pixels, relative | `{}` |
| `mouse.moveTo` | `{display, x, y}` | `{}` |
| `mouse.click` | `{button: "left"\|"right"\|"middle", count: 1\|2, display?, x?, y?}` | `{}` (moves first if a position is given) |
| `mouse.down` / `mouse.up` | `{button}` | `{}` (drag support) |
| `mouse.scroll` | `{dx, dy}` pixels | `{}` |
| `key.text` | `{text}` | `{}` — types the string (Unicode), `\n` = Enter, `\t` = Tab |
| `key.press` | `{key, mods?}` | `{}` — down+up |
| `key.down` / `key.up` | `{key}` | `{}` |
| `media` | `{action: "playPause"\|"next"\|"previous"\|"volumeUp"\|"volumeDown"\|"mute"}` | `{}` |
| `volume.get` | — | `{level: 0..100, muted}` or error `not_supported` |
| `volume.set` | `{level}` | `{}` |
| `apps.list` | `{icons: bool}` | `[{id, name, iconB64?}]` — installed apps; `id` is opaque (bundle path on macOS, shortcut path or AUMID on Windows); icons are 64 px PNG |
| `apps.running` | — | `[{id, name, pid, active}]` |
| `apps.launch` | `{id}` | `{}` |
| `apps.focus` | `{id?, pid?}` | `{}` |
| `apps.quit` | `{pid, force?}` | `{}` |
| `windows.list` | — | `[{id, title, app, pid, display}]` |
| `windows.focus` | `{id}` | `{}` |
| `power` | `{action: "lock"\|"sleep"\|"displayOff"\|"restart"\|"shutdown"}` | `{}` |
| `clipboard.get` | — | `{text}` |
| `clipboard.set` | `{text}` | `{}` |
| `open.url` | `{url}` | `{}` — `http(s)://` only |
| `ping` | — | `{"t": serverMillis}` |

Errors: `{code, message}` with `code ∈ needs_permission | not_supported |
invalid_params | not_found | internal`. `needs_permission` carries
`details.permission ∈ accessibility | inputMonitoring | screenRecording` so the
phone can tell the user which switch to flip on the laptop.

## 3. Events

| event | data |
|---|---|
| `info` | the `info` object, sent once after connect and whenever permissions change |
| `apps.running` | the running list, pushed on change (≤1/s) |
| `media.state` | `{playing?, title?, artist?}` when the platform exposes it (optional) |

## 4. Binary frames

A binary WebSocket message = `u8 kind` + body. `kind 0x01` = JPEG screen frame:
`u8 display, u16 w, u16 h (big-endian), JPEG bytes`. The phone renders it in an
`<img>`/canvas and maps taps back with `mouse.click {display, x, y}`.

## 5. Agent settings UI (local only)

The agent also hosts its own settings page (WKWebView / WebView2) through the
in-process bridge with: `reach.state` → `{name, port, pairings: [{device, createdAt,
lastSeen}], permissions, peers}`, `reach.settings.set {name, port, launchAtLogin}`,
`reach.pairings.revoke {device}`, `reach.pair.showCode` → `{code, expiresAt}`.

## 6. Phone UI pages (`apps/reach/ui/static`)

1. **Laptops** — the agents known to the phone (first one added by IP, QR or
   `http://<laptop-ip>:7788` opened in the browser; the rest from `/api/peers`),
   with online status and a Pair button.
2. **Remote** — trackpad surface (1-finger move, tap = click, 2-finger drag =
   scroll, 2-finger tap = right-click, long-press = drag), keyboard sheet (hidden
   input for text, row of special keys/arrows/modifiers), media bar
   (prev/play/next/vol−/vol+/mute).
3. **Apps** — grid of installed apps with icons; tap to launch; running apps row
   with focus/quit.
4. **Screen** — live preview; tap to click at that spot; pinch to zoom.
5. **Power** — lock / sleep / display off / restart / shutdown with confirm.

The PWA has a `manifest.webmanifest`, a service worker caching the static files,
`display: standalone`, and works offline for the UI (connections are live).
