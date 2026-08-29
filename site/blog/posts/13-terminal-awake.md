---
slug: keep-mac-awake-terminal-command
order: 13
date: 2026-08-28
title: How to keep a Mac awake from the terminal until a command finishes
description: caffeinate can scope a sleep hold to exactly one command or process, so the Mac stays awake precisely as long as the work runs. The flags, the traps, and the one case it cannot cover.
answer: Prefix the command with caffeinate — `caffeinate -i ./build.sh` holds off idle sleep until build.sh exits, then releases automatically. To cover an already-running process, use `caffeinate -i -w <pid>`, which releases when that pid exits. The hold is scoped to the work, which is the whole point — nothing to remember to turn off. It does not survive the lid closing, and `-s` only works on AC power.
faq:
  - q: How do I stop my Mac sleeping while a script runs?
    a: Run the script under caffeinate — `caffeinate -i ./script.sh`. The assertion is created when the command starts and released when it exits, so the machine can sleep again the moment the work is done.
  - q: How do I keep the Mac awake for a process that is already running?
    a: '`caffeinate -i -w <pid>` waits on that pid and holds sleep off until it exits.'
  - q: Does caffeinate keep the Mac awake with the lid closed?
    a: No. All of its assertions prevent idle sleep; closing the lid is a different kind of sleep and wins. On battery, a closed lid sleeps the machine regardless of caffeinate.
  - q: What is the difference between caffeinate -i and -s?
    a: -i prevents idle system sleep and works on battery. -s prevents system sleep but is only valid on AC power — on battery it holds nothing.
---

## Scope the hold to the work

The naive way to keep a Mac awake for a long task is to open System Settings and set sleep to Never, run the task, and try to remember to set it back. The failure mode is the second half: nobody remembers, and the machine spends the next month never sleeping.

The right shape is a hold that is created by the work and dies with the work. That is exactly what caffeinate does when you give it a command:

```sh
caffeinate -i ./build.sh
```

caffeinate starts `build.sh`, holds an assertion against idle sleep for as long as it runs, and releases it the instant the process exits — success, failure, or ctrl-C. There is nothing to remember, because there is nothing left behind.

For work that is already running, scope to its pid instead:

```sh
caffeinate -i -w 48291
```

The assertion is released when that process exits.

## The flags that matter

From `caffeinate(8)`, condensed to the ones you will actually use:

| Flag | Holds off | Notes |
|---|---|---|
| `-i` | Idle system sleep | The default choice. Works on battery. |
| `-d` | Display sleep | Add it if something on screen must stay visible |
| `-s` | System sleep | **AC power only** — on battery it holds nothing |
| `-m` | Disk idle sleep | Rarely needed on SSDs |
| `-w <pid>` | — | Scope the hold to an existing process |
| `-t <secs>` | — | Timed hold, no command attached |

The `-s` caveat is the one that bites people: it reads like the strongest option, and on a plugged-in machine it is, but the man page is explicit that the assertion is valid only on AC power. A laptop that leaves the desk mid-task is no longer holding anything.

You can watch any of these holds exist and disappear with `pmset -g assertions`, which lists every active assertion and the process behind it.

## What this cannot cover

Two boundaries, one obvious and one not:

**The lid.** Every caffeinate assertion prevents some form of idle sleep. Closing the lid is not idle sleep, and it wins — [in detail here](../caffeinate-lid-closed/). If the laptop will be closed while the work runs, you are in [different territory](../keep-macbook-awake-lid-closed/).

**Work that doesn't map to a process.** `caffeinate -i cmd` is perfect when one process is the work. An AI coding agent breaks that assumption: the process sits blocked on a network socket for minutes at a time, [looking exactly like an idle one](../cannot-detect-ai-agent-working/), and the session outlives any single command. Scoping a hold to agent activity takes cooperation from the agent's harness, which is [its own subject](../keep-coding-agent-running-lid-closed/). For a plain long command, though — a build, a training run, an export, a huge copy — the wrapper pattern is exactly right, and `goguma run -- <command>` is the same idea with expiry and safety cutouts attached.

## The case none of this covers

caffeinate, and everything else on this page, keeps an awake machine awake. If the machine will already be asleep when the work is due — the 3am cron job, the backup before you get up — no assertion helps, because there is nothing awake to hold. That case needs [a scheduled wake](../wake-mac-for-scheduled-job-pmset/), which is a different mechanism entirely.
