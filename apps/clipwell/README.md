<img src="assets/icon.png" width="128" align="left" alt="Clipwell icon">

# Clipwell — window snapping and clipboard history for macOS

[![Download](https://img.shields.io/github/v/release/r-thirumaran/Clipwell?label=download&style=flat-square)](https://github.com/r-thirumaran/Clipwell/releases/latest)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/macOS-15%2B-lightgrey?style=flat-square)](#requirements)
[![Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen?style=flat-square)](#how-it-works)

**A free, open-source macOS app that brings two things Windows gets right over to the Mac:
Aero Snap window tiling, and `Win+V` clipboard history.** It lives in the menu bar, has no Dock
icon, and uses no third-party code at all.

If you have ever pressed `Win+Left` on a Mac and had nothing happen, or wanted the last ten
things you copied instead of just the last one, that is what this is for.

```
⌃⌘←  snap left half        ⌘⇧V  clipboard history
⌃⌘→  snap right half            search it, arrow through it, Return to paste
⌃⌘↑  maximize
⌃⌘↓  restore, or minimize
```

**[⬇︎ Download the latest release](https://github.com/r-thirumaran/Clipwell/releases/latest)**
· [Install notes](#installing-the-prebuilt-release) · [Build from source](#build-and-install)

<br clear="left">

---

## What it does

**Window snapping — Windows semantics, not macOS tiling.**
Halves, maximize to the screen's usable area (*not* a macOS full-screen Space, which is a
different and more annoying thing), and `Win+Down`-style restore-or-minimize. Press left or
right again on an already-snapped window and it walks to your next display, exactly as
Windows 11 does. Restore returns the window to the precise frame it had before you touched it.

**Clipboard history that survives reboots.**
Text, rich text, images and files. Search as you type, pin the things you paste constantly,
and paste with `Return` or a click — Clipwell re-activates the app you were in and presses
`⌘V` for you. Unlike Spotlight's clipboard, history is on disk and is still there tomorrow.

**It refuses to record your passwords.**
Items marked `org.nspasteboard.ConcealedType` — what password managers set — are skipped
outright, along with transient and auto-generated pasteboard data.

**Nothing leaves your Mac.** No network code, no telemetry, no account. The Accessibility
permission is used for exactly two things: moving windows, and pressing `⌘V` for you.

## Why another one of these

Most Mac window managers are either paid, subscription-based, or bundled into a much larger
launcher. Most clipboard managers are separate apps again. Clipwell is one small menu bar
utility that does both, is MIT licensed, and has **zero third-party dependencies** — it is
AppKit, SwiftUI, Carbon, ApplicationServices, ServiceManagement, CryptoKit and Combine, all
first-party. The whole thing is ~25 Swift files you can read in an afternoon.

If you already use Rectangle, Magnet, Maccy or Raycast and are happy, you do not need this.
It exists for people who want both features, in one place, for free, with the source in front
of them.

## Requirements

macOS 15 (Sequoia) or later. Universal binary — Apple silicon and Intel. Needs the
Accessibility permission; nothing snaps or pastes without it.

---

## Build and install

```bash
make install
```

That builds a Release bundle and copies it to `/Applications`, then prints the Accessibility
instructions. Then launch it and grant the permission — nothing works until you do.

`make` picks a build backend automatically:

| Backend | Used when | Notes |
|---|---|---|
| `xcodebuild` | full Xcode installed | `xcodebuild -project Clipwell.xcodeproj -scheme Clipwell -configuration Release` |
| `Tools/build.sh` | Command Line Tools only | plain `swiftc` + `codesign`; produces the same bundle |

Other targets: `make build`, `make run`, `make stop`, `make test`, `make uninstall`,
`make clean`, `make project`.

### Granting Accessibility

Clipwell needs **System Settings ▸ Privacy & Security ▸ Accessibility** for two things: moving
other apps' windows, and pressing ⌘V for you. Both are the same permission. On first launch it
opens a setup window with a button straight to that pane.

> **Rebuilds and the Accessibility permission.** macOS ties the grant to the app's code
> signature. An ad-hoc signature (`CODE_SIGN_IDENTITY = "-"`) changes on every rebuild, so after
> reinstalling you may need to select Clipwell in the Accessibility list, press **−**, and
> re-add it. If you have an Apple Development certificate, `Tools/build.sh` picks it up
> automatically and the grant then survives rebuilds.

---

## Shortcuts

All configurable in **Settings ▸ Shortcuts**.

| Default | Action |
|---|---|
| `⌃⌘←` | Snap left half |
| `⌃⌘→` | Snap right half |
| `⌃⌘↑` | Maximize to the screen's `visibleFrame` (*not* a macOS full-screen Space) |
| `⌃⌘↓` | Restore if maximized, otherwise minimize |
| `⌘⇧V` | Clipboard history panel |

**Display cycling.** Pressing ← or → again on an already-snapped window walks it across your
displays, as Windows 11 does:

```
press ←   left-half of S   →  right-half of the display left of S
          right-half of S  →  left-half of S
          anything else    →  left-half of the display it is on
press →   mirror image
```

On one display this collapses to a left/right toggle. At the outermost display the window stays
put and beeps, unless you enable **Cycling wraps around** in Settings.

**⌘+Arrow mode** (off by default) additionally binds plain `⌘←`/`⌘→`. It is off for good reason:
those keys normally jump to line start/end and go back/forward in browsers, and Clipwell takes
them over everywhere. The Settings UI warns about this inline.

### Clipboard panel keys

| Key | Action |
|---|---|
| `↑` `↓` | Move selection (walks across the pinned/recent boundary as one list) |
| `↩` | Paste |
| `⇧↩` | Paste as plain text |
| `⌘P` | Pin / unpin |
| `⌘⌫` | Delete item (plain `⌫` also deletes when the search field is empty) |
| `⎋` | Close |

---

## How it works

```
App/                 entry point, AppDelegate wiring, status item, permission window,
                     FrontmostAppTracker
Hotkeys/             Carbon RegisterEventHotKey wrapper, KeyCombo model, recorder control
WindowManagement/    coordinate conversion, AX window wrapper, previous-frame store,
                     the snap state machine
Clipboard/           ClipItem model, store + eviction, changeCount poller, paste injection,
                     floating panel + SwiftUI list
Settings/            preferences (UserDefaults), SwiftUI settings, launch-at-login
Persistence/         Application Support paths, atomic JSON I/O
```

A few decisions worth knowing about when you maintain this:

**Carbon for hot keys, not an event tap.** `RegisterEventHotKey` genuinely *consumes* the
keystroke system-wide, so `⌃⌘←` never leaks into the focused app, and it needs no permission of
its own — hot keys work while you are still in System Settings granting the other one. Global
`NSEvent` monitors are observe-only and would not do.

**The coordinate flip is the whole ballgame.** Cocoa's origin is the bottom-left of the primary
screen; the Accessibility API's is the top-left. `ScreenGeometry` converts with
`axY = primary.maxY - cocoaRect.maxY` — the same arithmetic in both directions. Get this wrong
and windows do not drift slightly, they land on the wrong physical display. "Primary" always
means `NSScreen.screens[0]`, never `NSScreen.main` (which is merely wherever the key window is).

**Size → position → size.** Apps clamp to their own min/max, and some quantise to a character
grid. Setting the size first lets the app clamp at the old origin, the position then lands, and
the second size call re-applies the target on the destination display.

**Window identity uses `AXUIElement` itself**, boxed in a `Hashable` wrapper over
`CFEqual`/`CFHash`. The obvious alternative, `_AXUIElementGetWindow`, is a private symbol; this
stays on public API.

**Previous frames are saved only when leaving an *unmanaged* position.** So
snap-left → snap-right → maximize → `⌃⌘↓` restores to the frame you originally had, not to an
intermediate half.

**`changeCount` polling at 0.3 s is not a compromise** — `NSPasteboard` publishes no change
notification at all. Reading `changeCount` does not touch pasteboard contents, so the poll is
free until something actually changes.

**Image bytes never enter `history.json`.** They live at
`Application Support/Clipwell/Images/<uuid>.png`. Five hundred inlined screenshots would mean
re-encoding a several-hundred-megabyte JSON file on every single copy.

**Hot keys are suspended while recording a shortcut.** Otherwise pressing `⌘⇧V` to re-record it
would just open the history panel — the registered hot key would eat the keystroke before the
recorder saw it.

### Privacy

Items are skipped when the pasteboard carries `org.nspasteboard.ConcealedType` (what password
managers set), `TransientType`, or `AutoGeneratedType`. Nothing ever leaves your Mac; the
Accessibility permission is used only to move windows and to press ⌘V.

---

## Decisions I made that the spec did not cover

- **`⌫` in the panel is overloaded.** The spec asked for `Delete` = remove, but `⌫` also has to
  edit the search field. It deletes the selected item only when unambiguous: the search field is
  empty, or `⌘` is held. Forward-delete (`⌦`) always deletes.
- **Mouse:** a single click pastes, matching `↩` and Windows' Win+V. Rows are `Button`s rather
  than tap-gesture modifiers — stacking `.onTapGesture(count: 2)` and `.onTapGesture()` makes
  SwiftUI arbitrate between them and drop both, which made the list entirely unclickable.
- **Hovering never changes the selection.** It only draws a highlight. Driving selection from
  hover meant the scroll-to-selection handler fired as the pointer moved, so the list scrolled
  itself out from under the mouse.
- **`ClipItem.imagePNGData` is a computed accessor**, not a stored property — see the image
  storage note above. `hasImage` is what gets persisted.
- **⌘+Arrow mode adds bindings rather than replacing them**, so `⌃⌘←` keeps working when it is on.
- **Maximized window with no remembered frame** (it was already maximized before Clipwell
  started) restores to a centred 70% rect, since there is nothing to restore *to*.
- **Pasting does not reorder history.** Re-*copying* moves an item to the top, as specified;
  pasting leaves the list where it was so it does not shuffle under you.
- **"Cycling wraps around" toggle** added, defaulting off to match Windows.
- **The panel activates the app explicitly.** The `.nonactivatingPanel` style mask is set as
  specified — it stops the panel from implicitly activating on order-front — but keyboard focus
  is a separate matter, and a search field that silently swallows typing would be useless. The
  paste target is preserved independently by `FrontmostAppTracker`, which watches
  `didActivateApplicationNotification`, so activating costs nothing. This also fixes a real bug:
  opening a status-bar menu makes Clipwell frontmost, so reading `frontmostApplication` at
  paste time would have returned *us*.
- **Bundle ID is `com.thiru.clipwell`**; retention is swept hourly, not only at launch.

---

## Testing

```bash
make test
```

Ten automated checks (`Tools/smoketest.sh`), all passing:

```
✓ app launched and stayed running
✓ plain text copy recorded within 0.5 s
✓ concealed item was skipped                    (password-manager privacy filter)
✓ re-copy did not duplicate the entry
✓ image copy recorded as an image item
✓ image bytes stored as a separate PNG blob, not inlined in JSON
✓ history.json stayed free of image bytes
✓ history survived quit + relaunch
✓ LSUIElement is set (no Dock icon)
✓ Clipwell does not appear as a foreground app
```

### Manual checklist

These need the Accessibility grant and a human at the keyboard:

- [ ] Snap works in Safari, Finder, VS Code and Terminal
- [ ] On two displays, `⌃⌘←`/`⌃⌘→` cycle to the correct monitor
- [ ] `⌃⌘↑` then `⌃⌘↓` returns the window to its exact previous frame
- [ ] `⌃⌘↓` again minimizes
- [ ] Copy a password from your password manager — it must **not** appear
- [ ] History survives a reboot
- [ ] `⌘⇧V` → type to search → `↩` pastes into the app you were just in
- [ ] Launch at login works after `make install` (enable it *after* installing, so the login
      item points at `/Applications` rather than a build directory)

---

## Known caveats

- **macOS ships its own window tiling shortcuts** (System Settings ▸ Desktop & Dock ▸ Windows,
  and Keyboard Shortcuts). If a default collides on your setup, Clipwell logs it, and shows an
  alert if the collision appears while you are changing shortcuts in Settings. All five defaults
  registered cleanly on macOS 26.5 during testing.
- **Some windows resist exact sizing.** Terminal quantises to a character grid; apps with
  min/max constraints clamp. The size→position→size workaround handles most of it, and such
  windows still land at the correct origin.
- **Non-standard windows are skipped** — sheets, popovers and system dialogs report a subrole
  other than `AXStandardWindow` and are left alone; moving them is visually broken. Windows that
  publish no subrole at all (much of Electron) are treated as eligible.
- **`Tools/generate_xcodeproj.py` is optional.** The `.xcodeproj` is committed; regenerate it
  only after adding or moving source files. It writes deterministic IDs, so a no-op run produces
  a byte-identical file.

---

## Screenshots

macOS puts a screenshot on the clipboard **only** when you add Control:

| Shortcut | What macOS does | Captured by Clipwell |
|---|---|---|
| `⌃⌘⇧3` / `⌃⌘⇧4` | copies to clipboard | **yes**, automatically |
| `⌘⇧3` / `⌘⇧4` / `⌘⇧5` | saves a file to Desktop | no — nothing reaches the clipboard |

So if you want every screenshot in your history, pick one of these.

**Option 1 — swap the shortcuts (no extra permission).** In System Settings ▸ Keyboard ▸
Keyboard Shortcuts ▸ Screenshots, give *"Copy picture of selected area to the Clipboard"* the
plain `⌘⇧4`, and move *"Save picture of selected area as a file"* to `⌃⌘⇧4`. Now your muscle
memory copies to the clipboard and Clipwell picks it up with no new permissions at all. The
trade-off is that screenshots stop auto-saving to the Desktop unless you use the Control
variant.

**Option 2 — let Clipwell watch the folder (opt-in).** Settings ▸ Clipboard ▸ Screenshots ▸
*"watch the screenshots folder and import automatically"*. This catches `⌘⇧3`, `⌘⇧4` and `⌘⇧5`
and still saves the file as normal.

> This one asks macOS for **Files and Folders** access to your Desktop — a permission entirely
> separate from Accessibility. It is off by default deliberately: a clipboard tool requesting
> to read your Desktop deserves an explicit yes rather than a surprise prompt. The consent
> dialog can also appear *behind* other windows or on another display, because Clipwell has no
> Dock icon to anchor it — if the status in Settings says "Waiting for permission", go looking
> for that dialog.

---

## Installing the prebuilt release

Grab the `.dmg` from [Releases](https://github.com/r-thirumaran/Clipwell/releases) and drag
Clipwell to Applications.

### macOS will refuse to open it. This is expected.

You will see one of:

> **"Apple could not verify "Clipwell" is free of malware that may harm your Mac or
> compromise your privacy."**
>
> **"Clipwell is damaged and can't be opened."**

Neither means anything is wrong with the app. macOS says this about *any* app that has not been
through Apple's paid notarisation service ($99/yr); Clipwell is ad-hoc signed, so there is
nothing for Gatekeeper to look up. Get past it either way:

```bash
xattr -dr com.apple.quarantine /Applications/Clipwell.app
```

or, without Terminal: try to open the app once, dismiss the error, then go to **System Settings
▸ Privacy & Security**, scroll to **Security**, and click **Open Anyway** next to the Clipwell
message.

> On macOS 15 and later, right-click ▸ Open **no longer bypasses this** — Apple removed that
> shortcut. Use one of the two methods above.

If you would rather not trust a binary from a stranger on the internet — entirely reasonable —
build it yourself. One command, no dependencies, and the result is signed on your own machine
so Gatekeeper never complains:

```bash
git clone https://github.com/r-thirumaran/Clipwell.git && cd Clipwell && make install
```

Then grant Accessibility as described above.

> **Heads-up on updates.** macOS ties the Accessibility grant to an app's code signature, and an
> ad-hoc signature changes on every build. After installing a new version you may need to remove
> Clipwell from the Accessibility list and re-add it, or run
> `tccutil reset Accessibility com.thiru.clipwell`. This goes away if the project ever moves to
> a Developer ID signature — see [dist/DISTRIBUTION.md](dist/DISTRIBUTION.md).

## Contributing

Issues and pull requests are welcome. This is a personal tool that turned out to be generally
useful, so it is maintained on a best-effort basis.

Before opening a PR:

- `make test` should stay green (10 automated checks, no Xcode required — Command Line Tools are
  enough).
- Anything touching window management needs a manual pass; the checklist is under
  [Testing](#testing). Say in the PR which apps and how many displays you tried.
- Keep the commenting style: explain *why*, especially around the AX coordinate flip and the
  pasteboard polling, since those are the parts that look wrong until you know the reason.
- If you add or move a source file, run `make project` to regenerate the `.xcodeproj`.

Useful things to pick up, roughly in order of value:

- Quarter/third-screen snapping and a configurable grid
- Encrypted-at-rest clipboard history (currently plain JSON — see the privacy note above)
- Excluding chosen apps from clipboard capture by bundle ID
- A proper app icon (there is none; the menu bar uses an SF Symbol)
- Localisation — all strings are currently inline English

## Licence

[MIT](LICENSE). Do what you like with it; no warranty.
