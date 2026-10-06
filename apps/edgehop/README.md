# Edgehop

One mouse and one keyboard for several computers. Put a Mac next to a Windows
PC (or two Macs, or three PCs), tell Edgehop which screen sits where, and push
the pointer off the edge of one screen: it appears on the next machine and the
keyboard follows it. There is no server and no "host": every machine runs the
same app and any of them can hand control to any neighbour.

One web UI (`apps/edgehop/ui`) is shared by both platforms; each platform has
its own native backend (Swift on macOS, Go on Windows) that implements the same
bridge methods (`docs/ARCHITECTURE.md` §4–5) and the same wire protocol
(`docs/edgehop-protocol.md`), so a Mac and a PC pair and talk to each other
directly.

## How it works

* **Machines.** Every computer running Edgehop advertises itself on the local
  network (Bonjour, `_edgehop._tcp`). The Machines page lists what it sees;
  machines you have paired show as online/offline, the one that currently has
  the pointer as *Active*.
* **Layout.** The Layout page is a 5 × 5 grid. Drag each machine's tile next to
  the machine it physically sits beside (the tiles are drawn with the real
  shape of each machine's displays). The pointer crosses an edge only where two
  tiles touch, so a machine with nothing to its left simply stops at its left
  edge as before. The layout is shared: edit it on any machine and every other
  one gets it.
* **Hopping.** The active machine watches its own pointer. When it is pushed
  against an outer edge of the desktop that has a neighbour in the layout
  (and no button is held), control moves there: the pointer appears on the
  facing edge of the next machine at the same height, modifier keys that were
  held stay held (so a drag can cross), and from then on everything the mouse
  and keyboard do is sent to that machine over an encrypted connection. The
  machine the mouse is plugged into parks and hides its own pointer meanwhile.
  Pushing the pointer across the second machine's far edge continues to a
  third one.
* **Coming back.** Push the pointer back across the edge, or press
  **Ctrl+Alt+Home** (configurable) on the keyboard: control returns to the
  machine that keyboard is attached to. **Ctrl+Alt+←/→/↑/↓** moves control to
  the neighbour in that direction without touching the mouse. The tray/menu
  bar menu has *Switch to …* entries too.
* **Clipboard.** When control moves, the text clipboard goes with it (switch
  this off in Settings if you prefer).
* **If a machine disappears** (sleep, cable out, crash) while it has the
  pointer, the machine that was sending to it takes control back after a few
  seconds, so you are never stuck looking at a dead screen.

## Pairing, and why there is a code

Two machines will only talk to each other after you have paired them once.
Start the pairing on either side — pick the machine in the list, or type its
address (`host:port`, shown on its Machines page) if the network blocks
Bonjour. The *other* machine then shows a six-digit code for two minutes;
type it on the machine you started from and both remember each other.

The code is not a password that travels over the network. Each machine has a
permanent key pair; the pairing handshake mixes the code into the key exchange
so that the two machines prove to each other that the person typing the code
can see both screens. Anyone on the network who did not see the code cannot
pair, and after pairing every connection is authenticated with the stored keys
and encrypted (X25519 + ChaCha20-Poly1305; details in
`docs/edgehop-protocol.md` §4). Five failed attempts from one machine within
ten minutes are refused. *Unpair* forgets the machine's key; it will have to
pair again.

Only confirm a code on a machine you expected to pair with: whoever is typing
on a paired machine can move your mouse and type on yours.

## Settings

| Setting | What it does |
|---|---|
| Switch delay | How long the pointer must rest against the edge before control moves (0 = instantly). Raise it if you keep hopping by accident. |
| Corner dead zone | Pixels at each end of an edge that never switch, so macOS Hot Corners and the window buttons stay reachable. |
| Swap ⌘ and Ctrl | On when the other machine runs a different OS: ⌘C typed on a Mac keyboard arrives as Ctrl+C on the PC, and Ctrl on a PC keyboard acts as ⌘ on the Mac. Off keeps the physical keys as they are. |
| Sync clipboard | Text clipboard follows the pointer. |
| Hotkey | The modifiers held with ←/→/↑/↓ (move control) and Home (bring it back). |
| Hide the pointer here | Park and hide this machine's pointer while it is on another machine. |
| Launch at login | Start Edgehop when you sign in (Windows: `HKCU\…\Run`; macOS: Login Items). |
| Port | The TCP port other machines connect to (default 24800; 0 = any free port). Changing it takes effect after a restart. |

## Webcams, microphones and headsets

Edgehop moves only the mouse and the keyboard. USB devices stay with the
computer they are physically plugged into: a webcam on the hub attached to the
PC is still the PC's webcam when you are typing on the Mac, and the headset
plays the audio of whichever machine it is connected to. So plug the camera
and the headset into the hub or monitor that is wired to the machine you take
calls on, or use a monitor with a built-in KVM switch for those devices. If
you need the camera on both machines, give each one a camera.

## Windows

### Install

Download `Edgehop-<version>-windows-amd64.exe` (built with `make exe-edgehop`,
which bakes `apps/edgehop/VERSION` into the exe) and run it. Edgehop is a
single exe: no installer, no service, nothing written outside your profile.

* It lives in the notification area. The menu has **Open Edgehop**, **Launch
  at login**, **Switch to …** for every paired machine, and **Quit**.
* The window is an embedded WebView2 view of the UI served on a loopback port
  with a per-run token; without the WebView2 runtime the UI opens in the
  default browser.
* Only one instance runs per user; starting a second one just exits.
* Config (pairing keys, layout, settings): `%APPDATA%\Edgehop\config.json`
  and `identity.json`. Log: `%LOCALAPPDATA%\Edgehop\edgehop.log` (5 MB, one
  backup).

### Permissions

Edgehop runs as a standard user: it reads the mouse and keyboard with
low-level hooks and injects input with `SendInput`, neither of which needs
administrator rights. Two things to know:

* **Windows Defender Firewall** asks whether to allow Edgehop on the first
  start because it listens on a TCP port for the other machines. Allow it on
  **private** networks (your home or office Wi-Fi/LAN); it never needs
  public networks. Without the rule other machines cannot connect *to* this
  PC; this PC can still connect *to* them if they allowed it.
* Windows does not let a normal app send input to windows that run **as
  administrator** (an elevated installer, Task Manager started as admin, the
  UAC prompt itself). While such a window has focus, keys and clicks from the
  other machine are swallowed; use the local keyboard for that moment.

### Command line

```
Edgehop.exe [--name "Desk PC"] [--port 24800] [--peer 192.168.1.20:24800]
            [--no-ui] [--listen 127.0.0.1:0] [--config-dir DIR] [--debug]
```

`--peer` starts a pairing with that address right away (type the code in the
window). `--fake-input` (the default on the Linux test build) replaces the
hooks with an in-memory device that the UI tests drive.

## macOS

The macOS app (`apps/edgehop/mac`, Swift) lives in the menu bar and needs two
permissions, which it asks for on first launch and shows as badges in the
status strip until they are granted:

| Permission | Why |
|---|---|
| **Accessibility** (System Settings ▸ Privacy & Security) | to move the pointer and type on this Mac when another machine sends input |
| **Input Monitoring** | to see the mouse and keyboard on this Mac so it can hand them to another machine |

Config lives in `~/Library/Application Support/Edgehop/`; logs go to the
unified log (subsystem `com.thiru.edgehop`). The build is ad-hoc signed: see
`dist/READ-ME-FIRST.txt` for the first-launch steps.

While a password field has **secure input** on (a macOS feature), keystrokes
sent from another machine are blocked and the sender shows a badge; type the
password on the Mac's own keyboard.

## Developing

```
GOTOOLCHAIN=local go test ./apps/edgehop/...               # state machine, protocol, transport (loopback) tests
GOOS=windows GOARCH=amd64 go vet ./... && go build ./...   # the Windows backend compiles on any OS
node tests/ui/run.mjs 20-edgehop                           # two fake-input instances pair and hop, with screenshots
```

The Linux build (`go run ./apps/edgehop/win --fake-input --no-ui`) serves the
same UI and protocol as the Windows exe with an in-memory input device; the
`edgehop.debug.emit` / `edgehop.debug.injected` bridge methods feed it events
and read back what it injected.
