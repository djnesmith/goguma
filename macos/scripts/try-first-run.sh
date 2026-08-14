#!/bin/bash
#
# Runs the app exactly as a fresh download experiences it, without disturbing
# the daemon you actually have.
#
# GOGUMA_STATE_DIR points the app at an empty directory, so it looks for a
# socket that is not there and reports "goguma isn't running" honestly. Your
# real daemon keeps running, untouched, on its own socket.
#
# The Set Up button in this state DOES work and would install for real, so
# this is a rehearsal of the flow rather than a mock of it. Stop before typing
# your password if you only want to look.

set -euo pipefail

APP="${1:-/Applications/goguma.app}"
[ -d "$APP" ] || { echo "no app at $APP"; exit 1; }

SANDBOX="$(mktemp -d /tmp/goguma-first-run.XXXXXX)"
trap 'rm -rf "$SANDBOX"' EXIT

echo "Quitting any running copy…"
pkill -f "goguma.app/Contents/MacOS" 2>/dev/null || true
sleep 2

# The app shows itself once per machine, so clear the record of having done so.
echo "Clearing the first-run flag…"
defaults delete glass.goguma.ui goguma.hasPresentedFirstRun 2>/dev/null || true

echo "Launching against an empty state dir: $SANDBOX"
echo
echo "  Expect: no window for about a second, then the popover opens by itself"
echo "          under the menu bar potato, saying goguma isn't running."
echo
GOGUMA_STATE_DIR="$SANDBOX" "$APP/Contents/MacOS/goguma" &
APP_PID=$!

sleep 12
echo "Done looking? Closing the rehearsal copy."
kill "$APP_PID" 2>/dev/null || true
wait "$APP_PID" 2>/dev/null || true

echo "Restoring your normal copy…"
defaults delete glass.goguma.ui goguma.hasPresentedFirstRun 2>/dev/null || true
open "$APP"
echo "Back to normal. Your daemon was never touched."
