<div align="center">

# goguma

**Wakes your machine for scheduled jobs,<br>and lets it sleep the rest of the time.**

[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8)

</div>

## Why?

If your machine is asleep when a cron job is scheduled to run, the job
doesn't run. Nothing fails, nothing retries; it just doesn't happen.

goguma wakes the machine shortly before each job, keeps it awake while the
job runs, and lets it go back to sleep afterwards. It doesn't replace cron,
launchd, or systemd; they keep running your jobs. goguma makes sure the
machine is awake when they do.

## Features

- Finds your jobs by scanning crontab, launchd, systemd, and app schedulers
- Wakes the machine right before each job fires
- Sleeps again as soon as the job exits; a 2-second job holds sleep for 2 seconds
- Ends a hold early if the machine gets hot or the battery gets low
- Comes with a menu bar app for macOS

## Installation

Requires macOS 26+.

```sh
brew install junnam586/tap/goguma
goguma install
```

Or with Go 1.26+:

```sh
go install github.com/junnam586/goguma/cmd/goguma@latest
```

From source:

```sh
git clone https://github.com/junnam586/goguma && cd goguma
go build -ldflags "-X main.version=$(git describe --tags --always)" -o bin/ ./cmd/...
./bin/goguma install
```

Linux builds and passes its tests, but has not yet been run on real hardware,
so there is no release binary for it. Build from source if you want to try it.

`install` sets up a user daemon and a small privileged helper. The helper
only blocks sleep and schedules wakes; everything else runs unprivileged.
Use `--dry-run` to see what it would do first.

## Usage

```sh
goguma import    # find scheduled jobs on this machine
goguma status    # see what goguma is doing
```

`import` shows which of your jobs are actually affected by sleep:

```
7 of 43 worth waking for:

[1] Morning briefing: Global News
  schedule  daily at 09:00  (0 9 * * *)
  last run  10h 30m late  (ran Tue 19:30, scheduled for 09:00)
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

Holding a lid-closed laptop awake shouldn't be able to hurt it. A hold is
released early if the machine gets too hot (80°C by default) or the battery
drops below 20%, so a laptop can't cook or drain in a bag. If a job hangs,
a time limit learned from its previous runs ends the hold.

## FAQ

**Does goguma run my jobs?**
No. cron, launchd, or systemd still run them; goguma only makes sure the
machine is awake at the time.

**What if I can't change a job's command?**
goguma can watch the process table for it instead (`--detection pattern
--match "restic.*backup"`), or hold the machine awake for a fixed window
(`--detection none`).

**Does it work on Linux?**
It compiles and the logic is unit tested, but it hasn't run on real Linux
hardware yet. macOS on Apple silicon is what's tested. Windows is not
supported.

**Where's the menu bar app?**
In [`macos/`](macos/). Build it with `cd macos && swift build &&
./scripts/make-app.sh`.

**Where can I read more?**
Architecture notes are in [Docs/ARCHITECTURE.md](Docs/ARCHITECTURE.md).
Related: [Adrafinil](https://github.com/kageroumado/adrafinil) keeps a Mac
awake while an AI coding agent is working.

## License

[MIT](LICENSE)
