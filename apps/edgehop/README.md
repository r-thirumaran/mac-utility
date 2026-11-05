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
* **Layout.** The Layout page is a to-scale canvas of every screen of every
  machine: built-in displays and external monitors, Macs and PCs alike. Each
  machine's monitors move together and keep the arrangement from that
  machine's own Display settings. Drag the machines so their screens touch
  where they sit on your desk; edges snap together. The touching stretches
  are highlighted: those are the boundaries. The pointer crosses to another
  machine only there, and arrives at the same height on the other screen.
  Anywhere else, the edge stops the pointer as before. Add a third machine
  later and it appears on the canvas, ready to place. The layout is shared:
  edit it on any machine and every other one gets it.
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
* **Lost the pointer?** Shake the mouse left and right quickly: a ring
  appears around the pointer on whichever machine it is on (Settings ▸
  *Shake the mouse to find the pointer*).
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
| Send trackpad gestures to the other computer | Recognise Magic Trackpad gestures while the pointer is on another machine and send that machine's own shortcut instead of acting here. On by default; see [Trackpad gestures](#trackpad-gestures). |
| Launch at login | Start Edgehop when you sign in (Windows: `HKCU\…\Run`; macOS: Login Items). |
| Port | The TCP port other machines connect to (default 24800; 0 = any free port). Changing it takes effect after a restart. |

## Trackpad gestures

A trackpad gesture never travels as itself. macOS resolves a three-finger
swipe or a pinch inside the window server, and the other machine has no
trackpad to replay it on, so without help a swipe switched desktops on the Mac
with the trackpad while you were working on the other computer. With
*Send trackpad gestures to the other computer* on (the default), the Mac
recognises the finished gesture, swallows it locally and sends the keyboard
shortcut — or the Ctrl+wheel notch — that does the same thing on the machine
in control.

It only ever applies while this Mac is **forwarding**. When the Mac itself has
control every gesture passes through untouched and macOS handles it as always.

### What already worked, without translation

Two-finger scrolling, tap and click, right-click (two-finger or corner click),
two-finger drag and click-and-drag all travel as plain mouse events and arrive
on either platform. Nothing below changes them.

### What is translated

One shortcut per completed gesture — a swipe fires once when your fingers
lift, not once per frame.

| Gesture on the trackpad | Mac in control gets | Windows PC in control gets |
|---|---|---|
| Three/four-finger swipe left | Ctrl+← (previous desktop) | Win+Ctrl+← (previous virtual desktop) |
| Three/four-finger swipe right | Ctrl+→ (next desktop) | Win+Ctrl+→ (next virtual desktop) |
| Three/four-finger swipe up | Ctrl+↑ (Mission Control) | Win+Tab (Task View) |
| Three/four-finger swipe down | Ctrl+↓ (App Exposé) | *nothing — Windows has no equivalent* |
| Pinch open / closed | ⌘= / ⌘− per quarter of magnification | Ctrl held + one wheel notch up/down |
| Force click (Force Touch, stage 2) | Ctrl+⌘+D (look up) | *nothing — Windows has no Force Touch* |

The chords arrive exactly as listed whatever *Swap ⌘ and Ctrl* is set to:
Edgehop undoes that swap for translated gestures, so Win+Tab stays Win+Tab.

### What is not supported

* **Rotate** and **smart zoom** (two-finger double tap). No shortcut on either
  platform does the same thing, so they are dropped.
* **Show Desktop** (thumb-and-three-fingers spread) and **Launchpad** (pinch
  with thumb and three fingers). macOS does not hand a tap a reading this code
  can tell apart from the other Dock gestures, so rather than guess and switch
  the wrong desktop, Edgehop leaves them alone. Both chords are already
  written (F11 on a Mac, Win+D on Windows) and will be wired up if a reliable
  reading turns up.
* **Anything on the other machine's own trackpad or mouse.** Translation runs
  on the machine holding the trackpad; it does not give a PC's touchpad Mac
  gestures.
* **Windows and Linux senders.** This is macOS-only capture. A gesture made on
  a Windows touchpad is handled by Windows.

### Checking it on your hardware

Turn on the debug log (Log tab) and make a gesture while the pointer is on the
other machine. Each translated gesture writes one line:

```
gesture: swipe left → Ctrl+Left on Studio
gesture: pinch open → Ctrl+wheel up on Work PC
gesture: swipe down → nothing on windows
```

If left and right come out the other way round on your trackpad, macOS signs
the swipe progress the other way: flip `positiveProgressIsRightOrUp` in
`mac/Input/GestureTranslator.swift`. Nothing about the gesture other than its
name and the chord name is ever logged.

## Webcams, microphones and headsets

Edgehop moves only the mouse and the keyboard. USB devices stay with the
computer they are physically plugged into: a webcam on the hub attached to the
PC is still the PC's webcam when you are typing on the Mac, and the headset
plays the audio of whichever machine it is connected to. So plug the camera
and the headset into the hub or monitor that is wired to the machine you take
calls on, or use a monitor with a built-in KVM switch for those devices. If
you need the camera on both machines, give each one a camera.

## Windows

### Install and first run (required steps)

Needs **Windows 10 or 11, 64-bit**. Edgehop is a single exe
(`Edgehop-<version>-windows-amd64.exe`, built with `make exe-edgehop`): no
installer, no service, no administrator rights, nothing written outside your
profile. Put it somewhere permanent, e.g. `%LOCALAPPDATA%\Programs\Edgehop\`.

1. **Unblock the download.** The exe is not code-signed, so Windows
   SmartScreen says *"Windows protected your PC"*: click **More info ▸ Run
   anyway**. Or unblock it once in PowerShell:
   ```powershell
   Unblock-File "$env:LOCALAPPDATA\Programs\Edgehop\Edgehop.exe"
   ```
2. **Allow the firewall prompt** on the first start, for **Private
   networks** only (home or office LAN). Without it other machines cannot
   connect *to* this PC. Set your Wi-Fi to *Private* in Settings ▸ Network &
   internet ▸ (your network) ▸ Network profile type, or the rule does not
   apply. Missed the prompt? Run once in an **administrator** PowerShell:
   ```powershell
   New-NetFirewallRule -DisplayName "Edgehop" -Direction Inbound -Program "$env:LOCALAPPDATA\Programs\Edgehop\Edgehop.exe" -Action Allow -Profile Private
   ```
3. **Find it in the notification area** (the ^ by the clock), not the
   taskbar: **Open Edgehop**, **Launch at login**, **Switch to …**, **Quit**.
4. **Pair** from the Mac or the PC (Machines ▸ pick it, or **Pair by
   address…** with the `IP:24800` the PC shows) and type the six-digit code.
5. **Place the PC's monitors** on the Layout canvas so they touch the Mac's
   screens, then **Save**.
6. In **Settings** keep **Swap ⌘ and Ctrl** on: ⌘C on a Mac keyboard then
   arrives as Ctrl+C on the PC.

There is **no permission reset on Windows**: unlike macOS, an update keeps
working. To update: **Quit** from the tray menu, replace the exe, start it.

Required notes:

* **The window** is a WebView2 view (built into Windows 10/11). Without the
  WebView2 runtime the UI opens in your default browser instead.
* **Administrator windows** (an installer, Task Manager run as admin, the UAC
  prompt): Windows does not let a normal app send input to them, so keys and
  clicks from the Mac are swallowed while such a window has focus. Use the
  PC's own keyboard for that moment, or run Edgehop as administrator.
* **The lock screen and Ctrl+Alt+Del** cannot be driven remotely (Windows
  blocks injected input there). Sign in with the PC's own keyboard.
* **Trackpad gestures** from a Mac arrive as their Windows shortcuts
  (Win+Ctrl+←/→, Win+Tab, Ctrl+scroll); see [Trackpad gestures](#trackpad-gestures).
* **Work-managed PCs** (Intune, AppLocker, Defender for Endpoint) may block an
  unsigned exe, its firewall rule, or input hooks; then IT has to allow it.
* Only one instance runs per user; starting a second one just exits.
* Config (pairing keys, layout, settings): `%APPDATA%\Edgehop\config.json`
  and `identity.json`. Delete that folder to unpair everything and start over.
  Log: `%LOCALAPPDATA%\Edgehop\edgehop.log` (5 MB, one backup).

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

### Install on another Mac (and fix "it won't open")

Needs **macOS 14 (Sonoma) or later**; the app runs natively on Apple silicon
and Intel. Run these in Terminal on the Mac you are installing on:

```bash
# 1. Download the disk image (no GitHub sign-in needed)
curl -L -o ~/Downloads/Edgehop-0.1.0.dmg \
  https://github.com/r-thirumaran/mac-utility/releases/download/edgehop-v0.1.0/Edgehop-0.1.0.dmg

# 2. Quit the old copy, then mount, copy the app to /Applications, unmount
pkill -x Edgehop; sleep 2; rm -rf /Applications/Edgehop.app
hdiutil attach ~/Downloads/Edgehop-0.1.0.dmg -nobrowse -mountpoint /tmp/edgehop-dmg
cp -R /tmp/edgehop-dmg/Edgehop.app /Applications/
hdiutil detach /tmp/edgehop-dmg

# 3. Let macOS run it: clear the download quarantine and re-sign it for this Mac
xattr -cr /Applications/Edgehop.app
codesign --force --deep --sign - /Applications/Edgehop.app

# 4. Clear the permissions macOS remembers for the OLD build, then start it
tccutil reset Accessibility com.thiru.edgehop
tccutil reset ListenEvent com.thiru.edgehop
open /Applications/Edgehop.app
```

Step 3 lets the app open. The build is ad-hoc signed (not notarised by
Apple), so without it macOS says the app "could not be verified", "is
damaged and can't be opened", or silently refuses to start it.

**Step 4 is required after every install or update.** macOS ties
Accessibility and Input Monitoring to the exact build. After an update the
switch in System Settings can still show **on** while it belongs to the old
build, and Edgehop shows *Accessibility not granted*: the pointer can leave
this Mac but nothing can control it. `tccutil reset` clears Edgehop's entries
only (no other app is touched).

Then grant the two permissions:

1. macOS shows two prompts ("control this computer", "receive keystrokes"):
   click **Open System Settings** on each.
2. In **System Settings ▸ Privacy & Security** turn **Edgehop** on under
   **Accessibility** and **Input Monitoring** (on newer macOS both are in one
   list, **Device Control and Data Access**). No prompt? Click **+** and pick
   `/Applications/Edgehop.app`.
3. Quit Edgehop from its menu-bar icon and start it again
   (`open /Applications/Edgehop.app`). The red badge in its window disappears.
   Pairings and the layout are kept.

If `open` prints `error -600`, the old copy was still quitting: run
`open /Applications/Edgehop.app` again.

**It still looks like nothing happened?** Edgehop has **no Dock icon and no
window on start**: it is the icon in the menu bar at the top right (on a
crowded or notched menu bar it can be hidden behind the notch, so quit a few
other menu-bar apps or use a menu-bar manager). Click it ▸ **Open Edgehop**.

Check what is going on:

| Check | Command | What you want to see |
|---|---|---|
| macOS version | `sw_vers -productVersion` | 14.0 or higher. Older macOS cannot run this build. |
| Is it running? | `pgrep -lx Edgehop` | a process id and `Edgehop`. If yes, the app is up: look in the menu bar. |
| Is it listening? | `lsof -nP -iTCP:24800 -sTCP:LISTEN` | a line with `Edgehop … TCP *:24800 (LISTEN)`. |
| Signature OK? | `codesign --verify --deep --strict /Applications/Edgehop.app && echo OK` | `OK`. If not, repeat step 3. |
| Quarantine gone? | `xattr -l /Applications/Edgehop.app` | no `com.apple.quarantine` line. |
| Why it quit | `log show --last 5m --predicate 'process == "Edgehop" OR subsystem == "com.thiru.edgehop"' \| tail -40` | the error macOS or Edgehop logged. |
| Run in the foreground | `/Applications/Edgehop.app/Contents/MacOS/Edgehop` | errors printed straight to Terminal (Ctrl+C to stop). |

Still blocked:

* **System Settings ▸ Privacy & Security** shows *"Edgehop" was blocked*:
  click **Open Anyway**, enter your password, then open the app again.
* **A work-managed Mac** (company VPN, Jamf/Intune/Kandji, CrowdStrike,
  Santa, etc.) may refuse unsigned apps or the Accessibility and Input
  Monitoring permissions whatever you do locally. If `pgrep` shows nothing
  right after `open` and the log mentions a policy or "not allowed", the
  block comes from your IT department: ask them to allow the app.
* **Started from an old copy**: `pkill -x Edgehop`, delete
  `/Applications/Edgehop.app`, and repeat steps 2–4. Only one Edgehop runs
  per user, so a stale copy makes the new one quit at once.

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
* **Gesture capture needs Accessibility, not Input Monitoring.** Trackpad
  gestures ride with the mouse events, so they are captured and swallowed as
  soon as Accessibility is granted, even while Input Monitoring is still
  pending. See [Trackpad gestures](#trackpad-gestures).
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
