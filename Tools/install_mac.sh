#!/bin/bash
#
# One-shot build + install for this Mac.
#
#   Tools/install_mac.sh            build the four apps, install them to /Applications,
#                                   make Clipwell start at login, open the apps
#   Tools/install_mac.sh --no-open  same, but do not launch the apps afterwards
#
# Needs: Xcode Command Line Tools (xcode-select --install). Go is optional and only
# used to cross-compile the Windows executables (brew install go).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OPEN_APPS=1
[[ "${1:-}" == "--no-open" ]] && OPEN_APPS=0

say()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
note() { printf '    %s\n' "$*"; }

if [[ "$(uname)" != "Darwin" ]]; then
  echo "This script runs on macOS." >&2; exit 1
fi
if ! xcode-select -p >/dev/null 2>&1; then
  echo "Xcode Command Line Tools are missing. Run:  xcode-select --install   then re-run this script." >&2
  exit 1
fi

cd "$ROOT"

# ---------------------------------------------------------------- build
say "Building the macOS apps (swiftc, this takes a minute or two)"
make dmg
ls -1 dist-out/*.dmg

if command -v go >/dev/null 2>&1; then
  say "Cross-compiling the Windows executables (for the Windows laptop)"
  make windows || note "Windows build failed; the Mac apps are unaffected."
  ls -1 dist-out/*.exe 2>/dev/null || true
else
  note "Go is not installed; skipping the Windows executables (brew install go, then: make windows)."
fi

# ---------------------------------------------------------------- install
install_app() {   # <App.app path> <bundle id>
  local app="$1" bundle="$2" name dest existing
  name="$(basename "$app")"
  dest="/Applications/$name"
  if [[ -e "$dest" ]]; then
    existing="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$dest/Contents/Info.plist" 2>/dev/null || true)"
    if [[ "$existing" != "$bundle" ]]; then
      echo "refusing to replace $dest: bundle id is '$existing', expected '$bundle'" >&2; return 1
    fi
    pkill -x "${name%.app}" 2>/dev/null || true
    rm -rf "$dest"
  fi
  cp -R "$app" "$dest"
  xattr -dr com.apple.quarantine "$dest" 2>/dev/null || true
  note "installed $dest"
}

say "Installing to /Applications"
install_app "apps/clipwell/build/Clipwell.app" "com.thiru.clipwell"
install_app "build/Vitals.app"  "com.thiru.vitals"
install_app "build/Edgehop.app" "com.thiru.edgehop"
install_app "build/Reach.app"   "com.thiru.reach"

# ---------------------------------------------------------------- Clipwell at login
say "Making Clipwell start at login"
if osascript -e 'tell application "System Events" to get the name of every login item' 2>/dev/null | grep -q "Clipwell"; then
  note "already a login item"
else
  osascript -e 'tell application "System Events" to make login item at end with properties {path:"/Applications/Clipwell.app", hidden:true}' >/dev/null
  note "added as a login item (System Settings ▸ General ▸ Login Items)"
fi

# ---------------------------------------------------------------- current power settings
say "Current power settings (what Vitals' Power tab will show)"
pmset -g custom | sed 's/^/    /' || true

# ---------------------------------------------------------------- launch
if [[ $OPEN_APPS -eq 1 ]]; then
  say "Opening the apps"
  open -a Clipwell; open -a Vitals; open -a Edgehop
fi

cat <<'TXT'

==> Done. What only you can do now (macOS does not let a script grant these):

  Clipwell  System Settings ▸ Privacy & Security ▸ Accessibility      → switch ON
  Edgehop   System Settings ▸ Privacy & Security ▸ Accessibility      → switch ON
            System Settings ▸ Privacy & Security ▸ Input Monitoring   → switch ON
  Vitals    nothing for the monitor; the Power tab asks for your password once per Apply
  Reach     Accessibility + Screen Recording, when you start using the phone app

  Edgehop on the other computers: the office Mac → same DMG (dist-out/Edgehop-0.1.0.dmg);
  the Windows laptop → dist-out/Edgehop-0.1.0-windows-amd64.exe (just run it, allow the
  firewall prompt). Then Edgehop ▸ Machines ▸ Pair (6-digit code), Layout ▸ drag tiles ▸ Save.

  Rebuilt later? macOS ties those permission grants to the ad-hoc signature, so after a
  rebuild remove the app from the list and add it again (or: tccutil reset Accessibility com.thiru.edgehop).
TXT
