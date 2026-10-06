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

The macOS app (`apps/edgehop/mac`, Swift, built by `Tools/build_mac_app.sh`)
lives in the menu bar. The menu has **Open Edgehop**, **Switch to ▸** (every
paired machine plus *This Mac*), **Launch at Login** and **Quit**; while the
pointer is on another machine the menu bar shows `⌨ <that machine>`.

Config (pairing keys, layout, settings): `~/Library/Application Support/Edgehop/`
(`config.json` and `identity.json`, both 0600, the same shape the Windows app
writes). Logs go to the unified log (subsystem `com.thiru.edgehop`; the window's
Log panel shows the same lines). The build is ad-hoc signed: see
`dist/READ-ME-FIRST.txt` for the first-launch steps.

### Permissions

Edgehop needs two permissions. It asks for both on the first launch (macOS
shows each system prompt only once per app version) and shows a red badge in
the window's status strip with an **Open Settings** button for each one still
missing. Until they are granted the Mac can see its own mouse but cannot hand
the keyboard over or receive input.

| Permission | System Settings ▸ Privacy & Security ▸ … | Why |
|---|---|---|
| **Accessibility** | Accessibility | to inject the other machine's input here (pointer, clicks, keys) and to swallow local input while the pointer is on another machine |
| **Input Monitoring** | Input Monitoring | to see this Mac's keyboard so it can follow the pointer to another machine (and for the Ctrl+Alt hotkeys) |

Walkthrough:

1. Start Edgehop. Two system dialogs appear ("Edgehop would like to control
   this computer using accessibility features", "… would like to receive
   keystrokes from any application"). Click **Open System Settings** on each.
2. In **Accessibility**, turn on the switch next to Edgehop. If the app is not
   listed, click **+** and pick it from where you installed it.
3. In **Input Monitoring**, do the same. macOS may say the app must be quit
   and reopened: do that.
4. Back in Edgehop the badges disappear within a couple of seconds; the Log
   panel reports "full tap installed (mouse + keyboard)". If a badge stays,
   quit Edgehop from the menu bar and start it again — macOS does not always
   apply a fresh Input Monitoring grant to the running process.

**After rebuilding or updating:** the build is ad-hoc signed, so every new
binary has a different signature and macOS treats it as a different app.
Both permissions have to be granted again: remove the old Edgehop entry in
each pane (select it, press **−**), add the new one, and restart the app. The
same happens if the app is moved to another folder.

### Caveats

* **Secure input.** When a password field (or Terminal with *Secure Keyboard
  Entry*, or a login prompt) has the keyboard, macOS blocks synthetic
  keystrokes. Edgehop notices, stops injecting keys, and tells the machine
  that is sending input, which shows "*name*: secure input, keys blocked" in
  its status strip. Type the password on the Mac's own keyboard; everything
  resumes when the field loses focus. The pointer is not affected.
* **Cursor visibility while the pointer is elsewhere.** While another machine
  has control, the Mac parks its own cursor at the centre of the primary
  display and stops it from moving; hiding it uses `CGDisplayHideCursor`,
  which macOS only honours for the frontmost application. Without private
  APIs the parked arrow may therefore stay visible on some systems. It is
  harmless — it does not move and comes back to life the moment control
  returns.
* **Hot Corners.** Switching is suppressed within the corner dead zone
  (32 px by default, Settings ▸ Corner dead zone), so Hot Corners and the
  window buttons stay reachable.
* **Granting a permission does not relaunch anything.** The event tap upgrades
  itself when a permission appears, but if macOS has already denied the tap
  once (typically Input Monitoring) a restart of Edgehop is needed.

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
