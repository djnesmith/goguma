#!/bin/bash
#
# Assembles goguma.app around the SwiftPM executable.
#
# The binary already behaves as a menu bar app on its own — the delegate calls
# setActivationPolicy(.accessory) at launch — so this bundle exists for the
# things a loose executable can't have: a real bundle identifier, an
# Info.plist with LSUIElement, and a launchable target for `open` and Login
# Items.
#
# The four Go binaries ride along in Contents/Resources/bin. Someone who
# downloads the app has no CLI and no daemon, so without them the app is a
# viewer of something that does not exist: it would show its disconnected
# state forever with no way out. Carrying them means the app can set itself
# up. They are also what makes the download and the `brew install` paths end
# in the same place rather than being two different products.
#
# Usage:  scripts/make-app.sh [debug|release]     (default: release)
#
# Environment:
#   VERSION      version string baked into the bundle and the binaries
#   SIGN_ID      codesigning identity, e.g. "Developer ID Application: Name (TEAM)".
#                Defaults to an ad-hoc signature, which is fine for running the
#                app locally and useless for distributing it: Gatekeeper
#                rejects an ad-hoc signed app that arrived over the network.

set -euo pipefail

CONFIG="${1:-release}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="$(cd "$ROOT/.." && pwd)"
APP_NAME="goguma"
BUNDLE_ID="glass.goguma.ui"
VERSION="${VERSION:-0.1.0}"
SIGN_ID="${SIGN_ID:--}"

cd "$ROOT"

# Universal, unless UNIVERSAL=0.
#
# A plain `swift build` produces a binary for the machine doing the building,
# and release builds run on Apple silicon, so the downloadable app was arm64
# only: an Intel Mac could install it and it would refuse to launch, with no
# explanation anyone could act on. The Homebrew path was fine (goreleaser
# already ships both architectures), which made this invisible to anyone
# testing with brew.
UNIVERSAL="${UNIVERSAL:-1}"
if [[ "$UNIVERSAL" == "1" ]]; then
    ARCH_ARGS=(--arch arm64 --arch x86_64)
    echo "Building the app ($CONFIG, universal)…"
else
    ARCH_ARGS=()
    echo "Building the app ($CONFIG, this machine only)…"
fi

swift build -c "$CONFIG" "${ARCH_ARGS[@]}"

BIN="$(swift build -c "$CONFIG" "${ARCH_ARGS[@]}" --show-bin-path)/GogumaUI"
if [[ ! -x "$BIN" ]]; then
    echo "error: built binary not found at $BIN" >&2
    exit 1
fi

APP="$ROOT/build/$APP_NAME.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/bin"

cp "$BIN" "$APP/Contents/MacOS/$APP_NAME"
cp "$ROOT/assets/goguma.icns" "$APP/Contents/Resources/goguma.icns"

# The four Go binaries, matching the app's architectures.
#
# These ride inside the bundle so the app can set itself up with no CLI
# present. An arm64-only set inside a universal app is the same failure one
# layer down: the app launches on Intel and every command it runs dies.
#
# Named one by one rather than ./cmd/..., which also builds goguma-advisory.
# That one is the maintainer tool that generates and signs the notice feed, it
# is deliberately absent from .goreleaser.yaml, and it has no business inside a
# user's copy of the app.
TOOLS=(./cmd/goguma ./cmd/goguma-daemon ./cmd/goguma-helper ./cmd/goguma-mark)

# Kept in step with .goreleaser.yaml on purpose. The app bundles its own daemon,
# so if only the Homebrew build carried the advisory key then whether you got
# notices would depend on which of the two identical-looking install routes you
# took. Empty is the supported default and is what every release has shipped.
LDFLAGS="-X main.version=$VERSION"
LDFLAGS+=" -X github.com/junnam586/goguma/internal/advisory.publicKeyB64=${GOGUMA_ADVISORY_PUBKEY:-}"

echo "Building the command line tools…"
BINDIR="$APP/Contents/Resources/bin"
if [[ "$UNIVERSAL" == "1" ]]; then
    for arch in arm64 amd64; do
        (cd "$REPO" && GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 \
            CC="clang -arch $( [[ $arch == arm64 ]] && echo arm64 || echo x86_64 )" \
            go build -ldflags "$LDFLAGS" \
            -o "$BINDIR/$arch/" "${TOOLS[@]}")
    done
    for tool in "$BINDIR"/arm64/*; do
        name="$(basename "$tool")"
        lipo -create "$BINDIR/arm64/$name" "$BINDIR/amd64/$name" -output "$BINDIR/$name"
    done
    rm -rf "$BINDIR/arm64" "$BINDIR/amd64"
else
    (cd "$REPO" && go build -ldflags "$LDFLAGS" \
        -o "$BINDIR/" "${TOOLS[@]}")
fi

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>                  <string>$APP_NAME</string>
    <key>CFBundleDisplayName</key>           <string>$APP_NAME</string>
    <key>CFBundleExecutable</key>            <string>$APP_NAME</string>
    <key>CFBundleIconFile</key>              <string>goguma</string>
    <key>CFBundleIdentifier</key>            <string>$BUNDLE_ID</string>
    <key>CFBundlePackageType</key>           <string>APPL</string>
    <key>CFBundleShortVersionString</key>    <string>$VERSION</string>
    <key>CFBundleVersion</key>               <string>$VERSION</string>
    <!-- Must match Package.swift's platforms setting. A bundle claiming a
         higher minimum than the code needs is refused by Finder on Macs that
         would have run it perfectly well. -->
    <key>LSMinimumSystemVersion</key>        <string>14.0</string>
    <!-- Menu bar app: no Dock icon, no app switcher entry. The delegate also
         sets this at runtime so a loose binary behaves the same way. -->
    <key>LSUIElement</key>                   <true/>
    <!-- Declared even though setup now opens a .command file rather than
         scripting Terminal. Without this key macOS refuses an Apple Event
         outright, with no consent dialog and no error the user can see, which
         is exactly how "Set up goguma" came to do nothing at all. -->
    <key>NSAppleEventsUsageDescription</key>
    <string>goguma opens Terminal so you can watch its setup run and enter your password there, rather than being asked for it inside a menu bar window.</string>
    <key>NSHighResolutionCapable</key>       <true/>
</dict>
</plist>
PLIST

# Sign inside-out: nested code first, then the bundle. Signing the bundle while
# an unsigned executable sits in Resources produces a signature that validates
# here and fails notarization, which is a slow way to find out.
#
# --options runtime is the hardened runtime, and notarization refuses anything
# without it. --timestamp needs the network and is likewise required; the
# ad-hoc path skips both because neither means anything without a real identity.
if [[ "$SIGN_ID" == "-" ]]; then
    echo "Signing ad-hoc (local use only, will not pass Gatekeeper when downloaded)…"
    for exe in "$APP/Contents/Resources/bin/"*; do
        codesign --force --sign - "$exe" >/dev/null 2>&1 || true
    done
    codesign --force --sign - "$APP" >/dev/null 2>&1 || {
        echo "warning: ad-hoc codesign failed; the app may still run" >&2
    }
else
    echo "Signing with: $SIGN_ID"
    for exe in "$APP/Contents/Resources/bin/"*; do
        codesign --force --options runtime --timestamp --sign "$SIGN_ID" "$exe"
    done
    codesign --force --options runtime --timestamp --sign "$SIGN_ID" "$APP"
    codesign --verify --deep --strict --verbose=2 "$APP"
fi

echo "Built $APP"
echo "Run with:  open '$APP'"
