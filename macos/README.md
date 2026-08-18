# goguma menu bar app

[README](../README.md) ·
[Security](../SECURITY.md) ·
[Architecture](../Docs/ARCHITECTURE.md) ·
[getgoguma.com](https://getgoguma.com)

A macOS menu bar client for the goguma daemon. It shows what goguma is
doing right now, what it will wake the Mac for next, and how much battery each
job's wake window actually costs; and it lets you skip a wake, release the
holds, pause, and edit jobs without dropping to the CLI.

The app holds no state and does nothing privileged. Everything lives in the Go
daemon; this is a viewer with buttons.

## Requirements

- macOS 14 (Sonoma) or later to run it. `Package.swift` is the authority and
  says `.macOS(.v14)`; this file said 26 for a while after that changed, which
  is a good deal of the audience turned away by a stale line in a README
- Swift 6.2 tools / Xcode 26 toolchain to build it, which is a different
  question from what it runs on
- The goguma daemon (optional: the app renders a clear "not running" state
  in every window when the daemon is absent)

## Build and run

Plain SwiftPM, no Xcode project. It builds headlessly:

```sh
cd macos
swift build                       # debug
swift build -c release            # release
```

Run the binary directly. It puts itself into accessory (`LSUIElement`) mode at
launch, so there is no Dock icon and no app-switcher entry; look for the icon
in the menu bar:

```sh
.build/debug/GogumaUI
```

To get a real `.app` (bundle identifier, `Info.plist` with `LSUIElement`,
launchable by `open` and usable as a Login Item):

```sh
scripts/make-app.sh release       # writes build/goguma.app
open build/goguma.app
```

The script ad-hoc signs the bundle, which is enough to run locally. Replace the
signature with a Developer ID identity before distributing.

### Pointing at a different daemon

The socket path is
`~/Library/Application Support/goguma/daemon.sock`, and, exactly as on the
Go side, `GOGUMA_STATE_DIR` overrides the containing directory:

```sh
GOGUMA_STATE_DIR=/tmp/wg-dev .build/debug/GogumaUI
```

Useful for running against a development daemon without touching a real
installation. Note that `AF_UNIX` caps the socket path at 104 bytes, so a deeply
nested override directory will fail with a clear error rather than silently.

### Rendering a surface to a PNG

```sh
macos/build/goguma.app/Contents/MacOS/goguma --render popover out.png light
```

Surfaces: `popover`, `jobs`, `jobs-selected`, `settings`, `addjob`, `history`,
`empty`, `offline`, `marks`. The last argument is `light` or `dark`.

This is how the screenshots in the docs are made, and it is worth knowing about
before reaching for a screenshot tool: capturing a menu bar app off the screen
needs Screen Recording permission, which an agent or a CI runner doesn't have,
and the popover dismisses itself the moment anything else takes focus. The
renderer hosts the real view in a window placed far offscreen and captures it
with `cacheDisplay`, so it needs no permission and can't be dismissed.

It reads from the live daemon, so what comes out is a real machine's jobs
rather than a fixture. That is the point, and it is also the catch: check what
is in the picture before committing it. `SurfaceRenderer.swift` explains why
this isn't `ImageRenderer`.

`Docs/media/safety.png` is a plain `--render settings` with the Safety tab
selected, and needs no cropping.

It used to need a great deal. The pane was one column of every setting goguma
has, 2368px tall, so the picture had to be cut out of the middle of it by pixel
coordinates that were re-derived by hand every time a row above it moved. Tabs
made the section it wants the whole of what gets rendered.

The tab is `@AppStorage`, so the renderer draws whichever was last opened. To
capture a different one, change `tabRaw`'s default before building.

For the jobs window that default is wrong twice over, so there is
`Docs/media/demo-daemon.py`:

```sh
python3 Docs/media/demo-daemon.py &
GOGUMA_STATE_DIR=/tmp/ggdemo \
    macos/build/goguma.app/Contents/MacOS/goguma --render jobs-selected out.png light
```

It put the author's own project names and home directory path in a README, and
it left the battery columns empty. Empty was correct: "per sleep" is
`fires_per_night * battery_per_run`, and a job scheduled for 09:00 doesn't
fire while anyone is asleep. But a column of dashes says nothing about what the
column is for. The fixture is a machine whose work runs overnight, which is the
case goguma exists for, with one weekly job left in so a cell stays honestly
blank. It captures the shape from the running daemon and replaces only the
values, so it can't drift from the protocol, and it answers reads only: a real
second daemon would reach the same privileged helper as the installed one and
fight it over the wake schedule.

## Design: `Theme.swift` is the single file to edit

**When design principles arrive, edit `Sources/GogumaUI/Theme.swift` and
nothing else.**

Every visual decision in the app is a named token in that one file: semantic
colour roles, materials, the spacing scale, corner radii, stroke widths and dash
patterns, typography, SF Symbol names, surface geometry (popover width, window
sizes), chart geometry, and animation timing. No other file in this target
hard-codes a `Color`, a `Font`, a `Material`, an icon name, or a layout
constant.

Two consequences worth knowing:

- Tokens are named by **meaning**, not appearance: `Colors.stateHolding`, not
  `Colors.amber`; `Colors.chartReference`, not `Colors.dashedOrange`. A new
  palette reassigns roles without any call site changing.
- The current values resolve to stock system semantics (`Color.accentColor`,
  `.primary`, `.secondary`, `.regularMaterial`, `Font.system(…)`), so today the
  app looks like a well-built, brand-free Mac utility and adapts to light and
  dark automatically. That is a deliberate placeholder, not a design.

There are also two token-backed view modifiers (`themeCard()`, `themeRow()`,
`themeBadge(_:)`) so call sites can express intent rather than geometry.

## Layout

```
macos/
├── Package.swift                 SwiftPM manifest (tools 6.2, Swift 6 mode, macOS 14)
├── README.md
├── scripts/make-app.sh           Assembles build/goguma.app around the binary
└── Sources/GogumaUI/
    ├── main.swift                NSApplication entry point
    ├── Theme.swift               ← every visual token, the file to edit
    ├── Model/
    │   ├── Decoding.swift        Defensive Codable helpers, lenient enums, WGDate
    │   ├── WGDuration.swift      Lenient duration: "90s" / "1h 30m" / raw nanoseconds
    │   └── Models.swift          Job, Run, Stats, Hold, DaemonStatus, config, payloads
    ├── IPC/
    │   ├── Protocol.swift        Envelopes, op names, socket path, DaemonError
    │   ├── UnixSocket.swift      POSIX AF_UNIX round trip, framing, short-read loops
    │   └── DaemonClient.swift    Async typed API over a serial off-main queue
    ├── App/
    │   ├── AppDelegate.swift     App shell and main menu
    │   ├── StatusStore.swift     @MainActor @Observable state, polling, actions
    │   ├── StatusItemController.swift  NSStatusItem + popover
    │   ├── WindowCoordinator.swift     Jobs / History / Settings windows
    │   └── Formatting.swift      Date and count rendering
    └── Views/
        ├── PopoverView.swift
        ├── JobsWindowView.swift
        ├── JobEditSheet.swift
        ├── HistoryWindowView.swift
        ├── SettingsWindowView.swift
        ├── Components.swift      Shared rows, badges, empty states
        └── Charts.swift          Sparkline and run-duration chart (hand-drawn Paths)
```

## How it talks to the daemon

Length-prefixed JSON over a Unix domain socket: a 4-byte big-endian length, then
that many bytes of JSON, in both directions.

```
request   {"protocol":1,"op":"status","payload":{…}}
response  {"protocol":1,"ok":true,"error":"","payload":{…}}
```

`UnixSocket.swift` uses POSIX `socket(2)`/`connect(2)` on `AF_UNIX` directly,
one connection per call. It handles, distinctly:

| Condition | Result |
| --- | --- |
| Socket file absent | `notInstalled`: "run `goguma install`" |
| Socket present, nobody listening (`ECONNREFUSED`) | `notRunning` |
| `EACCES` / `EPERM` | `permissionDenied` |
| Read/write deadline (5s, `SO_RCVTIMEO`/`SO_SNDTIMEO`) | `timedOut` |
| Peer closes mid-frame | `io` with byte counts |
| Response `protocol` ≠ 1 | `protocolMismatch`: "update both" |
| `ok: false` | `refused`, carrying the daemon's message |

Reads loop until the requested byte count is satisfied, so a stream socket that
dribbles a reply out in fragments is handled correctly. `SO_NOSIGPIPE` is set so
a daemon that closes mid-write returns `EPIPE` instead of killing the app.

The response envelope is decoded **twice**: once with a payload type that
ignores its body, to read `protocol`/`ok`/`error`, and only then for the payload
itself. That way a payload this build can't parse still surfaces the daemon's
own error message rather than a generic decode failure.

### Concurrency

All socket I/O runs on a dedicated serial `DispatchQueue` inside `DaemonClient`:
off the main thread, and off the Swift concurrency cooperative pool, where a
blocking `read` would starve unrelated tasks. `StatusStore` is `@MainActor` and
`@Observable`; it only writes state after awaiting a client call. The target
builds under Swift 6 language mode with strict concurrency and **no warnings**.

### Defensive decoding

The daemon ships independently of this app, so decoding degrades field by field
rather than all at once:

- Every field uses `decodeIfPresent` with a default. Absent, `null`, and
  malformed all collapse to the default instead of throwing.
- Wire enums (`detection`, `outcome`, `cutout.kind`, warning `kind`) carry an
  `unknown` case; a value from a newer daemon renders as unknown, never crashes.
  Note that `unknown` is treated as *unobservable*, so version skew produces
  silence rather than a warning on every row.
- Durations accept a human string (`"90s"`, `"5m"`, `"1h 30m"`, `"2m 12s"`,
  `"150ms"`, `"2d"`) **or** a bare integer of nanoseconds, since the Go side
  emits both depending on the field. Unparseable reads as zero.
- Timestamps accept RFC 3339 with or without fractional seconds and with `Z` or
  a numeric offset. Go's zero time (`0001-01-01T00:00:00Z`) reads as "no value",
  matching what `omitzero` means on the Go side.

### Polling

`status` is polled once a second while the popover or any window is visible, and
the poll stops as soon as the last one closes. `jobs.list` is only fetched when
a surface that shows jobs is open.

One deliberate deviation from "stop polling when hidden": with everything closed,
polling continues at **30 seconds** (`Theme.Timing.idlePollInterval`) rather than
stopping outright. The menu bar item is always on screen and shows both the state
glyph and the next wake time; an icon that never refreshes is a broken icon. At
1/30th the request rate this is negligible, and both cadences are tokens in
`Theme.swift` if you want to change them; set `idlePollInterval` very high to
approximate a full stop.

## Detection modes

A job's `detection` decides what the UI may honestly claim about it.

| Mode | Wire | Observable | Meaning |
| --- | --- | --- | --- |
| Mark | `mark` | yes | Wrapped in `goguma-mark`; real start, exit, and exit code. |
| Pattern | `pattern` | yes | Process-table regexp. The only mode that can fail silently, so it is the only one tinted as a caution. |
| Wake only | `none` | **no** | Not watched. Wake, hold for `wake_only_hold`, release. |

Wake-only is what automatically adopted jobs use, so on a typical machine it is
the *most common* mode, which makes getting its presentation right load-bearing.
It exists for jobs that run inside another application's own process, where
there is nothing to wrap and no distinct process to match.

Because such a job is never observed, `stats.typical` and `stats.p95` stay
empty, its ceiling is a fixed window rather than a learned one, and it can never
"self-tune". **None of that is a fault**, so `DetectionMode.isObservable` gates
every observation-derived warning and statistic: no cold-start badge, no
never-detected badge, no ceiling-hit badge, no "wasted" column, no duration
sparkline (which would otherwise draw a flat line along zero and assert that
every run took no time). Real problems that are independent of observation (a
schedule that doesn't parse) are still reported for these jobs.

## Managed jobs

`job.managed` is true when goguma adopted the job from a watched scheduler
rather than the user adding it. They are badged in the jobs list so it is
obvious what was chosen versus what was adopted.

It also changes what removal means. A managed job is re-adopted on the next
sync, so deleting it is close to a no-op; it retires by itself once it vanishes
from its source. The remove dialog therefore leads with **Disable Instead**, and
keeps **Remove Anyway** available with an explanation rather than pretending the
deletion sticks.

## Suppressed wakes

`status.wake_suppressed` is non-empty when goguma is deliberately *not*
scheduling a wake: currently when the battery is too low for waking to be worth
it. This is a safeguard working correctly, and it is rendered as its own third
state, distinct from both a scheduled wake and a genuine failure:

- a wake is scheduled → time and countdown;
- a wake is withheld → a `zzz` glyph, "Next wake held back", and the daemon's
  reason **verbatim** (it is already user-facing prose carrying the specific
  percentages, and paraphrasing would lose them);
- nothing scheduled at all → "none scheduled".

Critically, suppression also suppresses the synthesized `wake_failed` warning.
Without that, a working safeguard would appear in the warnings list as a
failure, which is the fastest way to teach users to ignore warnings.

## Surfaces

**Menu bar item.** Glyph reflects state: idle (`moon`), holding
(`sun.max.fill`), paused (`pause.circle`), cutout (`exclamationmark.triangle.fill`),
disconnected. The next wake time appears as the title (`08:58`) when one is
scheduled. Left click opens the popover; right click opens a plain menu, so the
app stays reachable if the popover misbehaves.

**Popover.** Current state with a live elapsed counter that ticks every second
while holding; next wake and countdown; lid / power / battery / CPU temperature;
daemon warnings, prominently; a compact job list with enable toggles (click a row
for its history); and Skip next wake, Let it sleep now, Pause/Resume, Jobs,
Settings, Quit.

**Settings is tabbed.** Five groups, one on screen at a time, selected by a
drawn bar rather than a `Picker(.segmented)`: the segmented control brings
AppKit's greys and radii, and one control in the system palette on goguma's
surface reads as part of a different application. The pane was a single column
of everything until it reached 1266pt against roughly 1000pt of laptop, at which
point its bottom could not be reached at all. It measures its own height, which
is cheap now that a tab is a few hundred points; it was not when the measurement
covered every setting at once, and that is what made the window appear slowly
and then jump.

**One main window, two pages.** Jobs and Settings are the same `NSWindow`,
swapped by `WindowCoordinator.retarget(_:to:)` rather than opened as two. They
were separate windows, and picking Settings from a popover already covering the
Jobs window put the thing you asked for behind the thing you were looking at.
Sharing one window also means one place to remember position and size. History
is still its own window, because it is opened *from* the jobs list and wants to
sit beside it. The shared window is built once at launch by `prewarm()` and
kept off screen, because a cold `NSHostingController` costs a visible beat on
the first open.

**Jobs page.** A table of every job: name, schedule, next run, typical,
ceiling, detection mode, and an inline duration sparkline drawn from
`stats.recent` with non-`ok` runs marked. A badge appears when `schedule_error`
is non-empty, or (for observable jobs only) `stats.never_detected > 0`,
`stats.ceiling_hits > 0`, or `stats.cold_start`. Managed jobs are badged.
Add/Edit sheet, Remove behind a confirmation, and **Sync** to re-read watched
schedulers immediately.

**Add / edit sheet.** One `Grid` with a fixed 108pt label column, so every
control starts on the same x no matter how long its label is. The rows are
Name, Repeat, then whatever that repeat implies (On / Day / Every), At, Time
zone, While it runs, and a disclosure called Advanced holding Command, How it's
watched, Stay awake for and Wake early.

Two things it is careful about. The schedule is built rather than typed
(`ScheduleBuilder.swift`): the cron expression is generated from Repeat and At,
because asking for `0 3 * * *` is asking most people to get it wrong silently.
And the labels say what the setting does rather than what it is called
internally, so the ceiling is "Stay awake for" and the buffer is "Wake early".
Advanced is a disclosure inside the same grid, not a second layout, so opening
it adds rows instead of rearranging the sheet.

The pattern tester is still the point of the detection row: live `match.test`
validation, debounced while typing plus an explicit Test button, because a
wrong regexp otherwise fails silently at 3am.

**History window.** Run durations over time with the ceiling as a dashed
reference line and hold duration as a second series, so the gap between "what the
job needed" and "how long the Mac stayed awake" is visible at a glance. Below it,
a table of runs with started / duration / held / wasted / outcome / exit code /
whether goguma actually woke the machine. Rows where the hold greatly exceeds
the runtime are called out; that is wasted battery, and the thing worth fixing.

**Settings page.** `wake_buffer`, `default_ceiling`, `wake_only_hold`,
`thermal_cutout_c` (70-95), `low_battery_cutout_pct` (5-50), `auto_adopt`,
`webhook_url`, `notify_on_missed_job`, `use_wake_or_power_on`,
`advisory_checks`, `sleep_after_wake`, plus a **Sync Now** button, daemon version, helper connection and version, protocol version,
socket path, and last-updated. Text fields commit on Return; sliders on release.
Every write goes through `config.set` and the config is re-read afterwards, so a
value the daemon clamps shows its clamped value here rather than the one that
was typed.

Two things Settings is careful about:

- **`low_battery_cutout_pct` governs two thresholds.** Holds are released below
  it, *and* the machine isn't woken at all below it plus
  `cutout_rearm_margin_pct`. Showing only the cutout would understate the second
  by the margin, so the derived wake floor is spelled out beneath the slider.
- **`advisory_checks` is hidden, not disabled, when the build can't use it.**
  A daemon built without a signing key refuses every advisory, genuine ones
  included, so the setting is inert. It reports that as
  `advisories_available` on `config.get` and the row isn't drawn at all,
  because a switch that can't do anything is a control that lies.
- **`auto_adopt` has three states, not two.** `null` (never configured) means
  every adoptable source is watched, i.e. ON. `[]` means explicitly off. A
  non-empty list means exactly those sources. `null` and `[]` are opposites, so
  the decoder keeps them distinct and never collapses one into the other.

## Notes on the protocol

### `auto_adopt` over IPC

`config.set` carries a **string**, and the daemon's parser
(`daemon.validateAutoAdopt`) accepts:

- `""`, `"none"`, or `"off"` → an explicit empty list (adoption **off**);
- `"all"`, `"default"`, or `"auto"` → `nil`, the unconfigured "watch
  everything adoptable" state;
- a comma-separated list of watchable source names → exactly those.

`"off"` maps to the explicit empty list rather than `nil` because `nil`
expands to every adoptable source: collapsing them would turn adoption *on*,
the exact inversion of what the user asked for.

The toggle is therefore lossless in both directions: **off** sends `"off"`,
**on** sends `"all"`. (An earlier version scraped a source list from
registered jobs when re-enabling, which permanently narrowed coverage to
whatever happened to be visible at the moment of the click; `"all"` exists so
the toggle isn't a one-way door.)

### Other places the app had to decide

1. **Job IDs on `jobs.add`.** The daemon derives a job's `id` from its `name`,
   but the request payload is a whole `job` object with an `id` field, and
   `Job.Validate()` on the Go side rejects an empty id. The sheet therefore
   derives the id client-side with `Job.slug(from:)`, a direct port of
   `model.Slug`. If the daemon would rather derive it itself, sending an empty
   `id` would be the cleaner contract.

2. **Duration encoding.** `model.Duration` *renders* as `"1h 30m"` (with a
   space) but Go's `time.ParseDuration` rejects that, and rejects `d`/`w` in a
   multi-unit string. Outbound durations are therefore encoded in a strictly
   parseable form (`"1h30m"`, `"90s"`, `""` for zero) while the display form
   keeps the spaces. See `WGDuration.wireString` vs `displayString`.

3. **`managed` is echoed back on edit.** `jobs.put` takes a whole `job` object,
   and `managed` is `omitempty` on the Go side. Sending `managed: false` for a
   job that was adopted would silently un-adopt it, and the next sync would then
   register a duplicate, so the decoded value is round-tripped rather than
   assumed.

### Naming

`DetectionMode.none` is spelled `wakeOnly` in Swift, with the raw value `"none"`.
A case literally named `none` collides with `Optional.none` wherever a
`DetectionMode?` is in scope, which is most of the decoding layer.
