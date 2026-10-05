#!/bin/bash
#
# Generic macOS app builder for the apps in this repository (everything except
# Clipwell, which keeps its own Tools/build.sh).
#
#   Tools/build_mac_app.sh <app-dir> [debug|release]
#
# <app-dir> is e.g. apps/vitals. It must contain:
#   mac/            Swift sources (recursively)
#   mac/Info.plist  bundle metadata (CFBundleExecutable must equal the app name)
#   mac/AppIcon.icns  (optional) app icon
#   ui/static/      (optional) web UI copied into Contents/Resources/ui
#
# shared/swift/**/*.swift is compiled into every app.
#
# Only the Command Line Tools are required (swiftc + codesign). The result is an
# ad-hoc signed bundle at build/<AppName>.app under the repo root.

set -euo pipefail

APP_DIR="${1:?usage: build_mac_app.sh <app-dir> [debug|release]}"
CONFIG="${2:-release}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$(cd "$ROOT/$APP_DIR" 2>/dev/null && pwd || cd "$APP_DIR" && pwd)"
PLIST="$APP_DIR/mac/Info.plist"
DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-14.0}"

[[ -f "$PLIST" ]] || { echo "missing $PLIST" >&2; exit 1; }
APP_NAME="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$PLIST")"
BUILD_DIR="$ROOT/build"
APP="$BUILD_DIR/$APP_NAME.app"

SDK="$(xcrun --show-sdk-path --sdk macosx)"
case "$CONFIG" in
  release) SWIFT_FLAGS=(-O) ;;
  debug)   SWIFT_FLAGS=(-Onone -g) ;;
  *) echo "usage: $0 <app-dir> [debug|release]" >&2; exit 2 ;;
esac

SOURCES=()
while IFS= read -r -d '' f; do SOURCES+=("$f"); done \
  < <(find "$APP_DIR/mac" "$ROOT/shared/swift" -name '*.swift' -not -path '*/Tests/*' -print0 | sort -z)

echo "==> Building $APP_NAME ($CONFIG, ${#SOURCES[@]} sources, target macOS $DEPLOYMENT_TARGET)"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

ARCHS=(${ARCHS:-arm64 x86_64})
BINS=()
for arch in "${ARCHS[@]}"; do
  out="$BUILD_DIR/$APP_NAME-$arch"
  swiftc "${SWIFT_FLAGS[@]}" -swift-version 5 \
    -target "${arch}-apple-macos${DEPLOYMENT_TARGET}" -sdk "$SDK" \
    -module-name "$APP_NAME" -parse-as-library \
    -o "$out" "${SOURCES[@]}"
  BINS+=("$out")
done
if [[ ${#BINS[@]} -gt 1 ]]; then
  lipo -create "${BINS[@]}" -output "$APP/Contents/MacOS/$APP_NAME"
  rm -f "${BINS[@]}"
else
  mv "${BINS[0]}" "$APP/Contents/MacOS/$APP_NAME"
fi

cp "$PLIST" "$APP/Contents/Info.plist"
[[ -f "$APP_DIR/mac/AppIcon.icns" ]] && cp "$APP_DIR/mac/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
if [[ -d "$APP_DIR/ui/static" ]]; then
  mkdir -p "$APP/Contents/Resources/ui"
  cp -R "$APP_DIR/ui/static/." "$APP/Contents/Resources/ui/"
fi
if [[ -d "$ROOT/shared/ui" ]]; then
  mkdir -p "$APP/Contents/Resources/ui/shared"
  cp -R "$ROOT/shared/ui/." "$APP/Contents/Resources/ui/shared/"
fi
printf 'APPL????' > "$APP/Contents/PkgInfo"

IDENTITY="${CODESIGN_IDENTITY:--}"
ENTITLEMENTS="$APP_DIR/mac/Entitlements.plist"
if [[ -f "$ENTITLEMENTS" ]]; then
  codesign --force --sign "$IDENTITY" --timestamp=none --entitlements "$ENTITLEMENTS" "$APP"
else
  codesign --force --sign "$IDENTITY" --timestamp=none "$APP"
fi
codesign --verify --verbose=1 "$APP" 2>&1 | sed 's/^/    /'
echo "==> Built $APP"
