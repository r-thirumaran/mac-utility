#!/bin/bash
#
# Builds Clipwell.app using only the Command Line Tools (swiftc + codesign).
#
# This exists because `xcodebuild` requires a full Xcode install, and Clipwell needs no
# Xcode-specific build machinery: no asset catalogs, no storyboards, no code generation —
# just Swift sources, an Info.plist and an ad-hoc signature. The .xcodeproj in the repo is
# the equivalent path once Xcode is installed; both produce the same bundle.
#
# Usage: Tools/build.sh [debug|release]

set -euo pipefail

CONFIG="${1:-release}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$ROOT/build"
APP="$BUILD_DIR/Clipwell.app"
DEPLOYMENT_TARGET="15.0"

SDK="$(xcrun --show-sdk-path --sdk macosx)"
ARCH="$(uname -m)"

case "$CONFIG" in
  release) SWIFT_FLAGS=(-O) ;;
  debug)   SWIFT_FLAGS=(-Onone -g) ;;
  *) echo "usage: $0 [debug|release]" >&2; exit 2 ;;
esac

echo "==> Building Clipwell ($CONFIG, $ARCH, deployment target $DEPLOYMENT_TARGET)"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# Every .swift under Clipwell/ is a source file; there is no exclusion list to keep in sync.
SOURCES=()
while IFS= read -r -d '' file; do
  SOURCES+=("$file")
done < <(find "$ROOT/Clipwell" -name '*.swift' -print0 | sort -z)

echo "    ${#SOURCES[@]} source files"

swiftc \
  "${SWIFT_FLAGS[@]}" \
  -swift-version 5 \
  -target "${ARCH}-apple-macos${DEPLOYMENT_TARGET}" \
  -sdk "$SDK" \
  -module-name Clipwell \
  -o "$APP/Contents/MacOS/Clipwell" \
  "${SOURCES[@]}"

cp "$ROOT/Support/Info.plist" "$APP/Contents/Info.plist"
cp "$ROOT/Support/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
printf 'APPL????' > "$APP/Contents/PkgInfo"

# Ad-hoc signature. A real identity would be better — see README, "Rebuilds and the
# Accessibility permission" — but none is required for a personal build.
IDENTITY="-"
if security find-identity -v -p codesigning 2>/dev/null | grep -q "Apple Development"; then
  IDENTITY="$(security find-identity -v -p codesigning \
    | grep "Apple Development" | head -1 | awk '{print $2}')"
  echo "==> Signing with Apple Development identity $IDENTITY"
else
  echo "==> Signing ad-hoc (no Apple Development identity found)"
fi

codesign --force --sign "$IDENTITY" --timestamp=none "$APP"
codesign --verify --verbose=1 "$APP" 2>&1 | sed 's/^/    /'

echo "==> Built $APP"
