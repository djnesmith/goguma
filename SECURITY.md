# Security

[README](README.md) ·
[Architecture](Docs/ARCHITECTURE.md) ·
[Mac app](macos/README.md) ·
[getgoguma.com](https://getgoguma.com)

goguma installs a small program that runs as root. This says what that program
can do, what the rest of it reads and writes, and where any of it goes.

Every claim here points at the file that makes it true. The whole thing is a
few thousand lines of Go and Swift, so you can check any of it rather than
take it on trust.

## Nothing about you leaves your machine

Two pieces of code in goguma can open a network connection, and neither one
sends anything about you.

[`internal/daemon/webhook.go`](internal/daemon/webhook.go) returns immediately
unless you have set `webhook_url` yourself, and there is no default value, so
on a machine nobody has configured it never runs.

[`internal/advisory`](internal/advisory) can fetch a static file from
getgoguma.com once a day, to say that a bug has been found or a fix is out. It
is a plain GET with no query string, no identifier, no version number and no
body, so it tells the other end exactly what any web server learns from a file
being fetched. What comes back is checked against a signing key compiled into
the binary, and can do exactly two things: display a sentence, and say a newer
release exists. It carries no settings and can't change goguma's behaviour,
because a server able to reach into a tool that runs a root helper is a remote
control channel into your machine, and that isn't a thing this project will
build. Turn it off for good with `goguma config set advisory_checks off`.

It is on for new installs and **off** for anyone who installed before it
existed, because upgrading isn't consent.

**A build with no key compiled in does nothing at all**, and every release
before v0.1.0 was one. With no key every advisory fails verification, genuine
ones included, so rather than leave it failing the check is switched off at the
source: `Enabled()` in
[`internal/advisory/key.go`](internal/advisory/key.go) is false when the key is
empty, the daemon reports that to the app as `advisories_available`, and the
app hides the setting rather than offering a switch that can't do anything. A
build from source has no key either, unless you pass one.

A feed that is absent is not an error. If nothing has been published there is
nothing to fetch, the daily check finds a 404, and goguma says nothing, which
is the same outcome as a feed with no notices in it.

If you would rather be told by email than by the program, that is
[getgoguma.com/updates](https://getgoguma.com/updates).

There is no account, no sign-in, no telemetry, no analytics, and no crash
reporting. Your job names, your commands, your schedules and your sleep history
stay in `~/Library/Application Support/goguma`, which is created mode `0700`
with `jobs.json` written `0600`, in
[`internal/paths/paths.go`](internal/paths/paths.go).

## What runs as root, and what it is allowed to do

Blocking sleep and setting a wake alarm both need root. Everything else
doesn't, so everything else runs as you.

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

**It can't be talked into running something.** The helper shells out only to
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
awake in a closed bag gets hot, and that isn't something one user should be
able to do to another.

## It gives up on its own

The helper releases the sleep block if the background service stops talking to
it, checked every ten seconds. If the service crashes, is killed, or is unloaded
while a window is open, the machine goes back to sleeping normally rather than
staying awake until someone notices. It also clears the block on the way out
when it is shut down. Both live in
[`internal/helper/service.go`](internal/helper/service.go).

The safety cutouts sit on top of that: holds are released above 80°C or below
10% battery, and a job that hangs is bounded by a limit learned from its own
previous runs.

**A wrapped command cannot strand a hold either.** `goguma run -- <command>`
holds sleep off for as long as your command runs, which means the thing that
would release it is a process that can be killed. So the hold is leased rather
than trusted: it expires after 90 seconds unless something keeps renewing it,
and the wrapper renews while the command is alive. Kill the wrapper, pull the
power, and the hold lapses on its own instead of waiting for someone to notice.
It is also capped at 12 hours however long the command runs, and the cutouts
above apply to it exactly as they apply to a job's. The command itself runs as
you, with no shell in between, and nothing about it reaches the privileged
helper; the surface above is unchanged. See
[`internal/daemon/runhold.go`](internal/daemon/runhold.go).

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
sent anywhere. Jobs using the `goguma-mark` wrapper or a fixed window don't
scan at all.

## What it writes

Everything above is reading. Two things change files outside goguma's own state
directory, and this is both of them.

**Coding agent hooks.** goguma adds one command to the configuration of each
coding agent it finds, so the agent reports when it is working and the machine
stays awake until it stops. This is on by default and is the one write on this
page that is not asked about job by job, so it is worth being exact.

It is a setting, `agent_hooks`, and the background service keeps every agent in
line with it. Turning it off takes the configuration back out of every agent it
was added to, rather than merely declining to add more; there is no state where
the setting says one thing and your agents do another. `goguma config set
agent_hooks off`, or the switch in the app's settings, and `goguma hooks` shows
what is in place at any point.

What goes in is a line calling `goguma agent-hook` on that agent's own
prompt, tool-use and stop events, in `~/.claude/settings.json`,
`~/.codex/hooks.json` or `~/.cursor/hooks.json`. It runs nothing that would not
have run anyway and sends nothing anywhere: it opens and closes a sleep hold on
the local socket. Hooks you already have are kept, in their existing order,
alongside it. A copy of the file is written first, and if the rewritten file
does not parse, the original is put back. A configuration goguma cannot read is
refused rather than replaced.

`goguma hooks remove` takes out exactly what it added, leaving the rest of the
file as it was, and `goguma hooks` shows what is in place without changing
anything. There is no third state: goguma either has one line in that file or
none.

**Wrapping a scheduled job.** `goguma import --register` offers to put the
`goguma-mark` wrapper in front of
a job, so the job reports its own start and exit instead of being guessed at
from the process table. Accepting that for a cron job rewrites one line of your
crontab; accepting it for a launchd job rewrites that job's plist and reloads
it. `goguma import` with no flags only reports, and answering no to a job
leaves it alone.

Both writers work the same way, and both assume they will get it wrong:

- the original is copied first, to `crontab.backup` or to `launchd-backup/`
  inside goguma's state directory
- only the one line, or the one plist key, is changed, and the wrapper path is
  written absolute and quoted, because cron's `PATH` doesn't include
  `~/.local/bin` and home directories can contain spaces
- the result is read back and re-parsed, with `plutil -lint` for plists, and if
  it doesn't verify the original goes straight back

They are [`internal/cli/crontab_apply.go`](internal/cli/crontab_apply.go) and
[`internal/cli/launchd_apply.go`](internal/cli/launchd_apply.go), and both have
tests beside them.

Nothing else writes outside `~/Library/Application Support/goguma`. The
background service adopts jobs it finds by recording them in its own list, and
never by editing the scheduler it found them in.

**It also puts the machine back to sleep**, which is a change to the machine's
state rather than to a file, and worth naming here for the same reason.

goguma can't ask for a quiet wake: a scheduled wake is classified by macOS as
user-initiated, so the whole system comes up and other software takes
assertions of its own. Measured on one Mac, a 31-second job at 03:00 left it
awake for six hours after goguma had already released. So after a wake goguma
itself caused, and only where it watched the job start and exit, once every
hold is closed and the keyboard and pointer have been idle for two minutes, it
runs `pmset sleepnow`, the same thing the Apple menu does. A window that closed
without seeing the job finish never triggers it, because the job may still be
running. It's unprivileged, so this doesn't go through the helper and the
privileged surface above is unchanged. Turn it off with `goguma config set
sleep_after_wake off`. The conditions are in
[`internal/daemon/sleepback.go`](internal/daemon/sleepback.go), and every one
of them is a reason not to sleep.

## Installing and removing it

`goguma install` prints what it is about to do, and `--dry-run` prints it
without doing it. The password prompt is macOS's own `sudo`, in your terminal.

**It checks the helper before installing it.** Before anything runs as root,
`codesign --verify --strict` is run against the exact binary about to be
copied, and the identity that signed it is printed. A binary altered after it
was signed fails that check and the install stops. This is the one line in the
output that a tampered copy couldn't produce, because it is macOS reading the
bytes rather than goguma describing itself.
The app opens Terminal for this rather than asking inside a window of its own,
because a program asking for your password in its own text field is the shape
of a phishing prompt, and it is a habit worth not teaching.

`goguma uninstall` removes the background service, the helper and the binaries. Your
jobs, config and run history are kept, so reinstalling picks up where you left
off. Add `--purge` to delete those too.

## Who made this

My name is Juhyun (Jun) Nam. I'm a sophomore at Duke University, and I built
goguma because my own automations weren't running at night. Every commit in the
repository is under the same name, from the first one to the current one, and
there is no organisation behind it and nobody else with push access.

The Mac app is signed with a Developer ID that Apple issued to me by name.
That is checkable without trusting anything on this page:

```sh
codesign -dvvv /Applications/goguma.app
```

The authority chain reads `Developer ID Application: Juhyun Nam (735JVWA424)`,
then Apple's own certification authority, then the Apple Root CA. Apple
verified who I am before issuing that certificate, and the signature breaks if
a single byte of the app changes after I sign it. A build that was tampered
with on the way to you doesn't open.

You can find me on
[LinkedIn](https://www.linkedin.com/in/jun-nam-4ba16b326/), and I'm happy to
answer any questions about goguma at
[junnam586@gmail.com](mailto:junnam586@gmail.com).

## Reporting something

Open an issue at
[github.com/junnam586/goguma/issues](https://github.com/junnam586/goguma/issues).
If it is a vulnerability rather than a bug, say so in the title and leave out
the details, and I will find a private way to hear the rest.
