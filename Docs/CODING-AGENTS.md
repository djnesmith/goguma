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

So the harness has to say so, and every major one can. All four run shell
commands at lifecycle events:

| Harness | Events | Config |
|---|---|---|
| Claude Code | `UserPromptSubmit`, `PostToolUse`, `Stop` | `~/.claude/settings.json` |
| Cursor | `beforeSubmitPrompt`, `postToolUse`, `sessionEnd` | `hooks.json` |
| GitHub Copilot | `userPromptSubmitted`, `postToolUse`, `agentStop` | `.github/hooks/*.json` |
| Codex CLI | `UserPromptSubmit`, `PostToolUse`, `Stop` | `hooks.json` (experimental) |

The command to run is the same everywhere:

```sh
~/.local/bin/goguma awake 15m
```

Claude Code, in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "~/.local/bin/goguma awake 15m" }] }
    ],
    "PostToolUse": [
      { "hooks": [{ "type": "command", "command": "~/.local/bin/goguma awake 15m" }] }
    ]
  }
}
```

The other three take the same command at the events named above. Their exact
file shapes differ and each vendor documents its own; the part that matters here
is that it is one shell command with no arguments to get wrong.

### Why a rolling window, and why nothing on stop

`goguma awake` replaces its window rather than extending it, so calling it again
pushes the deadline out instead of stacking. That gives two properties worth
having:

**Hooking `PostToolUse` as well as the prompt makes it a heartbeat.** A session
that submits one prompt and then works for forty minutes keeps renewing itself
every time the agent runs a tool, so the window follows the work rather than
being a guess made at the start.

**Nothing is hooked to stop, deliberately.** Two editor windows share one
machine, and `goguma awake off` from the session that finished first would
release the hold out from under the one still running. Letting the window lapse
costs up to fifteen idle minutes and cannot cut anybody's session short.

If a single tool call runs longer than the window with nothing else firing, the
hold lapses early. Lengthen it if that happens to you, or use `goguma run` for
that particular job, which has no window at all.

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
