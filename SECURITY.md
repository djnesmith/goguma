# Security

goguma installs a small program that runs as root. This says what that program
can do, what the rest of it reads, and where any of it goes.

Every claim here points at the file that makes it true. The whole thing is a
few thousand lines of Go and Swift, so you can check any of it rather than
take it on trust.

## Nothing leaves your machine

There is one piece of code in goguma that can open a network connection:
[`internal/daemon/webhook.go`](internal/daemon/webhook.go). It returns
immediately unless you have set `webhook_url` yourself, and there is no default
value, so on a machine nobody has configured it never runs.

There is no account, no sign-in, no telemetry, no analytics, and no crash
reporting. Your job names, your commands, your schedules and your sleep history
stay in `~/Library/Application Support/goguma`, which is created mode `0700`
with `jobs.json` written `0600`, in
[`internal/paths/paths.go`](internal/paths/paths.go).

## What runs as root, and what it is allowed to do

Blocking sleep and setting a wake alarm both need root. Everything else does
not, so everything else runs as you.

The privileged part is `goguma-helper`, and it answers exactly six messages:

| Message | What it does |
|---|---|
| `ping` | reports its version |
| `helper.status` | reports whether sleep is blocked and what wake is set |
| `helper.set_sleep_blocked` | turns the sleep block on or off |
| `helper.schedule_wake` | registers a wake at a time |
| `helper.verify_wake` | checks a wake is really registered |
| `helper.cancel_wake` | clears the wake |

Anything else is refused by name. There is no message that runs a command, no
message that reads a file, and no message that takes a path.

**It cannot be talked into running something.** The helper shells out only to
`pmset` on macOS, or `systemd-inhibit` and `rtcwake` on Linux, and it builds
those arguments itself. The only values a caller supplies are a boolean, a
timestamp, and a reason string that is written to the log and never reaches a
command. Arguments are passed as an array rather than through a shell, so there
is no quoting to get wrong. The commands themselves are in
[`internal/helper/sleepblock_darwin.go`](internal/helper/sleepblock_darwin.go).

## Who can reach it

The helper's socket checks the credentials of whoever connects, and accepts
only the user who installed it, or root. Another account on the same Mac is
refused. The check is `AllowOwnerOrRoot`, in
[`internal/ipc/server.go`](internal/ipc/server.go).

That check exists because the sleep block is worth guarding. A laptop held
awake in a closed bag gets hot, and that is not something one user should be
able to do to another.

## It gives up on its own

The helper releases the sleep block if the daemon stops talking to it, checked
every ten seconds. If the daemon crashes, is killed, or is unloaded while a
window is open, the machine goes back to sleeping normally rather than staying
awake until someone notices. It also clears the block on the way out when it is
shut down. Both live in
[`internal/helper/service.go`](internal/helper/service.go).

The safety cutouts sit on top of that: holds are released above 80°C or below
10% battery, and a job that hangs is bounded by a limit learned from its own
previous runs.

## What it reads

To know when to wake, goguma reads what is already scheduled:

- `crontab -l`
- `launchctl list`, and the plists in your LaunchAgents directories
- scheduler files belonging to apps that run their own jobs
- `systemctl list-timers` on Linux

To know whether jobs have been missed, and whether it is safe to hold sleep
off, it reads `pmset -g log` for sleep history and `pmset -g batt` and
`pmset -g therm` for charge and temperature.

**One read is worth calling out.** A job set to pattern detection is watched by
scanning the process table with `ps -Awwo pid=,args=`, which returns the full
command line of every running process. Some programs put secrets in their
arguments, so this is the most sensitive thing goguma looks at. It is matched
against your own job's pattern, kept in memory, never written to disk and never
sent anywhere. Jobs using the `goguma-mark` wrapper or a fixed window do not
scan at all.

## Installing and removing it

`goguma install` prints what it is about to do, and `--dry-run` prints it
without doing it. The password prompt is macOS's own `sudo`, in your terminal.
The app opens Terminal for this rather than asking inside a window of its own,
because a program asking for your password in its own text field is the shape
of a phishing prompt, and it is a habit worth not teaching.

`goguma uninstall` removes the daemon, the helper and the binaries. Your
jobs, config and run history are kept, so reinstalling picks up where you left
off. Add `--purge` to delete those too.

## Reporting something

Open an issue at
[github.com/junnam586/goguma/issues](https://github.com/junnam586/goguma/issues).
If it is a vulnerability rather than a bug, say so in the title and leave out
the details, and I will find a private way to hear the rest.
