#!/bin/zsh
# Waits for the daemon to genuinely hold a job, then captures the popover.
#
# `pop-holding.png` cannot be staged: it is the popover during a real hold, with
# the real job name and a live ceiling countdown. `goguma awake` produces a
# manual keep-awake, which renders different copy and would be a lie. So this
# polls until a job actually holds and renders at that moment.
set -u
BIN="${0:a:h}/../macos/.build/arm64-apple-macosx/release/GogumaUI"
A="${0:a:h}/assets"
DEADLINE=$(( $(date +%s) + ${1:-2400} ))
while (( $(date +%s) < DEADLINE )); do
  if ~/.local/bin/goguma status 2>/dev/null | head -1 | grep -qi "holding"; then
    "$BIN" --render popover "$A/pop-holding.png" light
    "$BIN" --render popover "$A/pop-holding-open.png" light -popover.jobsExpanded YES
    echo "captured a real hold at $(date '+%H:%M:%S')"
    exit 0
  fi
  sleep 5
done
echo "no hold observed before the deadline"
exit 1
