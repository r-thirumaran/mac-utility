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

The Swift menu-bar agent lives in `apps/reach/mac` and implements the same API;
see its section once it lands.
