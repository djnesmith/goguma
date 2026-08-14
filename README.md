<div align="center">

# goguma

**Wakes your machine for scheduled jobs,<br>and lets it sleep otherwise.**

[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8)

</div>

## What it does

goguma wakes your machine shortly before each scheduled job, keeps it awake
while the job runs, and lets it sleep again as soon as the job is done.

It does not run your jobs. It makes sure the machine is awake when they fire.

A sleeping machine does not queue the jobs it missed and catch up later. It
misses them outright. Nothing fails, nothing retries, and no error is written
anywhere, because as far as the scheduler is concerned nothing went wrong. You
find out weeks later, when you notice the digest you set up has been arriving
on some days and not others.

## Are you missing runs?

`goguma import` reads what is already scheduled on your machine and reports
which jobs have been missing runs, and how often:

<div align="center">

![goguma import finding jobs that have been silently missing runs](Docs/media/import.gif)

</div>

It asks before registering anything. Add `--dry-run` to look without being
asked at all.

## Features

- Finds the work already scheduled on your machine. You wrote none of this
  down for goguma; it goes and reads what is there
- Tells you which of those jobs have been missing runs, and how often
- Wakes the machine right before each job fires, and sleeps again the moment
  it exits
- Learns how long each job takes, so the window fits the work instead of
  guessing
- Ends a hold early if the machine gets hot or the battery gets low
- Menu bar app for macOS, so it is all visible without the terminal

## Installation

Requires macOS 26+, or Linux with systemd.

### Download the app

[**Download goguma for macOS**](https://github.com/junnam586/goguma/releases/latest),
drag it to Applications, and open it. It will offer to set itself up, and the
command line tools are inside the app, so this is the whole install.

<div align="center">

<img src="Docs/media/menubar.png" alt="The goguma menu bar popover, showing the next wake and the jobs it is watching" width="480">

</div>

I'd suggest this one. The menu bar shows whether the machine is being held
awake and what for, when the next wake is and which job it is for, and how long
each job has been taking. Keep it awake, skip the next wake, or pause it
entirely without opening a terminal.

### Or install the CLI only

```sh
brew install junnam586/tap/goguma
goguma install
```

Both paths end in the same place: a background daemon, a privileged helper and
the `goguma` command. The app is a viewer for that daemon, so adding it later
is just opening it, and removing it changes nothing about your jobs.

`goguma install` sets up the daemon and the helper. The helper only blocks
sleep and schedules wakes; everything else runs unprivileged. Run
`goguma install --dry-run` first to see exactly what it will do.

Also available as a release archive, via `go install`, or built from source
with `go build ./cmd/...`. Linux builds are tested in CI but have seen far less
real use than macOS.

## Usage

You do not have to do this. goguma reads every scheduler on the machine by
itself, every couple of minutes, and starts waking for what it finds. Run
`goguma import` when you want to see what it found, or to give one of those
jobs exact timing:

```
7 of 43 worth waking for:

[1] Morning briefing: Global News
  schedule  daily at 09:00  (0 9 * * *)
  last run  10h 30m late  (ran Tue 19:30, scheduled for 09:00)
```

Adopted jobs are woken for either way. Where a job's process can be picked out
of the process table, goguma watches for it and sleeps the moment it exits.
Where it cannot, the machine is held for a bounded window instead, which costs
a little more battery. `import` is how you close that gap: it offers to wrap
the job in `goguma-mark`, which reports the exact start, end and exit code.
That needs one line of your crontab changed, and goguma will never make that
edit on your behalf.

Everything else is a click. The menu bar shows what is being held awake and
what is coming next, the jobs window lists everything with its learned duration
and lets you add, edit or pause a job, and settings covers the timing and
safety limits. The rest of this section is the same features for people who
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

`goguma help` lists all commands.

## Safety

A hold is released early if the machine gets too hot (80°C by default) or the
battery drops below 10%, so a laptop in a bag cannot overheat or run itself
flat. The battery floor rises for jobs measured to cost more than that, so a
job that drains 4% never starts a run it cannot finish. If a job hangs, a
time limit learned from its previous runs ends the hold rather than letting
it run forever.

## FAQ

**Does goguma run my jobs?**
No. Whatever runs them now still runs them; goguma only makes sure the machine
is awake at the time.

**If I only install the app, does it find my jobs on its own?**
Yes. The daemon re-reads every scheduler on the machine every couple of
minutes and wakes for whatever is worth waking for, with no terminal step. It
never edits your crontab to do it, so a job whose process it cannot recognise
gets a bounded window rather than exact timing. `goguma import` is how you
upgrade those.

**What if I can't change a job's command?**
goguma can watch the process table for it instead (`--detection pattern
--match "restic.*backup"`), or hold the machine awake for a fixed window
(`--detection none`).

**Does it work on Linux?**
It compiles and the logic is unit tested, but it hasn't run on real Linux
hardware yet. macOS on Apple silicon is what's tested, and Windows is not
supported at all.

**Where's the menu bar app?**
In the [release download](https://github.com/junnam586/goguma/releases/latest),
or build it yourself from [`macos/`](macos/) by running
`cd macos && ./scripts/make-app.sh`.

**Where can I read more?**
Architecture notes are in [Docs/ARCHITECTURE.md](Docs/ARCHITECTURE.md).
Related: [Adrafinil](https://github.com/kageroumado/adrafinil) keeps a Mac
awake while an AI coding agent is working.

## License

[MIT](LICENSE)
