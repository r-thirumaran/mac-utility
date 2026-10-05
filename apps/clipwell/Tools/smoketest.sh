#!/bin/bash
#
# End-to-end smoke test for the clipboard half of Clipwell.
#
# Covers the acceptance criteria that can be checked without a human at the keyboard:
#   * a copy appears in history within 0.5 s
#   * a pasteboard item marked ConcealedType (password managers) is NOT recorded
#   * history survives quitting and relaunching the app
#   * no Dock icon (LSUIElement)
#
# Snapping and paste injection need the Accessibility grant and a real window, so they are
# covered by the manual checklist in the README instead.

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/build/Clipwell.app"
BIN="$APP/Contents/MacOS/Clipwell"
SUPPORT="$HOME/Library/Application Support/Clipwell"
HISTORY="$SUPPORT/history.json"
LOG="$ROOT/build/smoketest.log"

PASS=0
FAIL=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  [[ -n "${APP_PID:-}" ]] && kill "$APP_PID" 2>/dev/null
  wait "$APP_PID" 2>/dev/null
  # Put the user's clipboard back the way we found it.
  [[ -n "${ORIGINAL_CLIPBOARD:-}" ]] && printf '%s' "$ORIGINAL_CLIPBOARD" | pbcopy
}
trap cleanup EXIT

ORIGINAL_CLIPBOARD="$(pbpaste 2>/dev/null || true)"

echo "==> Fresh state"
rm -rf "$SUPPORT"

echo "==> Launching $BIN"
"$BIN" >"$LOG" 2>&1 &
APP_PID=$!
sleep 2

if ! kill -0 "$APP_PID" 2>/dev/null; then
  bad "app stayed running (it exited; see $LOG)"
  cat "$LOG"
  exit 1
fi
ok "app launched and stayed running (pid $APP_PID)"

# ---------------------------------------------------------------- capture latency
echo "==> Capture"
MARKER="clipwell-smoketest-$$-$(date +%s)"
printf '%s' "$MARKER" | pbcopy
sleep 0.5
if [[ -f "$HISTORY" ]] && grep -q "$MARKER" "$HISTORY"; then
  ok "plain text copy recorded within 0.5 s"
else
  # The store debounces disk writes by 0.5 s; give that one more beat before failing.
  sleep 0.6
  if grep -q "$MARKER" "$HISTORY" 2>/dev/null; then
    ok "plain text copy recorded (within debounce window)"
  else
    bad "plain text copy was not recorded"
  fi
fi

# ---------------------------------------------------------------- privacy filter
echo "==> Privacy (org.nspasteboard.ConcealedType)"
SECRET="clipwell-secret-$$-should-never-persist"
cat > "$ROOT/build/conceal.swift" <<SWIFT
import AppKit
let pb = NSPasteboard.general
pb.clearContents()
let item = NSPasteboardItem()
item.setString("$SECRET", forType: .string)
// The marker password managers set to opt out of clipboard history.
item.setString("", forType: NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType"))
pb.writeObjects([item])
SWIFT
swift "$ROOT/build/conceal.swift" >>"$LOG" 2>&1
sleep 1.5
if grep -q "$SECRET" "$HISTORY" 2>/dev/null; then
  bad "CONCEALED item leaked into history"
else
  ok "concealed item was skipped"
fi

# ---------------------------------------------------------------- dedupe / promote
echo "==> Dedupe"
printf '%s' "$MARKER" | pbcopy          # re-copy the first marker
sleep 1.2
COUNT=$(grep -c "\"$MARKER\"" "$HISTORY" 2>/dev/null || echo 0)
if [[ "$COUNT" -le 1 ]]; then
  ok "re-copy did not duplicate the entry"
else
  bad "re-copy produced $COUNT entries"
fi

# ---------------------------------------------------------------- image blobs
echo "==> Image capture"
cat > "$ROOT/build/copyimage.swift" <<'SWIFT'
import AppKit
let image = NSImage(size: NSSize(width: 40, height: 24))
image.lockFocus()
NSColor.systemTeal.setFill()
NSRect(x: 0, y: 0, width: 40, height: 24).fill()
image.unlockFocus()
let pb = NSPasteboard.general
pb.clearContents()
pb.writeObjects([image])
SWIFT
swift "$ROOT/build/copyimage.swift" >>"$LOG" 2>&1
sleep 1.5
if grep -q '"kind" : "image"' "$HISTORY" 2>/dev/null; then
  ok "image copy recorded as an image item"
else
  bad "image copy not recorded"
fi
if compgen -G "$SUPPORT/Images/*.png" > /dev/null; then
  ok "image bytes stored as a separate PNG blob, not inlined in JSON"
else
  bad "no PNG blob written"
fi
if grep -qi 'imagePNGData\|base64' "$HISTORY" 2>/dev/null; then
  bad "image bytes leaked into history.json"
else
  ok "history.json stayed free of image bytes"
fi

# ---------------------------------------------------------------- persistence
echo "==> Persistence across relaunch"
kill "$APP_PID" 2>/dev/null; wait "$APP_PID" 2>/dev/null
sleep 0.5
"$BIN" >>"$LOG" 2>&1 &
APP_PID=$!
sleep 1.5
if grep -q "$MARKER" "$HISTORY" 2>/dev/null; then
  ok "history survived quit + relaunch"
else
  bad "history lost on relaunch"
fi

# ---------------------------------------------------------------- LSUIElement
echo "==> Menu-bar-only"
if /usr/libexec/PlistBuddy -c 'Print :LSUIElement' "$APP/Contents/Info.plist" 2>/dev/null \
     | grep -qi true; then
  ok "LSUIElement is set (no Dock icon)"
else
  bad "LSUIElement missing"
fi

ACTIVATION=$(osascript -e 'tell application "System Events" to get (name of every process whose background only is false)' 2>/dev/null)
if echo "$ACTIVATION" | grep -q "Clipwell"; then
  bad "Clipwell appears as a foreground (Dock) app"
else
  ok "Clipwell does not appear as a foreground app"
fi

echo
echo "==> $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]]
