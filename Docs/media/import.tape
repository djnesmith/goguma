# A real recording of the real command, made with charmbracelet/vhs.
#
#   brew install vhs gifsicle    # vhs pulls ttyd and ffmpeg
#   cd Docs/media && vhs import.tape
#   gifsicle -O3 --colors 64 import.gif -o import.gif
#
# The gifsicle pass is not optional and VHS cannot do it: it takes the file from
# 194K to 131K. A terminal recording is a dozen theme colours plus antialiasing,
# so 64 is more than it uses; measured against the full-palette version the mean
# pixel differs by 4 of 765 and the worst single pixel by 80, which is one
# shade on one edge of one glyph.
#
# This is for the README, not the landing page — the page wanted something with
# more life in it than a terminal, and it got a reenactment of the search
# instead. The recording is still the best artifact for a README, and it is
# still the only thing here that cannot drift from the product: it reports the
# misses because the tool does, in whatever numbers are true on the day. The
# exact figure used to be quoted here and was three re-records out of date.
#
# `import` reports and changes nothing unless asked with --register, so this
# is safe to re-record. It used to need `--dry-run` on screen to be safe, which
# put a flag in the demo whose whole job was to promise the demo was harmless.

Output import.gif

Require goguma

# Recorded against a sandbox HOME and an empty state directory, so it shows
# nobody's machine and no daemon answers.
#
# The empty state directory is because `existingJobIDs` is a best-effort daemon
# call: with one running, every line is tagged "(already registered)", which is
# true and meaningless to someone seeing the command for the first time.
#
# The sandbox HOME is because this command scans the machine it runs on, which
# is the point of the command and the problem with filming it. Earlier takes
# published a real username, a real tool's path and six real job names, enough
# between them to name two projects and a payment stack, and every one is still
# fetchable from the commits that carried it. demo-home.py builds the sandbox;
# the scan resolves its sources from HOME, so what comes out is a genuine
# recording of the genuine command against invented jobs.

Set Shell "zsh"
# Menlo, because it is actually installed. VHS silently falls back when a
# font is missing, and the fallback is not monospaced — the first take came
# out with letters spaced like a ransom note.
Set FontFamily "Menlo"
# Everything below is sized to the output rather than chosen: the whole report
# has to be on screen at once (see the note by the command), so the only way to
# make the image smaller is to make the text smaller and cut the slack around
# it. 17 gave a 1200px-tall window, 15 gave 1100.
# Everything here is doubled, and the README displays it at half.
#
# The asset was 850 wide and a README column is about 900, so it painted at
# 1:1 in CSS pixels, which on any screen made since about 2012 means one image
# pixel smeared across two device pixels. That is what "blurry" was: not the
# encoder and not the palette, just an asset with no headroom. Rendering at 2x
# and pinning the display width in the README gives it that headroom, and is
# why the very first version of this recording looked sharper despite being
# older; at 1180 wide it was being scaled down rather than up.
Set FontSize 26
# The longest line the report prints is 99 characters, the launchd sources
# line. At 26px Menlo that is about 1544px, plus the padding.
Set Width 1700
# About twenty rows, which is a terminal rather than a page.
#
# This was 930, tall enough for all 53 lines of the report at once, because a
# heading that says "6 of 44" above a list of two reads as a broken tool. That
# is true, and the fix was worse: a README does not have room for a 930px
# column of terminal, and the thing was three times the height of everything
# around it.
#
# So the window is short and the output scrolls, which is what a real terminal
# does. The summary goes off the top, and going off the top is the point: with
# no count on screen there is nothing for the entry list to contradict, and
# what the last frame rests on instead is the two jobs whose runs goguma
# actually saved. That was always the stronger half of the report.
Set Height 780
Set Padding 44
Set Margin 0
Set BorderRadius 12
# Real window chrome, baked into the recording rather than faked in HTML around it.
Set WindowBar Colorful
Set WindowBarSize 40
Set TypingSpeed 18ms
# Double speed. The scan takes several seconds inside a pty and there is no way
# to make it faster, so the recording sat on a near-static "Scanning…" screen
# for about half its length before anything happened. Nothing here rewards
# being watched at real time; the Sleep at the end is doubled to compensate so
# the result still holds for the same 4.5 seconds.
Set PlaybackSpeed 2
# 24 rather than the default 50. Half the frames for a recording whose only
# motion is a line of typing and one paint of output, and the file has to come
# down a README over someone's connection.
Set Framerate 24
Set CursorBlink true
Set WaitTimeout 60s

# The page's own palette, so the recording belongs to the site rather than
# looking like a stock terminal dropped into it.
Set Theme { "name": "goguma", "background": "#150F22", "foreground": "#EDE7F4", "cursor": "#EDE7F4", "selection": "#3A2A52", "black": "#150F22", "red": "#D4674A", "green": "#7FB58A", "yellow": "#D9A15E", "blue": "#9B7BC4", "magenta": "#B98FD8", "cyan": "#8FBFD0", "white": "#EDE7F4", "brightBlack": "#6B5F7D", "brightRed": "#E07C5C", "brightGreen": "#93C79E", "brightYellow": "#F0B36C", "brightBlue": "#B295D6", "brightMagenta": "#CDA6E8", "brightCyan": "#A6D2E2", "brightWhite": "#FFFFFF" }

Hide
# Both of the above, set up here rather than left to whoever runs this. The
# state directory was only ever described in a comment, and a re-record on a
# machine with the daemon running duly tagged every line "(already registered)".
Type "python3 demo-home.py" Enter
Type "export HOME=/tmp/goguma-demo GOGUMA_STATE_DIR=$(mktemp -d)" Enter
Type "clear" Enter
Sleep 400ms
Show

# Short pre-roll. A third of the old recording was typing and a blank
# "Scanning..." screen before anything was shown, which is a long time to ask
# of someone deciding whether to keep watching.
Sleep 250ms
# The whole command, unpiped. `head -22` used to trim it, which is what put a
# count of six above a list of two and made the tool look broken. Nothing is
# trimmed now; the window is simply smaller than the output, so the terminal
# scrolls the way it would for anyone running this.
Type "goguma import"
Sleep 300ms
Enter

# Wait on the output, not on a guess. Fixed Sleeps were tried at 6s and 11s and
# both ended the recording on the "Scanning…" line — the scan is slower inside a
# pty than it is at an ordinary prompt. `Wait+Screen` blocks until the text is
# actually on screen, so the recording cannot end early however slow the machine.
# `@45s` is the TIMEOUT, not a poll interval — `@500ms` gave up instantly and
# the whole recording failed with nothing but "recording failed".
Wait+Screen@45s /worth waking for/
# Long enough to actually read the finding before the loop restarts. 2200ms
# put the cut about a second after the output painted, so it flashed the result
# and vanished, which reads as the recording breaking rather than looping.
Sleep 9000ms
