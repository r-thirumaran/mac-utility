# Reach — control any laptop on your network from your phone

Reach puts a small agent on each laptop and gives your phone a web app that
finds the agents on the local network and drives them: trackpad and keyboard,
launching and switching apps, a live screen preview you can tap, media keys and
volume, lock / sleep / restart / shut down. The agent API both agents implement
is `docs/reach-api.md`.

## Windows agent

`Reach-<version>-windows-amd64.exe` (built by `make exe-reach`) is a single
tray application; there is nothing to install.

1. Run the exe. It appears in the notification area. The first start triggers
   the **Windows Defender Firewall prompt** because the agent listens on
   TCP 7788 for the phone: tick *Private networks* and allow it. If you
   dismissed the prompt, allow the exe later under *Windows Security ▸ Firewall
   & network protection ▸ Allow an app through firewall*.
2. The tray menu has *Open Reach* (the settings window), *Show pairing code*,
   *Launch at login* and *Quit*. The settings window shows the laptop's name,
   the port, the addresses phones can use and a QR code of the first one, the
   paired phones (with *Revoke*), and the pairing code when a phone asks for it.
3. Config lives in `%APPDATA%\Reach\config.json` (0600: it holds the phone
   tokens) and the log in `%LOCALAPPDATA%\Reach\reach.log`.

Command-line flags: `--listen 0.0.0.0:7788`, `--port`, `--name`,
`--config-dir`, `--no-ui` (serve only), `--debug` (log every call) and
`--fake` (the recording backend used by the Linux tests, also handy to try the
phone app without moving the real mouse).

What the agent does on Windows: Start Menu shortcuts are the app list
(launched with ShellExecute), icons come from the shell, windows from
`EnumWindows`, input is injected with `SendInput` (absolute mouse over the
virtual desktop, Unicode typing, scan codes for named keys, virtual keys for the
media keys), the screen preview is a GDI capture scaled and JPEG-encoded at up
to 10 fps, and the power actions use `LockWorkStation`, `SetSuspendState`,
`SC_MONITORPOWER` and `shutdown /s|/r /t 0`. No administrator rights are
needed.

## Phone app

The agent serves the phone app itself. On the phone, on the same Wi-Fi, open

    http://<laptop-ip>:7788

(the address is on the laptop's settings page, with a QR code) and use the
browser's **Add to Home Screen** (Safari: share sheet; Chrome: menu). It then
opens full screen like an app. The Capacitor app (`apps/reach/mobile`, built in
CI) ships the same files and needs no agent to load from.

### Native apps (Android APK, iOS)

The same files are also packaged as native apps by the Capacitor wrapper in
`apps/reach/mobile` (details in `apps/reach/mobile/README.md`). An installed
app keeps its pairings when the browser clears site data, has a proper icon,
vibrates on trackpad taps and understands the Android back button; inside it
the app talks to the agents over the network exactly like the PWA.

* **Android** — every CI run (`.github/workflows/build.yml`, job *Reach Android
  APK*) produces `Reach-<version>-android-debug.apk` as the artifact
  `reach-android-apk`, and tagged releases attach it to the GitHub release.
  Download it on the phone, allow installs from that app when Android asks and
  open the file (or `adb install -r …`). It is a debug build signed with a
  debug key: see the mobile README for keeping successive builds installable
  over each other.
* **iOS** — there is no CI build; on a Mac run `npm ci && npm run ios && npx
  cap open ios` in `apps/reach/mobile` and press Run in Xcode with a personal
  Apple ID (free account: the signature lasts seven days, re-run from Xcode to
  renew). The first laptop connection triggers iOS's *local network* prompt;
  allow it.

Inside the wrapper the Laptops tab starts empty (no agent served the page): add
the first laptop by address, the rest show up under *Nearby*.

### Pairing

Every laptop must be paired once per phone:

1. **Laptops** tab → the laptop you opened the app from is already listed;
   add others by address (`host` or `host:port`), or pick them from *Nearby*
   (agents discover each other over Bonjour and share the list).
2. Tap **Pair**. The laptop shows a 6-digit code (notification + settings
   window + tray). Type it on the phone within two minutes.
3. The phone stores a token for that laptop; the laptop lists the phone under
   *Paired devices*, where it can be revoked at any time. Five wrong codes in
   ten minutes lock pairing for ten minutes.

### Tabs

* **Remote** — the trackpad: one finger moves (with acceleration), tap clicks,
  two-finger drag scrolls, two-finger tap right-clicks, three-finger tap
  middle-clicks, tap-tap-hold drags. *Keyboard* opens a sheet that types into
  the laptop (Esc, Tab, ⌘/Ctrl, Alt, Shift, arrows and the F-keys are buttons;
  modifiers latch for the next key). The media bar sends previous / play-pause
  / next and volume down / mute / up; a slider appears when the agent can
  report the volume.
* **Apps** — the installed apps with icons (tap to launch) and the running ones
  (tap to focus, × to quit).
* **Screen** — a live preview; tap to click there, pinch to zoom.
* **Power** — lock, sleep, display off, restart, shut down (each confirmed).
* **Settings** — the phone's name used for pairing, theme, and on the laptop
  itself the agent's own settings.

### Security caveat: plaintext on the LAN

Version 1 talks plain HTTP/WebSocket on the local network: the pairing token
and everything you type travel unencrypted between phone and laptop. Use it only
on networks you trust (home Wi-Fi), never on public ones, and revoke pairings
you no longer use. Because the page is plain `http://`, browsers do not install
the service worker or grant the wake lock on the LAN address (they do for the
Capacitor app and on `localhost`); the app still works, it just does not cache
itself offline.

## macOS agent

`Reach-<version>.dmg` (built by `make dmg-reach` on a Mac with the Command Line
Tools, or by CI) holds `Reach.app`, a menu-bar agent written in Swift
(`apps/reach/mac`, on top of `shared/swift`). It serves the same phone app on
TCP 7788 and implements `docs/reach-api.md` exactly like the Windows exe, so a
phone cannot tell the two apart beyond `platform: "macos"`.

1. Drag `Reach.app` to `/Applications` and open it. It lives in the menu bar
   (no Dock icon); the first launch opens the settings window and asks for the
   two permissions below. The menu has *Open Reach*, *Show pairing code*,
   *Launch at Login* and *Quit*.
2. **Permissions** (System Settings ▸ Privacy & Security):
   * **Accessibility** — needed to control the Mac at all: every synthetic
     mouse, keyboard, media-key and text event is dropped silently without it.
     A phone that tries gets `needs_permission` and a notice naming the switch.
   * **Screen Recording** — needed for the Screen tab's live preview
     (`screen.frame` / `screen.stream.*`) and for window titles in
     `windows.list` (without it macOS hides other apps' titles and the list
     falls back to app names). Takes effect after Reach is relaunched.
   * **Automation ▸ System Events** is requested on the first *Restart* or
     *Shut down* from a phone (lock, sleep and display-off need nothing extra).
   * **Local Network** — on macOS 15 the first start shows the system's local
     network prompt because the agent advertises itself with Bonjour and
     browses for other agents; allow it, or phones cannot reach the port and
     *Nearby* stays empty.

   The build is ad-hoc signed, so rebuilding or updating the app changes its
   code signature and macOS forgets the grants: switch Reach off and on again
   in the Accessibility and Screen Recording lists after every rebuild.
3. **Port** 7788 by default (changeable on the settings page; the new port
   applies after a restart). The settings window shows the addresses phones
   can open, a QR code, the paired devices (with *Revoke*) and the pairing
   code when a phone asks for it or *Show pairing code* is chosen.
4. **Pairing** works as described under *Phone app* below: the phone asks,
   the Mac shows a six-digit code (notification + settings window) for two
   minutes, five wrong codes lock pairing for ten minutes, and a token per
   device name is stored in `~/Library/Application Support/Reach/config.json`
   (0600). Logs go to unified logging under the `com.thiru.reach` subsystem
   (`log stream --predicate 'subsystem == "com.thiru.reach"'`).

What the agent does on macOS: the app list is a scan of `/Applications`,
`~/Applications` and `/System/Applications` with Finder names and 64 px icons;
running apps come from `NSWorkspace`, windows from the window server and are
raised through the Accessibility API; input is posted as `CGEvent`s (relative
and absolute mouse, pixel scrolling, Unicode typing in 20-character chunks,
virtual key codes from the bundled HID key table, `⌘` for `cmdOrCtrl`); media
keys are system-defined events like a keyboard's; volume uses AppleScript's
`volume settings`; the screen preview is a display capture scaled and
JPEG-encoded at up to 10 fps; lock uses the login framework (falling back to
`CGSession -suspend` and ⌃⌘Q), sleep and display-off use `pmset`, and restart
and shut down ask System Events. Nothing needs an administrator password.

The **plaintext caveat** applies as much on macOS as on Windows: the pairing
token and every keystroke travel as plain HTTP/WebSocket over the LAN, so use
Reach only on networks you trust and revoke devices you no longer use.
