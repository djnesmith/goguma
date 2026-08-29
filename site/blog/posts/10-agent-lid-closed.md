---
slug: keep-coding-agent-running-lid-closed
order: 10
date: 2026-08-20
title: How to keep Claude Code or Codex running when you close the MacBook lid
description: Why the agent dies the moment the lid shuts, the three ways to fix it ranked by how badly each fails, and the hook configuration for all four major harnesses.
answer: Closing the lid triggers clamshell sleep, which `caffeinate` cannot hold — so the agent freezes mid-task. The only mechanism that holds a closed lid on battery is `sudo pmset -a disablesleep 1`, and using it by hand is risky because **it persists after the process that set it dies**. The safe shape is to have the hold scoped to actual agent activity and expiring on its own.
faq:
  - q: Why does Claude Code stop when I close my MacBook lid?
    a: Closing the lid triggers clamshell sleep, a lower-level path than the idle sleep that caffeinate and menu bar toggles assert against. With no external display and no mains power the Mac sleeps and suspends the agent mid-task.
  - q: Does caffeinate keep Claude Code running with the lid closed?
    a: No. caffeinate prevents idle sleep only. It works with the lid open; a closed lid on battery sleeps regardless.
  - q: What is the command to stop a MacBook sleeping with the lid closed?
    a: sudo pmset -a disablesleep 1, and sudo pmset -a disablesleep 0 to restore normal sleep. The hard part is that it is global and it stays set even if whatever set it is killed.
  - q: Does tmux keep an agent running when the Mac sleeps?
    a: No. tmux keeps a session alive across a disconnected terminal, but a sleeping Mac suspends every process including tmux and everything inside it. It solves closing a window, not closing a lid.
  - q: Which agents can report when they are working?
    a: Claude Code, Codex CLI, Cursor and Gemini CLI all run shell commands at their own lifecycle events, so they can signal when a turn starts and finishes. Anything else can be wrapped at the command line instead.
---

## Why it happens

`caffeinate`, and every menu bar toggle built the same way, asserts against **idle** sleep. Closing the lid is not an idle timeout — it is a hardware event on a separate path, and on a MacBook with no external display and no mains power it puts the machine to sleep unconditionally. The full explanation is in [why caffeinate doesn't work with the lid closed](../caffeinate-lid-closed/).

The agent does not crash. It is suspended mid-token, and when you open the lid it resumes into a connection that timed out hours ago.

## The three fixes, worst to best

### 3. tmux or screen

Commonly suggested, and it does not address this. tmux keeps a session alive when the *terminal* goes away. A sleeping Mac suspends every process on the machine, tmux included. It solves closing a window, not closing a lid.

It is still worth using alongside a real fix, so a closed terminal does not kill a run.

### 2. `pmset -a disablesleep 1` by hand

```sh
sudo pmset -a disablesleep 1     # before you walk away
sudo pmset -a disablesleep 0     # when you come back
```

This genuinely works. Two problems.

**Remembering the second line.** This is the classic flat-battery-in-a-bag story, and it is not a discipline problem — you set it expecting to be back in twenty minutes and then the day happens.

**It persists past the thing that set it.** [`disablesleep`](../pmset-disablesleep/) is written into the power management preferences and is **not** cleared when the process that set it exits or is killed. A script that sets it and dies before its cleanup line leaves a Mac that cannot sleep at all until someone works out why. Anything automating this needs a dead-man switch — something independent that clears the setting when the owner disappears.

### 1. Scope the hold to the work

The right shape: the machine stays awake exactly while the agent is doing something, and sleeps normally when it is not. That needs an answer to "is it working right now?", and there is [a genuinely surprising result there](../cannot-detect-ai-agent-working/) — you cannot tell by watching.

Measured across four live sessions over ten wall seconds, the **idle** one used 0.50s of CPU and the **actively working** one used 0.32s. The agent spends its time blocked on a socket waiting for a model; the work is not on your machine, so there is nothing local to observe.

So the agent has to say so. All four major harnesses can:

| harness | config file | fires while working | fires when finished |
|---|---|---|---|
| Claude Code | `~/.claude/settings.json` | `UserPromptSubmit`, `PostToolUse` | `Stop` |
| Codex CLI | `~/.codex/hooks.json` | `UserPromptSubmit`, `PostToolUse` | `Stop` |
| Cursor | `~/.cursor/hooks.json` | `beforeSubmitPrompt`, `afterFileEdit` | `stop` |
| Gemini CLI | `~/.gemini/settings.json` | `BeforeAgent`, `AfterTool` | `AfterAgent`, `SessionEnd` |

Two traps if you wire this yourself:

**Gemini's `SessionStart`/`SessionEnd` are the wrong events.** They look like the obvious pair, but they are the *process* boundaries, not the *work* boundaries. `SessionStart` fires when the CLI launches — so a session left open at an idle prompt would hold sleep off indefinitely. `BeforeAgent` and `AfterAgent` are the per-turn events that correspond to Claude Code's `UserPromptSubmit` and `Stop`.

**The config shapes differ.** Claude Code, Codex and Gemini CLI all nest as `hooks.<Event> = [{ hooks: [{type, command}] }]`. Cursor is flat with a top-level `version: 1`. Write one into the other's file and the tool silently ignores it — no error, no hook, no way to tell from outside that nothing is armed.

## The part that is easy to get wrong

A hook says "a turn started". Nothing guarantees the matching "it finished" arrives — the agent can crash, be killed, lose the network, or have its terminal closed. If only a stop event releases the hold, one missed event means a Mac that never sleeps again.

So the hold has to be a **lease**: it expires unless something keeps renewing it. A missed stop event then costs a bounded amount of extra wakefulness instead of an unbounded one.

goguma uses 15 minutes for an agent session, a hard ceiling of 12 hours however often it is renewed, and a helper that clears a stranded block after 60 seconds without contact. It also drops every hold if a closed-lid machine gets hot or the battery falls, which matters here more than anywhere — see [is it safe to keep a MacBook awake in a bag](../macbook-awake-in-bag-safe/).

## For agents without hooks

Anything you launch from a terminal can be wrapped, hooks or not:

```sh
goguma run -- aider --model sonnet
goguma run -- opencode
goguma run -- npm run build:prod
```

The hold opens when the command starts and closes when it exits. No integration, no plugin, no cooperation from the tool — which means it also works for whatever ships next month.

This is deliberately not done by writing a shell alias into your `~/.zshrc`. An alias is a permanent edit to a file you own, it only catches shells that read that file, and it misses editor-launched sessions entirely. Wrapping the command you actually meant to wrap is smaller and needs no uninstall.

## Before you walk away

A closed MacBook that cannot sleep is a sealed box with no airflow and no way to signal you. Hard surface over bag, mains power if you have it, and make sure whatever is holding it awake will let go on its own.
