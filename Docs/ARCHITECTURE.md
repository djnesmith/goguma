# goguma architecture

> How goguma is built: the three privilege tiers, the wake and sleep-hold
> mechanisms, how jobs are detected, the hold lifecycle, and the safety
> cutouts.

[README](../README.md) ·
[Security](../SECURITY.md) ·
[Mac app](../macos/README.md) ·
[getgoguma.com](https://getgoguma.com)

---

## 1. Components

Five binaries across three privilege tiers, plus an optional GUI. Four of them
ship to users; the fifth is a maintainer tool.

```
┌──────────────────────────────────────────────────────────────────────┐
│  goguma              (CLI, runs as the user, no privilege)           │
│  goguma.app          (menu bar app, macOS only: a view layer)        │
│  • add / list / status / history / import / doctor / install         │
│  • never touches power management; everything goes via the daemon    │
└────────────────────────────┬─────────────────────────────────────────┘
                             │ length-prefixed JSON over a unix socket
                             │ ~/Library/Application Support/goguma/daemon.sock
                             ▼
┌──────────────────────────────────────────────────────────────────────┐
│  goguma-daemon       (LaunchAgent / systemd user unit)               │
│  ALL POLICY LIVES HERE                                               │
│  • schedule evaluation and wake-time computation                     │
│  • hold lifecycle and reference counting                             │
│  • job detection (process scan + mark protocol)                      │
│  • duration history and ceiling estimation                           │
│  • thermal and low-battery cutouts, cutout latch                     │
│  • sleep/wake observation, event log, webhooks                       │
│  • app-scheduler manifests, and the daily advisory check             │
└────────────────────────────┬─────────────────────────────────────────┘
                             │ length-prefixed JSON over a unix socket
                             │ /var/run/goguma-helper.sock  (root-owned)
                             ▼
┌──────────────────────────────────────────────────────────────────────┐
│  goguma-helper       (LaunchDaemon / systemd system unit, root)      │
│  THE ONLY PRIVILEGED CODE. Holds no policy at all.                   │
│  • set_sleep_blocked(bool)                                           │
│  • schedule_wake(time) / cancel_wake()                               │
│  • status (read-only)                                                │
└──────────────────────────────────────────────────────────────────────┘

  goguma-mark   (wrapper, runs inside the user's cron line)
  • goguma-mark <job> -- <command>
  • reports the job's real start, exit time, and exit code to the daemon

  goguma-advisory  (maintainer tool, never run by a user)
  • keygen / sign, for the notice feed
  • the private half never exists on a user's machine
```

### Why the split

The helper is privileged because scheduling a wake and overriding clamshell
sleep both require root. It is kept to two mutating operations and no state so
the elevated-privilege surface of the whole product is one readable file. It
doesn't know what a job is, when anything is scheduled, or why sleep is being
blocked.

The daemon runs as the user so it can read the user's crontab, watch
user-owned processes, and write under the user's own directories. It is
always-on rather than tied to the GUI, because jobs must survive whether or
not anyone has a window open.

The CLI and the app are pure clients. Quitting the app deliberately does **not**
pause the daemon; goguma must keep waking the machine for cron jobs
regardless of whether a UI is running.

---

## 2. Waking the machine

**macOS**: `pmset schedule wake "MM/dd/yy HH:mm:ss" goguma`, run by the
helper.

Entries are tagged with the owner string `goguma`. This matters more than
it looks: `pmset -g sched` on an ordinary Mac already shows several competing
entries from Apple subsystems, and the owner tag lets us cancel precisely our
own entry rather than using `cancelall`, which would delete the user's alarms
and other applications' scheduled events.

`wakeorpoweron` is available via `use_wake_or_power_on` and additionally boots
a powered-off machine. It is off by default because silently powering on a
shut-down laptop is a surprise most users wouldn't consent to.

**Known quirk, and the mitigation.** Scheduled wake entries can be silently
dropped or overwritten when other applications call `pmset schedule`. A wake
that was accepted at request time may simply not be there later, and a job
that never fires because its wake vanished is exactly the silent failure
goguma exists to prevent. So the daemon re-asserts the current wake every
60 seconds rather than setting it once, and the helper exposes
`scheduledWakes()` so a requested wake can be read back and verified; "pmset
returned success" isn't treated as sufficient evidence.

Only one wake is registered at a time: the nearest. Registering every job's
wake up front would fill the system schedule with entries that go stale the
moment a job is edited.

**Linux**: `rtcwake -m no -t <epoch>` sets an RTC alarm without suspending.
RTC wake is hardware- and firmware-dependent, so `WakeScheduleSupported()`
probes for `rtcwake` on PATH plus a readable `/sys/class/rtc/rtc0/wakealarm`
and reports the answer, rather than assuming one.

---

## 3. Holding sleep off

Two mechanisms compose, because they cover different things.

**Idle sleep (lid open)**: an unprivileged `IOPMAssertion`
(`kIOPMAssertPreventUserIdleSystemSleep`) taken directly by the daemon via
cgo. The kernel releases it automatically if the daemon dies, so it can never
be stranded.

**Clamshell (lid-closed) sleep**: privileged, via the helper, using
`pmset -a disablesleep 1`.

`IOPMAssertionCreateWithName` with the public assertion types does **not**
override clamshell sleep; Apple's own header says "the system may still sleep
for lid close." The Adrafinil project verified on-device (macOS 26.3) that
three cleaner in-process alternatives also fail to keep a displayless
lid-closed Mac awake:

- private `RootDomainUserClient` selector 12 (`setClamShellSleepDisable`):
  returns success, the Mac still sleeps; it governs the external-display
  clamshell path, not no-display lid close;
- `IORegistryEntrySetCFProperty(IOPMrootDomain, "SleepDisabled", …)`:
  `kIOReturnNotPermitted` even as root;
- `IOPMSetSystemPowerSetting("SleepDisabled", …)`: returns success but
  `pmset -g` still reports `SleepDisabled 0`; `pmset` coordinates
  `IOPMSetPMPreferences` and an activation step around that call which the
  bare call doesn't reproduce.

`pmset -a disablesleep` is blunt (global, also suppresses idle sleep, and
persists in the power-management preferences until cleared) but it is Apple's
own tested implementation and it is the path that works. It runs only on state
flips, so the subprocess cost is negligible.

### Why disablesleep can never be stranded

`disablesleep` is **not** cleared when the process that set it exits, and can
be reset by the kernel across a sleep/wake cycle. Five layers cover that:

1. the helper clears it on release and on **SIGTERM** (logout, shutdown, unload);
2. the helper clears it at **startup**, and its unit has `RunAtLoad` so this
   runs at boot before any login, recovering from a crash or forced power-off;
3. the helper has a **dead-man switch**: if the last daemon connection drops
   while blocked and none returns within 60 seconds (the daemon SIGKILLed at
   logout), it clears the block itself;
4. the daemon clears it on its own SIGTERM;
5. while blocking, the daemon **re-pushes the state every 60 seconds**, healing
   a failed `pmset`, a raced helper restart, or a kernel reset. The helper's
   `set` is deliberately *not* short-circuited when the state looks unchanged,
   because that is what makes the re-push a repair rather than a no-op.

Every `pmset` invocation runs under a 10-second timeout so a wedged subprocess
can't deadlock the helper's policy lock.

**Linux**: a `systemd-logind` inhibitor lock held by a child process. The
inhibitor is `--what=sleep:idle:handle-lid-switch`, not `sleep:idle`:
`logind.conf` ships `LidSwitchIgnoreInhibited=yes`, which tells logind to
suspend on lid close *even while sleep inhibitors are held*, a hole exactly
where clamshell holds matter. The lock is an fd held by a child whose stdin is
a pipe we own, so if the daemon dies by any means including SIGKILL the kernel
closes the fd and logind drops the lock. That kernel-enforced cleanup is why
an inhibitor was chosen over masking sleep targets, which persists across
reboot and is precisely the stranding the dead-man switch exists to prevent.

---

## 4. Detecting jobs

goguma doesn't run jobs. The user's existing cron, launchd, or systemd
entry does. So the daemon has to work out on its own whether a job is
currently running, and there are two ways to know.

### 4.1 Mark: exact

```
goguma-mark <job-name> -- <original command>
```

The wrapper runs the command unchanged, and tells the daemon the moment it
starts and the moment it exits, with the real exit status. This gives a true
duration from the very first run, distinguishes "ran and failed" from "ran and
succeeded", and releases the hold the instant the job is done.

Its overriding constraint is that **it must never be the reason a job fails**.
Every daemon interaction is best-effort with a 2-second timeout; if the daemon
is down, unreachable, or slow, the wrapped command still runs and its exit
status is still propagated faithfully. Signals are forwarded to the child, and
a process killed by a signal reports `128 + signum` in the shell convention.

A mark for a job with no open window opens an **ad-hoc window**. Something real
is running and the machine should stay awake for it, a genuine advantage over
pattern matching, which can only observe jobs inside an expected window.

**Getting the wrapper in there is `import --register`'s job, not the user's.**
It used to print the line and leave the user to run `crontab -e` and paste it,
which reads as the more modest promise but is the worse outcome: the job is
already registered as exactly-timed by that point, so a forgotten paste leaves
it recording "never detected" on every run and warning about a setup nobody
said was half-finished. A setup step that ends in homework, and then silently
punishes forgetting it, isn't restraint.

So it writes. `internal/cli/crontab_apply.go` rewrites the one crontab line and
`internal/cli/launchd_apply.go` rewrites the one plist and reloads the job, both
only after the user picks that option for that specific job. Each copies the
original first, changes only its own line or key, reads the result back, and
puts the original back if it doesn't re-parse. Two details are load-bearing:
the wrapper path is absolute, because cron's `PATH` is `/usr/bin:/bin` and would
never find `~/.local/bin/goguma-mark`; and it is shell-quoted for crontab but
not for launchd, because one is a shell line and the other is an argv array.
Getting that backwards splits `/Users/Jun Nam/...` in half.

### 4.2 Wake-only: for jobs that can't be observed

Detection mode `none`. The machine is woken and held for a fixed window, and
no attempt is made to watch the job.

This exists because a whole class of jobs is genuinely unobservable. An
application that runs schedules inside its own process (Hermes, n8n, a
self-hosted runner) offers no command line to wrap and no distinct process to
match. Forcing one of the other modes on such a job means every single run is
recorded as `never_detected` and warned about, generating a permanent alarm
about a configuration that is correct.

Wake-only holds therefore end as `ok` rather than `never_detected`, and their
ceiling comes from `wake_only_hold` (3 minutes default) rather than from
history. The cost is explicit: the hold can't converge on real runtime, so it
wastes more battery than the observable modes. That is the honest price of not
being able to see the job, and it is stated in the UI rather than hidden.

### 4.3 Pattern: best effort

The daemon scans the process table for a regexp match. No changes to the
user's setup, but it can't see exit codes, can't distinguish concurrent
runs, and observes nothing at all if the pattern is wrong.

Process listing is `ps -Awwo pid=,args=` on macOS: reading another process's
full argv requires `KERN_PROCARGS2`, which is increasingly gated, and `ps` is
`setgid procview` precisely to make this work. On Linux `/proc/<pid>/cmdline`
is read directly, so no subprocess is involved. Scanning happens only while a
window is actually open.

**Self-match guard.** goguma's own processes are excluded from matching.
Without it, `goguma add --match backup` self-matches the moment the user
runs `goguma list` in a terminal (the pattern is stored in `jobs.json` and
appears in the daemon's own command line) and the daemon would conclude the
job is running and hold the machine awake indefinitely. The guard keys on the
program name, so a user's own script that merely mentions "goguma" is still
matchable.

### 4.4 Making failure loud

Pattern detection is the project's main reliability risk. The mitigation
isn't cleverer matching, it's refusing to fail silently:

- a window that closes without ever seeing the job is recorded as
  `never_detected`, not as a successful run;
- two consecutive such runs raise a warning in `status`, `doctor`, and the
  menu bar, carrying the exact command to fix it;
- `goguma test-match` evaluates a pattern against the live process table so
  a mistake surfaces at configuration time rather than at 3am;
- history shows hold time alongside real runtime, so the battery wasted by bad
  detection is a number rather than a suspicion.

---

## 5. The hold lifecycle

```
every tick (10s), plus a faster poll (2s) while a window is open:

  for each enabled job:
    fire = schedule.next()
    if now >= fire - wake_buffer and not already held:
        open a window: take the idle assertion, ask the helper to block sleep

  for each open window:
    detect start   (mark ping, or first pattern match)
    detect exit    (mark ping, pid gone, or pattern no longer matches)
        -> release immediately; record the real duration
    past deadline  -> force-release
        -> ceiling hit if the job was running, never-detected if it was not
```

A window ends when the job is observed to exit, **not** when a timer expires.
That is the entire battery argument: hold duration converges on real runtime
plus wake overhead rather than on a padded guess.

Ceilings are enforced on the fast poll as well as the main tick. Checking only
on the tick lets a short ceiling overshoot by a full tick interval (a 5s
ceiling fired at 10.2s in testing before this was fixed), which matters
because the ceiling exists to bound how long a hung job can pin the machine.

Before detection the window is bounded by a **detection grace** of at least two
minutes past fire time. A machine that has just woken from deep sleep can take
tens of seconds to run cron, and a job wrongly declared missing produces a loud
warning about a match pattern that is actually fine.

### Wrapped commands: `goguma run`

Some work has no fire time and no schedule. A coding agent runs for as long as
it runs; a long build takes what it takes. `goguma run -- <command>` holds sleep
off for exactly as long as the command does, then releases.

It exists because such work cannot be watched from outside. An agent waiting on
a model response is a process blocked on a socket, and looks identical to one
doing nothing: measured on one machine, four agent processes used 0.32s, 0.25s,
0.50s and 0.05s of CPU over ten wall seconds, and the busiest figure belonged to
an idle session while the one actively working used less. The work is not
happening locally, so there is nothing local to observe. The command therefore
announces itself, exactly as `goguma-mark` does for a scheduled job.

Each run gets its own entry in the hold map, under an id from
`model.RunHoldPrefix`, rather than sharing the single manual keep-awake slot.
Two terminals can each be running something, and the second must not release the
first's hold by starting; the machine stays awake until the last finishes.

**The hold is leased, and that is the whole safety design.** The wrapper sends
`run.end` when the command exits, but a wrapper that is `SIGKILL`ed, or whose
machine loses power, never gets to. A hold with nothing left able to close it is
the failure §7 exists to prevent. So the hold expires by default after 90
seconds and survives only while something keeps renewing it; the wrapper renews
at a third of that, so two consecutive failures still leave a live command held.
A hold is also capped at 12 hours however often it is renewed, which covers the
other case: a live wrapper attached to something that will never finish.

Run holds count as `manual()`, which is the single gate that keeps them out of
run history, the ceiling estimator, per-job statistics, and sleep-back. A
wrapped command's runtime is a fact about whatever the user chose to wrap rather
than evidence about any registered job, and there is no job for it to be
evidence about. Keeping them out of sleep-back is right for a second reason: a
wrapped command is user-initiated, so finishing one is never a reason to put the
machine to sleep.

### Coding agents

An agent inside an editor is the case `goguma run` cannot reach: there is no
command to wrap, because the editor started it. It cannot be watched either, for
the reason above, so it has to report itself, and every current harness can. All
of them run shell commands at their own lifecycle events.

goguma writes one command into each agent's own configuration, on its prompt,
tool-use and stop events. The shapes differ (`~/.claude/settings.json` and
`~/.codex/hooks.json` nest handlers under a matcher block, `~/.cursor/hooks.json`
takes a flat array beside a `version`), so `internal/agenthooks` holds one
description per harness and merges rather than writes: entries carrying goguma's
marker are replaced, everything else is left in place and in order. A file that
does not parse is refused rather than replaced, and every write is backed up and
rolled back if the result does not verify.

**Holds are keyed by session.** `RunStartReq.Key` opens or renews rather than
opening afresh, so the same session reporting on every prompt and every tool call
lands on the hold it already has instead of stacking up holds nothing will close.
Two editors open therefore hold independently, and the one that finishes first
releases only its own.

**Stopping is what stops it, not a timer.** The stop event releases immediately.
The lease is the backstop for a harness killed outright, and it is fifteen
minutes rather than the ninety seconds `goguma run` uses: a wrapper renews on a
timer it controls, while an agent renews on events it does not, and a model can
think for minutes between tool calls with nothing happening locally. An event
goguma does not recognise renews rather than releases, because harnesses add
events and being wrong that way holds a working machine awake rather than
sleeping it mid-run.

**The daemon keeps it true, rather than the installer doing it once.** `agent_hooks`
is a setting, on by default, reconciled on every daemon start and whenever it
changes. An agent installed a month after goguma is set up without anyone
remembering to; turning the setting off takes the configuration back out of every
agent it was added to, which is what lets the switch live in the app's settings,
since that sets config over IPC and never runs the CLI.

---

## 6. Duration estimation

The ceiling is a **safety valve, not a prediction**. Process-exit detection is
what normally ends a hold. The ceiling only matters when that signal never
arrives (a hung job, or detection that stopped working) and its job is to
stop goguma holding a machine awake indefinitely.

```
ceiling = clamp(p95(last 20 completed runs) × 1.2, min_ceiling, max_ceiling)
```

- **Cold start**: fewer than 3 completed runs uses a conservative 5-minute
  default. One or two fast runs would otherwise produce a confidently wrong,
  very tight cap.
- **Nearest-rank percentile**, so the result is always a duration that actually
  occurred and the ceiling is explainable (`goguma list --explain`).
- **Truncated runs never train the estimator.** A run cut off at its ceiling is
  evidence of how long the cap was, not how long the job takes. Feeding it back
  creates a ratchet where one hung run pins the ceiling high forever. Cutouts
  are excluded for the same reason. Failed runs *do* count: they produced a
  real duration measurement.
- **Only the recent window counts**, so a job that legitimately got slower
  converges on its new duration rather than being held to a stale baseline.

`--max-runtime` is an override, never the primary mechanism, and always wins
when set.

---

## 7. Safety cutouts

Both valves are gated on the lid being closed, because that is the only state
where holding sleep off is genuinely dangerous: the machine may be in a bag
with no airflow and the user can't see anything is wrong. With the lid open, a
hot or draining machine is visible and the normal system protections apply.

- **Thermal**: above 80°C (configurable 70-95) every hold is force-released.
  Temperature comes from the SMC over public IOKit, no entitlement needed.
- **Low battery**: below 20% (configurable 5-50) while on battery. Because the
  block is global, a lid-closed machine held awake on battery would otherwise
  drain to a hard shutdown rather than entering normal low-power sleep with
  charge to spare. Never fires on AC, where there is no drain to protect
  against.

### Refusing the wake, not just releasing the hold

The cutouts above are reactive: they release a hold once conditions have
already gone bad. That is sufficient for a tool which only ever holds a machine
that is *already awake*: the user was just using it, and it is probably open
or recently open.

goguma has a hazard that shape of tool doesn't. It wakes a machine the user
deliberately put to sleep, possibly into a closed bag, possibly at 3am with
nobody watching. **A cutout can't un-wake a machine**: by the time it fires,
the wake has happened and the energy is spent. On a nearly-flat battery the
sequence would be wake, immediately release, and end up closer to a hard
shutdown with the job not run either.

So the decision is made before the wake is ever registered. If the machine is
on battery at or below the low-battery threshold, no wake is scheduled at all:
it stays asleep, the charge is preserved, and the job is missed exactly as it
would have been anyway. This is reported as `wake_suppressed` rather than
`wake_error`, and shown by `doctor` as a skip rather than a warning: a
safeguard doing its job must not read as a broken scheduler.

Temperature is deliberately *not* checked at scheduling time. A machine cools
while it sleeps, so how hot it is now says nothing about how hot it will be
hours later at wake time; blocking on it would refuse wakes for a hazard that
has passed. Charge only ever falls while asleep, which makes it the one signal
that stays meaningful.

**An unreadable sensor can't fire a cutout**, and is never treated as safe
either: the daemon raises a warning saying the valve is inoperative, so the
user knows rather than assuming they are protected.

**The latch.** A fired cutout latches. The job is usually still running, so
without a latch the next tick re-opens the window into a live hazard and the
system oscillates release → re-acquire many times a second. The latch clears
when the hazard genuinely recedes by a hysteresis margin (5°C, or 5% / back on
AC), or when the lid opens, which ends the hazard by definition.

### SMC temperature across Mac generations

There is no single portable CPU-temperature key. Intel Macs expose `TC0P`;
Apple silicon dropped it and publishes per-cluster die sensors instead. Rather
than detecting the architecture (a proxy for the real question), a candidate
list is probed in order and the winner cached, with readings outside 5-120°C
rejected as implausible. On the Apple silicon machine used for development,
`TCHP` is the key that answers.

---

## 8. Miss-risk: deciding what is worth waking for

`goguma import` has to solve a presentation problem before a technical one.
A typical Mac has hundreds of loaded launchd services and, usually, an empty
crontab. Showing all of it would be worse than showing nothing.

### 8.0 Discovery is pluggable, and reports its own coverage

The OS schedulers are only part of the picture. Plenty of tools run schedules
entirely inside their own config and process (Hermes, n8n, self-hosted
runners) and those jobs appear in neither `crontab -l` nor `launchctl list`.
On a machine where such a tool is the user's main automation, an OS-only scan
reports "nothing scheduled here" while every job they care about sits in a
file it never opened.

So discovery is a `Provider` interface (`internal/scan/provider.go`) and adding
a scheduler in Go is one new file. Each provider reports three things: whether
it exists on this machine, *where* it looked, and what it found.

One line goguma always skips is its own. `import --register` rewrites a
crontab entry to `goguma-mark <job> -- <command>`, and the adoption sweep used
to read that back as a job it had never seen, registering a second one called
`goguma-mark-<job>` at the same minute with its own wake and its own hold. The
duplicate could never be detected either, because the wrapper announces the job
under its real name. `UnwrapMark` in `internal/scan/crontab.go` recognises the
shape and adopts under the real name instead.

Shipping a Go file per app doesn't scale to software goguma has never heard
of, so there is a second path that needs no code at all.
`internal/scan/manifest.go` reads small JSON manifests out of the state
directory, each naming a file to watch and which fields inside it are a job's
name and schedule. `goguma scheduler add <name> <file>` writes one: it reads
the file, infers those fields (`internal/scan/infer.go`), and prints what it
decided, so the guess can be checked and the manifest hand-edited afterwards.
Teaching goguma a new scheduler is a user action now, not a release.

That coverage report isn't decoration. The single most dangerous output this
command can produce is a confident "nothing needs goguma" when the truth is
"your jobs live somewhere I didn't check", and the user most likely to run
`import` is precisely the one who doesn't know where their jobs are defined.
An unqualified all-clear would actively mislead them. When nothing is found,
the command says which sources were empty and which were never available.

### 8.1 Observed lateness beats inferred risk

Most schedulers keep no run history: cron records nothing, launchd exposes
nothing useful. Application schedulers frequently do, and when a `last_run_at`
is available it is far better evidence than anything goguma can infer.

Comparing that timestamp against the schedule gives the delay as a *fact*:
"scheduled 09:00, last ran 12:52, 3h52m late." No sleep-history
reconstruction, no schedule replay, nothing to be wrong about. Such a job is
surfaced even when it would otherwise be excluded as self-healing: a
scheduler that catches up isn't a problem in principle, but if its own records
show it running four hours behind, then in practice the job isn't happening
when the user expects, and catch-up logic doesn't change that.

Lateness is only treated as urgent for schedules that name a wall-clock time.
"Every 6 hours" only asks to happen periodically and running late is nearly
free; "at 09:00" states that 09:00 is the point, and a morning briefing
delivered at 13:00 has lost most of its value.

Where the scheduler publishes its own next-run time, that value is used rather
than recomputing from the expression: an interval schedule counts from its
last run, not from now, and showing a time that disagrees with what will
actually happen is worse than showing nothing.

The filter is therefore not "does this look like a cron job" but **"would this
actually be missed"**:

| Excluded | Why |
|---|---|
| OS-owned (`com.apple.*`, distro timers) | Not the user's automation |
| Always running (`KeepAlive`, live pid, socket-activated) | Already there on wake; never missed |
| No clock trigger (`RunAtLoad`, `WatchPaths`) | No fire time to wake for |
| Self-healing | The scheduler re-runs it after a missed window |
| Fires more often than 30 minutes | A dedicated wake costs more battery than the job saves |
| Measurably never missed | The machine is already awake when it fires |

What survives is ranked by **measured** miss-risk: the schedule is replayed
against the machine's real sleep history and scored on how many of its past
fire times landed while the machine was asleep. A heuristic ("3am is risky")
would be wrong for anyone who works nights or leaves the lid open.

Sleep history comes from `pmset -g log` on macOS, which reaches back weeks,
so this works on the day of install rather than after a fortnight of
self-observation. Each `Sleep` line carries the length of the sleep it is
entering as a trailing `N secs` field, so one line yields a complete interval.
The daemon also records its own observed sleep gaps, which covers platforms
with no queryable system log and survives log rotation.

**DarkWake coalescing.** macOS punctuates a long sleep with maintenance
DarkWakes lasting two to five seconds. The machine isn't usefully awake during
those, so gaps under 90 seconds are joined into one continuous interval.
Without this an overnight sleep is recorded as dozens of separate intervals and
a job that happened to fire in a gap is scored "not missed", exactly backwards.

**Unknown risk is kept, not discarded.** Absence of evidence would hide exactly
the jobs the user is looking for, so a schedule with too little history to
judge is surfaced as unknown rather than filtered as low-risk.

### cron loses jobs; launchd only delays them

These are different problems and goguma says so. From `launchd.plist(5)`:

> Unlike cron which skips job invocations when the computer is asleep, launchd
> will start the job the next time the computer wakes up. If multiple intervals
> transpire before the computer is woken, those events will be coalesced into
> one event upon wake from sleep.

So for a cron job goguma buys **survival**; for a launchd calendar job it
buys **punctuality**. Import labels the latter accordingly instead of implying
those runs disappear.

---

## 9. IPC

Length-prefixed JSON over a Unix domain socket: a 4-byte big-endian length,
then that many bytes of JSON. Length prefixing rather than newline delimiting
because payloads are large and structured, and a parser scanning for delimiters
inside JSON strings is a class of bug worth designing out. Frames are capped at
8 MiB so a corrupt length can't cause an unbounded allocation.

```json
// request
{"protocol": 1, "op": "status", "payload": {}}
// response
{"protocol": 1, "ok": true, "payload": {...}}
```

`protocol` is checked on both sides; a mismatch produces "client speaks vN,
this daemon speaks vM" rather than a misrendered screen.

New ops are added without bumping `protocol`, which is reserved for changes that
break an existing shape. An older daemon meeting `run.start` answers that it
does not know the op, and `goguma run` reports that and runs the command anyway
rather than refusing to run somebody's build because a background service is out
of date.

**Authorization comes from the kernel, never from the wire.** The helper reads
the peer's effective uid via `LOCAL_PEERCRED` (macOS) or `SO_PEERCRED` (Linux)
and accepts only the owning user and root. The peer pid is read too but is
advisory (pids are recycled) and is used only for log context.

Socket paths are length-checked at bind. `sockaddr_un.sun_path` is a fixed
104-byte buffer on macOS, and exceeding it fails with a bare `EINVAL` that says
nothing about the cause; it is reachable in practice through a long username.

---

## 10. Persistence

`~/Library/Application Support/goguma/` (macOS) or
`$XDG_STATE_HOME/goguma/` (Linux):

| File | Contents |
|---|---|
| `jobs.json` | Registered jobs, versioned envelope |
| `config.json` | Settings; out-of-range values are clamped and reported |
| `daemon.sock` | CLI/GUI channel, mode 0600 |
| `history/<job>.jsonl` | One line per run, capped at 500 |
| `events.jsonl` | Audit trail of every wake/hold/release, rotated at 10 MB |
| `sleepwake.jsonl` | Sleep intervals the daemon observed itself |

Everything is written atomically (temp file, fsync, rename), because the daemon
can be killed at any moment and a half-written `jobs.json` would silently
disable every registered job. A malformed individual job is skipped and
reported rather than failing the whole load: one bad hand-edit shouldn't take
every other job offline. Unparseable history lines are skipped for the same
reason: a truncated final line from a hard kill must not reset a job to a
cold-start ceiling.

Run records and event lines are written off the control path so a slow disk
can't delay releasing a sleep hold, but they are tracked and flushed on
shutdown so the last window of a session isn't lost.

---

## 11. Verified on device

Confirmed on macOS 26.5.1, Apple silicon:

- SMC temperature reads via the candidate-key probe (`TCHP`, 38.6°C).
- `pmset -g log` parsing reconstructed 32 real sleep intervals over 7 days;
  the machine was asleep 105 of 168 hours (63%).
- `goguma-mark` end to end: 5 runs of a 2-second job measured at exactly 2s,
  with **hold time equal to run time** (no wasted battery) and the ceiling
  converging from the 5-minute cold-start default to 30s.
- Exit codes propagate: a job exiting 3 is recorded as `failed` with code 3.
- Ceiling valve: a job with a 5s ceiling that never finishes was force-released
  at 6.1s and recorded as `ceiling`, with the daemon still responsive.
- Import against this machine's real launchd tree: 36 entries examined, all 36
  correctly excluded with reasons.
- **Waking the machine from real sleep**: the RTC alarm fired to the second,
  twice, from a genuine `pmset sleepnow`. Measured against the wake, not
  against the return value of the call that scheduled it.
- **Lid-closed holds**: a physical clamshell test held the machine awake for
  the window and let it sleep on release.
- Sleep-gap detection and wake rescheduling across a real sleep cycle.

### Platform behaviour worth knowing

These are properties of the platforms rather than of this code, and each one
is why the corresponding probe exists rather than an assumption.

- **RTC wake is firmware-dependent on Linux.** Firmware can accept an alarm
  and then ignore it, and nothing in userspace can tell the difference, so
  `WakeScheduleSupported()` reports what it can actually confirm: `rtcwake` on
  PATH and a readable `/sys/class/rtc/rtc0/wakealarm`.
- **Lid policy belongs to the desktop environment.** GNOME and KDE implement
  their own, so a systemd inhibitor is one input to that decision rather than
  the whole of it.
- **RTC clocks may be local or UTC**, which dual-boot machines commonly change.
  The wake path reads the offset rather than assuming UTC.
- **`systemd-inhibit --list` column output varies by systemd version**, so the
  parser is written against the columns it finds rather than fixed positions.
