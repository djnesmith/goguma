# wakeguard

**Wake your machine for scheduled jobs. Let it sleep the rest of the time.**

> **Note on visual identity.** This README is deliberately unstyled — no logo,
> banner, or colour scheme. A brand direction is pending; when it lands, the
> only files that need to change are this header, `internal/render/theme.go`
> (the CLI's colours and symbols), and `macos/Sources/WakeGuardUI/Theme.swift`
> (the app's). No logic references a colour anywhere else.

---

## The problem

Cron doesn't check whether your laptop is awake. If the machine is asleep when
a job's fire time arrives, the job is skipped — no error, no retry, no
notification. You find out days later, if at all.

On the machine this was developed on, `pmset -g log` shows it was **asleep for
105 of the last 168 hours — 63% of the week.** That's the baseline probability
that any given cron job silently didn't happen.

WakeGuard closes the loop: wake the machine shortly before the job fires, hold
sleep off while it runs, notice the moment it finishes, and let the machine go
back to sleep. It does not run your jobs — your existing cron, launchd, or
systemd entry still does that. WakeGuard only guarantees the machine is awake
to receive them.

### One honest distinction

**cron** skips a job that fires during sleep. It is gone.
**launchd** does not — `launchd.plist(5)` states it "will start the job the
next time the computer wakes up," coalescing multiple missed intervals into
one.

So for cron jobs WakeGuard buys **survival**. For launchd calendar jobs it buys
**punctuality** — a 3am briefing being ready at 3am rather than whenever you
next open the lid. `wakeguard import` labels which is which rather than
implying every missed run vanishes.

---

## Install

Requires Go 1.26+ to build. macOS 26+ or Linux with systemd.

```sh
git clone https://github.com/junnam/wakeguard && cd wakeguard
go build -o bin/ ./cmd/...
./bin/wakeguard install
```

`install` copies the binaries to `~/.local/bin`, registers the daemon to start
at login, and installs the privileged helper. It prints every step, marks which
ones need your password, and verifies the services actually came up rather than
assuming they did. `--dry-run` shows the plan without touching anything.

The helper is a separate root service that does exactly two things — block
sleep, and register a wake with the OS. All scheduling and policy stays in the
unprivileged daemon. You can skip it with `--no-helper`, but then WakeGuard
cannot schedule wakes or hold a lid-closed machine awake, which is most of the
point.

---

## Quick start

```sh
wakeguard import      # find jobs on this machine worth waking for
wakeguard status      # see what it's doing
```

### If you don't know what you have scheduled

That's the normal case, and it's what `import` is built for. It scans your
crontab, launchd agents, systemd timers, **and application schedulers** — then
throws almost all of it away. A typical Mac has hundreds of loaded services, so
the filter isn't "does this look scheduled" but **"would this actually be
missed"**:

```
Examined 43 scheduled entries.
  · crontab   none on this machine  ·  crontab -l
  ✓ hermes    7 found  ·  ~/.hermes/cron/jobs.json
  ✓ launchd   36 found  ·  ~/Library/LaunchAgents, /Library/LaunchAgents, …
This machine was asleep 4d 7h of the last 6d 23h.

7 of 43 worth waking for:

[1] Morning briefing: Global News
  source    hermes
  schedule  daily at 09:00  (0 9 * * *)
  last run  10h 30m late  (ran Tue 19:30, scheduled for 09:00)
      this job does eventually run — but not at the time it is scheduled for.
```

Two things matter here.

**It reports what it searched**, per source. A tool that runs jobs from inside
its own config is invisible to a crontab scan, so "found nothing" has to be
distinguishable from "didn't look there" — otherwise the command most useful to
someone unsure what they have is also the most misleading.

**Evidence beats inference.** When a scheduler records its own last-run time,
WakeGuard compares it against the schedule and reports the delay as fact. Only
when no such record exists does it fall back to replaying the schedule against
your machine's real sleep history. Directly observed lateness always ranks
first. `--all` shows everything filtered and why.

Adding support for another scheduler is one file implementing a `Provider` —
see [`internal/scan/hermes.go`](internal/scan/hermes.go).

To register a job by hand, whatever runs it:

```sh
wakeguard add --name nightly-backup --cron "every day at 3am" \
    --command "restic backup /home"
```

Schedules accept plain English — `weekdays at 6pm`, `every 30 minutes`,
`mondays at 09:00` — as well as cron syntax. Run `wakeguard add` with no flags
and it asks, echoing back both the cron expression and the next real fire time
so a misreading is caught before it is saved.

### Never having to remember

The moment a job is created is rarely a moment anyone is thinking about sleep.
If you ask an agent for a morning briefing, it writes a schedule into its own
store and nothing prompts you to also tell WakeGuard.

So WakeGuard watches for them. **This is on by default**: installing the tool
is the statement that your jobs should survive sleep, and requiring a second
opt-in mostly produces installs that quietly do nothing — a new silent failure
of exactly the kind this exists to prevent.

New jobs are picked up within a couple of minutes and retired when they
disappear from their source. `wakeguard sync` runs it immediately.

Acting unprompted is only acceptable if it is not also invisible, so install
tells you what it adopted and what it costs:

```
✓ now waking for 6 job(s) found on this machine:
    • Morning briefing: AI/Tech        daily at 09:00
    • FloppyData balance watchdog      every 6h
    …
  this costs about 13 wakes a day, roughly 58m of extra awake time
  turn any of it off with 'wakeguard disable <name>', or all of it with
  'wakeguard config set auto_adopt off'
```

A tool whose purpose is saving battery owes you an account of the battery it
intends to spend.

Two limits. Only schedulers that run their own jobs are adopted — a crontab
entry needs its command line edited to be detectable, so adopting one
unprompted would create a job that looks registered and is recorded as
never-detected on every run. Those stay a deliberate `import` decision. And
jobs you registered by hand are never removed by a scan.

---

## How WakeGuard knows your job is running

This is the part that determines whether any of the rest works, so there are
two mechanisms and they are not equal.

### Exact — recommended

Change your crontab line to run the job through the wrapper:

```crontab
0 3 * * * wakeguard-mark nightly-backup -- restic backup /home
```

The wrapper runs your command unchanged and passes its exit status through,
but tells WakeGuard the instant the job starts and the instant it exits. That
gives a true duration from the very first run, a real exit code (so "ran and
failed" is distinguishable from "ran fine"), and a release the moment the work
is done.

It is written so it can never be the reason your job fails: if the daemon is
down, unreachable, or slow, your command still runs and still exits with its
own status.

### Best-effort — no changes to your setup

```sh
wakeguard add --name backup --cron "0 3 * * *" \
    --detection pattern --match "restic.*backup"
```

WakeGuard watches the process table for a match. Nothing to edit, but it can't
see exit codes and sees nothing at all if the pattern is wrong.

That failure mode is the reason WakeGuard is loud about it. A window that
closes without ever seeing the job is recorded as `never_detected` — not as a
success — and two in a row raises a warning with the command to fix it. Check a
pattern before you rely on it:

```sh
wakeguard test-match "restic.*backup"
```

### Wake-only — for jobs you can't instrument

```sh
wakeguard add --name briefing --cron "0 9 * * *" --detection none
```

Some jobs can't be observed at all. An application that runs schedules inside
its own process gives you no command line to wrap and no distinct process to
match. For those, WakeGuard wakes the machine and holds for a fixed window
(3 minutes by default, `--max-runtime` to change it), and doesn't pretend to
watch anything.

The tradeoff is stated rather than hidden: the hold can't self-tune to the
job's real runtime, so it costs more battery than the other two modes. That's
the honest price of not being able to see the job — and it's still far better
than the job not running.

---

## Battery: measured, not guessed

The hold ends when your job *exits*, not when a timer runs out. In testing, a
2-second job held sleep off for exactly 2 seconds.

There is still a ceiling, but it's a safety valve for a hung job — not the
expected hold time. It's learned from real runs (p95 of the last 20 × 1.2), so
it converges on what your job actually does:

```
$ wakeguard history nightly-backup
  runs     14 recorded
  typical  42s  (median)
  p95      71s
  ceiling  90s
  trend    ▁▂▁▁▃▁▂▁▁▂▁▁▂▁

   STARTED       RAN FOR  HELD FOR  OUTCOME
✓  Aug 04 03:00      41s       41s  ok · woken
✓  Aug 03 03:00      44s       44s  ok · woken
```

Runs that were cut off never train the estimator — otherwise one hung run would
ratchet the ceiling up and pin it there permanently.

`HELD FOR` sitting well above `RAN FOR` is wasted battery, and usually means
detection isn't seeing your job exit. WakeGuard totals it for you rather than
leaving it as a suspicion.

---

## Safety

Holding a lid-closed laptop awake in a bag is genuinely risky, so two valves
release every hold — both only while the lid is closed, since that's the only
state where you can't see something is wrong:

- **Thermal** — above 80°C (configurable 70–95), read from the SMC.
- **Low battery** — below 20% on battery (configurable 5–50), so the machine
  enters normal low-power sleep with charge to spare instead of draining to a
  hard shutdown.

A fired cutout **latches** until conditions genuinely recover, because the job
is usually still running and would otherwise re-acquire immediately, oscillating
several times a second.

If no temperature sensor is readable, the thermal cutout **cannot** fire — and
WakeGuard tells you the valve is inoperative rather than letting you assume
you're protected.

---

## Keeping it awake yourself

Sometimes the reason the machine has to stay up is you, not a job:

```sh
wakeguard awake 30m       # 1h30m, 2h, …
wakeguard awake off       # release it early
```

Asking again replaces the window rather than adding to it, so `awake 10m`
followed by `awake 1h` leaves you awake for an hour from now. The duration is
clamped to between 1 minute and 12 hours — there is deliberately no "until I
say otherwise", because a hold nobody remembers is the failure the safety
valves exist to prevent, and a bounded one ends by itself.

It is otherwise an ordinary hold: `status` shows it with a countdown, the
thermal and low-battery cutouts release it exactly as they release a job's,
and it is gone if the daemon restarts. It is never recorded as a run, so it
can't skew any job's learned ceiling or statistics.

---

## Commands

```
getting started   install · import · add
everyday          status · list · history
managing jobs     edit · group · remove · enable · disable · test-match
control           awake · skip-next · sleep-now · pause · resume
maintenance       config · doctor · uninstall · version
```

`wakeguard help <command>` for any of them.

Output is coloured and aligned on a terminal and plain when piped — no flag
needed. `NO_COLOR` is honoured.

`wakeguard status --json` emits a stable, versioned shape for scripting, and
stays valid JSON even when the daemon is down so a dashboard doesn't have to
special-case that.

When something isn't working, `wakeguard doctor` walks the whole chain —
binaries, daemon, helper, wake registration, thermal sensor, then each job's
configuration — and tells you which link is broken and the command to fix it.

---

## Menu bar app (macOS)

A SwiftUI status-bar app lives in [`macos/`](macos/): current hold with a live
counter, next wake, power state, warnings, and full job add/edit/remove, plus
duration history charts and settings.

```sh
cd macos && swift build && ./scripts/make-app.sh
```

Quitting the app does **not** pause the daemon. WakeGuard has to keep waking
the machine whether or not a window is open; ⌘Q silently disabling your jobs
would be a trap.

---

## Platform support

| | macOS | Linux |
|---|---|---|
| Wake from sleep | `pmset schedule wake` | `rtcwake` (hardware-dependent) |
| Idle sleep hold | `IOPMAssertion` | logind inhibitor |
| Lid-closed hold | `pmset disablesleep` | logind inhibitor incl. `handle-lid-switch` |
| Temperature | SMC | `/sys/class/thermal` |
| Sleep history | `pmset -g log` | `journalctl` |
| Services | launchd | systemd |

**What has actually been verified**, on macOS 26.5.1 / Apple silicon: SMC
temperature reads, sleep-log reconstruction, the wrapper measuring durations
exactly, exit-code propagation, the ceiling valve force-releasing a hung job,
and import filtering a real launchd tree.

**What has not**: waking from real sleep on a schedule overnight, and
lid-closed holds — both need unattended on-device runs. And **all of Linux**:
it compiles for amd64 and arm64 and its logic is unit tested, but no part of it
has run on Linux hardware. In particular, whether systemd inhibitors hold a
lid-closed laptop across desktop environments with their own lid policy, and
whether `rtcwake` genuinely wakes a given machine, are unproven — firmware can
accept an alarm and then ignore it, which userspace cannot detect.

Windows is not supported.

---

## Roadmap

- Overnight on-device verification of wake-from-sleep and lid-closed holds
- Linux hardware validation
- Adrafinil interop — defer sleep-hold assertions to it when installed, so the
  two tools don't hold duplicate assertions
- Homebrew tap
- Windows (Task Scheduler wake timers)

## Design

See [Docs/ARCHITECTURE.md](Docs/ARCHITECTURE.md) for the three privilege tiers,
why `pmset disablesleep` is used despite being blunt, how a stranded sleep block
is made impossible, and the miss-risk model behind `import`.

## Related

[Adrafinil](https://github.com/kageroumado/adrafinil) keeps a Mac awake while an
AI coding agent is actively working. WakeGuard is the other half of the same
idea: it wakes the machine *for* scheduled work. Different trigger, same
philosophy — awake only when there's real work, asleep the rest of the time.

## License

MIT
