---
slug: cannot-detect-ai-agent-working
order: 8
date: 2026-08-20
title: You cannot tell whether an AI coding agent is working by watching it
description: Measured over ten seconds, the idle Claude Code session used more CPU than the one actively working. Here is the data, and why every process-watching approach to agent sleep prevention is built on sand.
answer: An agent waiting on a model is a process blocked on a socket, and it looks exactly like one doing nothing. Measured across four live sessions over ten wall seconds, the **idle** session used 0.50s of CPU while the **actively working** one used 0.32s. The work is not happening on your machine, so there is nothing local to observe. The harness has to tell you.
faq:
  - q: Can you detect an active coding agent by CPU usage?
    a: No. In a measurement across four concurrent sessions over ten wall seconds, the busiest process by CPU was idle and the actively working process ranked third of four. The agent spends nearly all of its wall time blocked on a network socket waiting for the model, which costs almost no CPU.
  - q: Can you detect it by whether the process exists?
    a: No. The process exists for the whole session whether or not anything is happening. On the machine measured, four agent processes had been alive between three and twenty-eight hours, almost all of it idle at a prompt.
  - q: Can you detect it by network activity?
    a: Not reliably. A streaming response is a trickle of bytes on a long-lived connection, indistinguishable from an editor's telemetry or a sync client. It also disappears entirely between turns, which is exactly when you still need the machine awake.
  - q: So how should it be detected?
    a: The harness reports it. Claude Code, Codex CLI, Cursor and Gemini CLI all run shell commands at their own lifecycle events, so they can say when a turn starts and when it ends. That is the harness stating something no external observer could work out.
---

## The measurement

Four coding agent sessions were running on one machine. Their CPU consumption was sampled over ten wall seconds, and one of them was known to be mid-task — actively editing files and running tests.

| process | CPU used over 10s | state |
|---|---|---|
| 32126 | 0.32s | **actively working** |
| 34832 | 0.25s | idle |
| 77384 | 0.50s | idle |
| 80257 | 0.05s | idle |

The working session ranked **third of four**. The busiest process on the list was doing nothing.

This is not a measurement error or an unlucky sample. It is what the architecture predicts. An agent's work happens on somebody else's GPUs. Locally, the process spends nearly all of its wall time blocked in a `read` on a socket, waiting for tokens to come back. Blocking on a socket costs no CPU. What little the process does spend goes on parsing the stream, redrawing a terminal, and running the occasional tool — bursty, brief, and easily beaten by an idle session that happens to be repainting a spinner.

## Why the other signals fail too

**Process presence.** The obvious approach — is `claude` running? — fails because the process is there for the entire session regardless. On the machine above, the four agent processes had been alive **between three and twenty-eight hours**, and were idle at a prompt for nearly all of it. Treating "the binary is running" as "work is happening" means holding a laptop awake all night because someone left a tab open.

**Network traffic.** A streaming response is a slow trickle of bytes on one long-lived TLS connection. On a normal developer machine that is indistinguishable from an editor phoning home, a Dropbox sync, or a package manager checking for updates. Worse, it goes to zero *between* turns — the exact moment when a multi-step agent is about to start its next tool call and you still need the machine awake.

**Window focus, or recent keystrokes.** These measure the human, not the agent. The entire point of a long agent run is that the human has walked away.

**Child processes.** An agent running `npm test` does spawn something visible. But that is a fraction of the session, and the gaps between tool calls — waiting for the model to decide what to do next — are both invisible and precisely when sleep would kill the run.

Every one of these has the same shape of failure: it either holds the machine awake when nothing is happening, or it lets the machine sleep while something is.

## The consequence for tools in this space

There are now several tools that keep a Mac awake for AI agents. It is worth knowing which of the two approaches each one takes, because it determines how they behave when you walk away.

| approach | holds awake when | releases when |
|---|---|---|
| Process watching | the agent process exists | the process exits, or a CPU heuristic guesses idle |
| Lifecycle hooks | the harness reports a turn started | the harness reports it finished, or the lease expires |

Process watching is the more permissive of the two, and permissive is the wrong direction for something whose failure mode is a laptop that never sleeps. An agent sitting idle at a prompt overnight is a live process. A tool that watches processes holds sleep off for it.

That is why some of them bolt a CPU-idle heuristic on top — and the table above is exactly why that heuristic cannot work.

## What the harnesses actually give you

Every major agent runs shell commands at its own lifecycle events. That is the harness telling you something no observer outside it could determine.

| harness | config | events that mean "working" | event that means "finished" |
|---|---|---|---|
| Claude Code | `~/.claude/settings.json` | `UserPromptSubmit`, `PostToolUse` | `Stop` |
| Codex CLI | `~/.codex/hooks.json` | `UserPromptSubmit`, `PostToolUse` | `Stop` |
| Cursor | `~/.cursor/hooks.json` | `beforeSubmitPrompt`, `afterFileEdit` | `stop` |
| Gemini CLI | `~/.gemini/settings.json` | `BeforeAgent`, `AfterTool` | `AfterAgent`, `SessionEnd` |

Two traps in that table, both of which produce a tool that appears to work and does not.

**The event names are not translations of each other.** Gemini CLI has `SessionStart` and `SessionEnd`, which look like the obvious pair to use. They are the *process* boundaries, not the *work* boundaries — `SessionStart` fires when the CLI launches, which for a session left open at an idle prompt means holding sleep off indefinitely. `BeforeAgent` and `AfterAgent` are the per-turn events, and those are the ones that correspond to Claude Code's `UserPromptSubmit` and `Stop`.

**The config shapes differ.** Claude Code, Codex and Gemini CLI all nest as `hooks.<Event> = [{ hooks: [{type, command}] }]`. Cursor is flat, with a top-level `version: 1`. Writing one into the other's file produces a config the tool silently ignores — no error, no hook, and no way to tell from the outside that nothing is armed.

## Hooks alone are not sufficient either

A hook says "a turn started". Nothing guarantees the matching "it finished" ever arrives. The agent can crash, be killed, hit a network failure, or have its terminal closed. If the only thing that releases the hold is a stop event, a missed stop event means a Mac that never sleeps again — which is the failure people actually fear, and the reason they don't trust these tools.

The fix is that the hold has to be a **lease**: it expires on its own unless something keeps renewing it. Then a missed stop event costs you a bounded amount of extra wakefulness rather than an unbounded one. goguma uses 15 minutes for an agent session, with a hard ceiling of 12 hours however often it is renewed, and its privileged helper clears a stranded sleep block after 60 seconds without contact from the daemon.

That last one matters more than it sounds. `pmset disablesleep` is a global setting that persists in the power management preferences — it is **not** cleared when the process that set it dies. Anything that sets it and is then killed leaves a Mac that cannot sleep until a human notices and runs `pmset` by hand.

## The summary

You cannot observe agent work from outside the agent, because the work is not on your machine. Any tool claiming to detect it by watching is either watching process lifetime — which over-holds — or watching CPU, which the table at the top of this page shows does not distinguish the two states at all.

The harness has to say so. And because the harness can fail to say so, whatever holds the machine awake has to expire by itself.
