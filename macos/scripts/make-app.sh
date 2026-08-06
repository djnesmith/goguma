#!/bin/bash
#
# Assembles WakeGuard.app around the SwiftPM executable.
#
# The binary already behaves as a menu bar app on its own — the delegate calls
# setActivationPolicy(.accessory) at launch — so this bundle exists for the
# things a loose executable can't have: a real bundle identifier, an
# Info.plist with LSUIElement, and a launchable target for `open` and Login
# Items.
#
# Usage:  scripts/make-app.sh [debug|release]     (default: release)

set -euo pipefail

CONFIG="${1:-release}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="WakeGuard"
BUNDLE_ID="glass.wakeguard.ui"
VERSION="0.1.0"

cd "$ROOT"

echo "Building ($CONFIG)…"
swift build -c "$CONFIG"

BIN="$(swift build -c "$CONFIG" --show-bin-path)/WakeGuardUI"
if [[ ! -x "$BIN" ]]; then
    echo "error: built binary not found at $BIN" >&2
    exit 1
fi

APP="$ROOT/build/$APP_NAME.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$BIN" "$APP/Contents/MacOS/$APP_NAME"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>                  <string>$APP_NAME</string>
    <key>CFBundleDisplayName</key>           <string>$APP_NAME</string>
    <key>CFBundleExecutable</key>            <string>$APP_NAME</string>
    <key>CFBundleIdentifier</key>            <string>$BUNDLE_ID</string>
    <key>CFBundlePackageType</key>           <string>APPL</string>
    <key>CFBundleShortVersionString</key>    <string>$VERSION</string>
    <key>CFBundleVersion</key>               <string>$VERSION</string>
    <key>LSMinimumSystemVersion</key>        <string>26.0</string>
    <!-- Menu bar app: no Dock icon, no app switcher entry. The delegate also
         sets this at runtime so a loose binary behaves the same way. -->
    <key>LSUIElement</key>                   <true/>
    <key>NSHighResolutionCapable</key>       <true/>
</dict>
</plist>
PLIST

# Ad-hoc signature. Enough for local running; replace with a Developer ID
# identity before distributing.
codesign --force --sign - --timestamp=none "$APP" >/dev/null 2>&1 || {
    echo "warning: ad-hoc codesign failed; the app may still run" >&2
}

echo "Built $APP"
echo "Run with:  open '$APP'"
