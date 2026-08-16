<div align="center">

# 🍠 goguma

**Wakes your machine for scheduled jobs,<br>and lets it sleep otherwise.**

[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8)

[getgoguma.com](https://getgoguma.com) ·
[Download](https://github.com/junnam586/goguma/releases/latest) ·
[Security](SECURITY.md) ·
[Architecture](Docs/ARCHITECTURE.md) ·
[Updates](https://getgoguma.com/updates)

</div>

## What it does

goguma wakes your machine shortly before each scheduled job, keeps it awake
while the job runs, and lets it sleep again as soon as the job is done.

goguma doesn't run your jobs; it makes sure the laptop is awake when they fire.

A sleeping machine doesn't queue the jobs it missed and catch up later. It
misses them outright. Nothing fails, nothing retries, and no error is written
anywhere, because as far as the scheduler is concerned nothing went wrong. You
find out weeks later, when you notice the digest you set up has been arriving
on some days and not others.

<div align="center">

<img src="Docs/media/menubar.png" alt="The goguma menu bar popover, showing the next wake and the jobs it is watching" width="460">

</div>

It lives in the menu bar: what is being held awake and what for, when the next
wake is and which job it is for, and how long each job has been taking. Keep
the Mac awake, skip the next wake, or pause everything without ever opening a
terminal window.

[**Download goguma for macOS**](https://github.com/junnam586/goguma/releases/latest)

## Features

- Finds the work already scheduled on your machine. You wrote none of this
  down for goguma; it goes and reads what is there
- Reads app schedulers too, for apps that run jobs from inside themselves
  rather than through cron or launchd (`goguma scheduler add`)
- Tells you which of those jobs have been missing runs, and how often
- Wakes the machine right before each job fires, and sleeps again the moment
  it exits
- Learns how long each job takes, so the window fits the work instead of
  guessing
- Ends a hold early if the machine gets hot or the battery gets low
- Menu bar app for macOS, so it is all visible without the terminal

## Installation

Requires macOS 14 or newer, or Linux with systemd.

### Download the app (recommended)

[**Download goguma for macOS**](https://github.com/junnam586/goguma/releases/latest),
drag it to Applications, and open it. It will offer to set itself up, and the
command line tools are inside the app, so this is the whole install.

### Or install the CLI only

```sh
brew install junnam586/tap/goguma
goguma install
```

Both paths end in the same place: a background service, a privileged helper and
the `goguma` command. The app is a viewer for that service, so adding it later
is just opening it, and removing it changes nothing about your jobs.

`goguma install` sets up the background service and the helper. The helper only blocks
sleep and schedules wakes; everything else runs unprivileged.

Also available as a release archive, via `go install`, or built from source
with `go build ./cmd/...`. The Mac app is a universal binary, so Intel and
Apple silicon run the same download; the command line archives are per
architecture and Homebrew picks the right one. Linux builds ship for amd64 and
arm64 and pass CI on every commit.

## Usage

You don't have to do this. goguma reads every scheduler on the machine by
itself, every couple of minutes, and starts waking for what it finds. Run
`goguma import` when you want to see what it found, or to give one of those
jobs exact timing:

<div align="center">

<img src="Docs/media/import.gif" width="850" alt="goguma import reporting jobs that have been silently missing runs">

</div>

Adopted jobs are woken for either way. Where a job's process can be picked out
of the process table, goguma watches for it and sleeps the moment it exits.
Where it can't, the machine is held for a bounded window instead, which costs
a little more battery.

`goguma import --register` is how you close that gap. It offers to wrap the job
in `goguma-mark`, which reports the exact start, end and exit code. If you
accept, goguma makes that edit for you: it rewrites the crontab line or the
launchd plist, keeps a copy of the original, and puts the original back if the
result doesn't verify. Nothing is changed until you say yes to that job, and
`goguma import` on its own only reports.

Everything else is a click. The menu bar shows what is being held awake and
what is coming next, the jobs window lists everything with its learned duration
and lets you add, edit or pause a job, and settings covers the timing and
safety limits.

<div align="center">

<img src="Docs/media/jobs.png" alt="The goguma jobs window, with a job selected showing its learned hold window and what each run costs in battery">

</div>

Selecting a job shows where its numbers came from: the hold window underneath
is the figure goguma learned, not one you set, and it says which runs it was
derived from. The rest of this section is the same features for people who
would rather type.

```sh
goguma status    # see what goguma is doing
```

Add a job by hand with cron syntax or plain English:

```sh
goguma add --name nightly-backup --cron "every day at 3am" \
    --command "restic backup /home"
```

For exact timing, run the job through the included wrapper. It reports the
start, end, and exit code, so the machine sleeps the moment the job is done:

```
0 3 * * * goguma-mark nightly-backup -- restic backup /home
```

Some apps keep their own job list in a file rather than registering with cron
or launchd. Point goguma at that file once and it reads it from then on:

```sh
goguma scheduler add cowork ~/Library/Application\ Support/Cowork/tasks.json
```

`goguma help` lists all commands, and `goguma help <command>` explains one.

## Safety

A hold is released early if the CPU goes above 80°C or the battery drops
below 10%, both configurable. So a laptop that is awake in a closed bag stops
heating up, and one on battery doesn't run out of charge.

The 10% floor rises for jobs that are measured to cost more than that. A job
that has been drawing 4% per run won't start one below 14%, so it can't
strand the machine partway through.

If a job hangs, a time limit learned from its previous runs ends the hold. The
default backstop is five minutes.

goguma installs one small program that runs as root, because blocking sleep and
setting a wake alarm both need it. [SECURITY.md](SECURITY.md) says what that
program is allowed to do, who can reach it, and what the rest of it reads.

## FAQ

**Does goguma run my jobs?**
No. Whatever runs them now still runs them; goguma only makes sure the machine
is awake at the time.

**If I only install the app, does it find my jobs on its own?**
Yes. The background service re-reads every scheduler on the machine every couple of
minutes and wakes for whatever is worth waking for, with no terminal step. It
doesn't touch your crontab to do it, so a job whose process it can't recognise
gets a bounded window rather than exact timing. `goguma import --register` is
how you upgrade those, and `goguma sync` re-reads everything on demand.

**Does it ever edit my crontab?**
Only in one place, and only after you say yes to that specific job:
`goguma import --register`. It keeps a copy of the old crontab first and
restores it if the new one doesn't verify. Everything else reads and never
writes. See [Installing and removing it](SECURITY.md#installing-and-removing-it).

**What if I can't change a job's command?**
goguma can watch the process table for it instead (`--detection pattern
--match "restic.*backup"`), or hold the machine awake for a fixed window
(`--detection none`).

**Does anything leave my machine?**
Nothing about you. There is no account, no telemetry and no analytics.
[SECURITY.md](SECURITY.md#nothing-about-you-leaves-your-machine) lists the only
two pieces of code that can open a socket at all.

**Does it work on Linux?**
Yes, on a distribution with systemd. It uses `systemd-inhibit` to hold sleep
and `rtcwake` to set the alarm. Windows isn't supported.

**Where's the menu bar app?**
In the [release download](https://github.com/junnam586/goguma/releases/latest),
or build it yourself from [`macos/`](macos/README.md) by running
`cd macos && ./scripts/make-app.sh`.

**How do I hear when something breaks?**
[getgoguma.com/updates](https://getgoguma.com/updates).

**Where can I read more?**
Architecture notes are in [Docs/ARCHITECTURE.md](Docs/ARCHITECTURE.md), and the
Mac app has its own notes in [macos/README.md](macos/README.md).

## License

[MIT](LICENSE)
