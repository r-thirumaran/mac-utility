#!/bin/bash
#
# Wrap a built .app in a drag-to-install disk image.
#
#   Tools/make_dmg.sh <path/to/App.app> <output.dmg> [volume name] [readme file]
#
# macOS only (hdiutil). Produces a compressed UDZO image containing the app, an
# /Applications symlink, and an optional read-me.

set -euo pipefail

APP="${1:?usage: make_dmg.sh App.app out.dmg [volname] [readme]}"
OUT="${2:?usage: make_dmg.sh App.app out.dmg [volname] [readme]}"
VOLNAME="${3:-$(basename "${APP%.app}")}"
README="${4:-}"

STAGE="$(mktemp -d "${TMPDIR:-/tmp}/dmg-stage.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT

cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
if [[ -n "$README" && -f "$README" ]]; then
  cp "$README" "$STAGE/$(basename "$README")"
fi

rm -f "$OUT"
mkdir -p "$(dirname "$OUT")"
hdiutil create -volname "$VOLNAME" -srcfolder "$STAGE" -ov -format UDZO "$OUT" >/dev/null
echo "==> $OUT ($(du -h "$OUT" | cut -f1))"
