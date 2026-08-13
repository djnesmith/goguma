#!/bin/bash
#
# Builds the disk image users download.
#
# The window is styled: custom background, no toolbar, the app on the left and
# an Applications alias on the right with an arrow between them. That layout is
# the convention for a reason — it is the instruction, and a plain Finder window
# leaves the reader to work out what to do with a loose .app.
#
# Finder holds that styling in a .DS_Store, and writing one requires scripting
# Finder, which needs a real user session. A CI runner does not have one. So the
# .DS_Store is generated once by a human running this with STYLE=1, committed to
# assets/, and reused verbatim on every later build. That is also why the layout
# numbers below and the artwork in make-assets.swift have to agree: nothing
# re-derives one from the other.
#
# Usage:
#   scripts/make-dmg.sh            build using the committed layout
#   STYLE=1 scripts/make-dmg.sh    re-style via Finder and save a new .DS_Store
#
# Environment:
#   VERSION   version string, used in the output filename
#   SIGN_ID   codesigning identity for the finished image

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-0.1.0}"
SIGN_ID="${SIGN_ID:--}"
STYLE="${STYLE:-0}"

VOLNAME="goguma"
APP="$ROOT/build/goguma.app"
OUT="$ROOT/build/goguma-${VERSION}.dmg"
STAGE="$ROOT/build/dmg-stage"
RW="$ROOT/build/rw.dmg"

# Window geometry. Must match the artwork: the background is drawn 660x420 at
# 2x, and the icon centres below sit where the arrow points.
WIN_W=660
WIN_H=420
ICON_SIZE=128
APP_X=165
APP_Y=270
APPS_X=495
APPS_Y=270

[[ -d "$APP" ]] || { echo "error: no app at $APP; run make-app.sh first" >&2; exit 1; }

rm -rf "$STAGE" "$RW" "$OUT"
mkdir -p "$STAGE/.background"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
cp "$ROOT/assets/dmg-background.png" "$STAGE/.background/background.png"

if [[ "$STYLE" != "1" && -f "$ROOT/assets/dmg-DS_Store" ]]; then
    cp "$ROOT/assets/dmg-DS_Store" "$STAGE/.DS_Store"
fi

if [[ "$STYLE" == "1" ]]; then
    echo "Styling via Finder (needs a logged-in session)…"
    hdiutil create -volname "$VOLNAME" -srcfolder "$STAGE" -ov \
        -format UDRW -size 200m "$RW" >/dev/null
    MOUNT="$(hdiutil attach "$RW" -readwrite -noverify -noautoopen | \
        grep -o '/Volumes/.*' | head -1)"

    osascript <<APPLESCRIPT
tell application "Finder"
    tell disk "$VOLNAME"
        open
        set current view of container window to icon view
        set toolbar visible of container window to false
        set statusbar visible of container window to false
        set the bounds of container window to {200, 120, $((200 + WIN_W)), $((120 + WIN_H))}
        set opts to the icon view options of container window
        set arrangement of opts to not arranged
        set icon size of opts to $ICON_SIZE
        set text size of opts to 13
        set background picture of opts to file ".background:background.png"
        set position of item "goguma.app" of container window to {$APP_X, $APP_Y}
        set position of item "Applications" of container window to {$APPS_X, $APPS_Y}
        close
        open
        update without registering applications
        delay 2
    end tell
end tell
APPLESCRIPT

    # Finder writes .DS_Store lazily; sync before detaching or the layout is
    # lost and the image comes out looking exactly like the default one.
    sync
    cp "$MOUNT/.DS_Store" "$ROOT/assets/dmg-DS_Store"
    echo "Saved layout to assets/dmg-DS_Store — commit it."
    hdiutil detach "$MOUNT" >/dev/null
    hdiutil convert "$RW" -format UDZO -imagekey zlib-level=9 -o "$OUT" >/dev/null
    rm -f "$RW"
else
    hdiutil create -volname "$VOLNAME" -srcfolder "$STAGE" -ov \
        -format UDZO -imagekey zlib-level=9 "$OUT" >/dev/null
fi

rm -rf "$STAGE"

if [[ "$SIGN_ID" != "-" ]]; then
    codesign --force --timestamp --sign "$SIGN_ID" "$OUT"
fi

echo "Built $OUT"
