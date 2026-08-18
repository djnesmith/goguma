# Keeping the Mac awake for long jobs and coding agents

[README](../README.md) ·
[Architecture](ARCHITECTURE.md) ·
[Security](../SECURITY.md) ·
[getgoguma.com](https://getgoguma.com)

A coding agent runs for as long as it runs. Close the lid halfway through and
the Mac sleeps, the agent stops mid-task, and you come back to a session that
went nowhere. The same is true of a long build or a big sync.

goguma covers this two ways, depending on whether you get to type the command.

## If you launch it yourself

```sh
goguma run -- claude -p "refactor the auth module"
goguma run -- make -j8 release
goguma run --label nightly -- rsync -a ~/work backup:/work
```

Sleep is held off for exactly as long as the command runs, lid closed included,
and released when it exits. Output, input and exit status pass straight through,
so this can go in front of anything without changing what it does.

This is the exact version: the hold begins and ends with the process, with no
window to guess at. Prefer it wherever you have the choice.

## If it runs inside an editor

An agent embedded in an IDE never gives you a command to wrap, and it cannot be
watched from outside either.

**This is worth being precise about, because the obvious approach does not
work.** The agent process is there whether or not it is doing anything: on one
machine four of them had been alive between three and twenty-eight hours. Nor
does CPU distinguish them. Measured over ten wall seconds:

| process | CPU used | state |
|---|---|---|
| 32126 | 0.32s | **actively working** |
| 34832 | 0.25s | idle |
| 77384 | 0.50s | idle |
| 80257 | 0.05s | idle |

The idle session used more CPU than the working one, because the work is not
happening on your machine. An agent waiting for a model is a process blocked on
a socket, and looks exactly like one doing nothing.

So the agent says so instead, and `goguma install` sets that up for every agent
it finds. There is nothing to do. To check, add or undo it by hand:

```sh
goguma hooks              # what is set up
goguma hooks install      # set up everything found
goguma hooks remove       # take it back out
```

| Harness | Configured in | Events used |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | `UserPromptSubmit`, `PostToolUse`, `Stop` |
| Codex CLI | `~/.codex/hooks.json` | `UserPromptSubmit`, `PostToolUse`, `Stop` |
| Cursor | `~/.cursor/hooks.json` | `beforeSubmitPrompt`, `afterFileEdit`, `stop` |

Restart the agent afterwards; hooks are read when it starts.

### How it behaves

**It stops when the agent stops, not on a timer.** The stop event releases the
hold immediately. Prompts and tool calls renew it, so an agent that works for
four hours holds for four hours, and one that finishes in twenty seconds
releases in twenty seconds.

**Each session holds separately.** Two editors open is ordinary, and the one
that finishes first must not sleep the machine under the one still going. The
hold is keyed by the session id the harness reports, so it doesn't.

**A crashed agent cannot strand the hold.** The stop event is the ordinary path,
and a harness killed outright never fires it. So the hold is leased: fifteen
minutes without a word from that session and it lapses by itself. Fifteen rather
than one, because a model can think for minutes between tool calls with nothing
happening locally, and a shorter lease would drop the hold in the middle of
exactly the work this exists for.

**An event goguma has not heard of renews rather than releases.** Harnesses add
events; being wrong in that direction keeps a working machine awake, and being
wrong in the other sleeps it mid-run.

**Your existing hooks are kept.** goguma adds one line beside them, backs the
file up first, and puts the original back if the result does not parse. See
[Security](../SECURITY.md#what-it-writes).

### If your agent is somewhere else

Anything you launch from a terminal can be wrapped whether or not it has hooks:

```sh
goguma run -- codex exec "refactor the auth module"
```

An agent running in a browser tab, or one of the cloud agents, needs nothing at
all. The work is happening on somebody else's server, so your Mac sleeping does
not interrupt it, and there is nothing local to hold awake.

## What still applies

Everything in [Security](../SECURITY.md) is unchanged by either route.

- **The cutouts still fire.** Above 80°C or below 10% battery the hold is
  released, whichever route opened it. A closed laptop running an agent flat out
  on battery will reach both, so keep it on power
- **Nothing here reaches the privileged helper.** `goguma run` executes your
  command as you, with no shell in between
- **A hold cannot be stranded.** A wrapped command's hold is leased and expires
  by itself if the wrapper is killed; a hooked window is bounded by the duration
  you set, and no hold outlives 12 hours
- **Neither is recorded as a job run.** They teach the estimator nothing and
  appear in no job's history, only in the event log
