---
slug: caffeinate-vs-pmset-vs-amphetamine
order: 5
date: 2026-08-20
title: caffeinate vs pmset vs Amphetamine — what each one actually does
description: Three tools people reach for interchangeably that behave completely differently with the lid closed, on battery, and when the thing holding the assertion dies.
answer: `caffeinate` asserts against idle sleep for the life of a command and releases automatically — but a closed lid defeats it. `pmset -a disablesleep 1` does hold a closed lid awake, but it is a global setting that **persists after the process that set it dies**. Amphetamine is a GUI wrapper with triggers and timers that has to be turned off manually. None of the three can wake a Mac that is already asleep.
faq:
  - q: What is the difference between caffeinate and pmset?
    a: caffeinate creates a power assertion scoped to a process or a duration, and macOS drops it automatically when that ends. pmset -a disablesleep 1 changes a global system setting that stays changed until something sets it back, including across the death of whatever set it.
  - q: Is Amphetamine better than caffeinate?
    a: It is more convenient rather than more capable. Amphetamine gives you a menu bar UI, triggers and timers over broadly the same underlying mechanisms, which matters if you want app-based or drive-based activation. For wrapping a single command, caffeinate is built in and releases by itself.
  - q: Which one keeps a MacBook awake with the lid closed?
    a: Only the pmset disablesleep route, or an app that uses it. caffeinate asserts against idle sleep, and closing the lid triggers a separate clamshell sleep path that the assertion does not cover.
  - q: Can any of them wake a sleeping Mac?
    a: No. All three prevent an awake machine from sleeping. Waking a sleeping one requires pmset schedule, which is a different command and a different mechanism.
  - q: What happens if the process holding the assertion is killed?
    a: With caffeinate, the assertion dies with the process and normal sleep resumes — this is the correct behaviour. With pmset -a disablesleep 1, the setting survives, so the Mac cannot sleep until something explicitly sets it back to 0.
---

## The one-line version

| | holds idle sleep | holds a closed lid | releases by itself | can wake a sleeping Mac |
|---|---|---|---|---|
| `caffeinate` | yes | **no** | yes, when the command exits | no |
| `pmset -a disablesleep 1` | yes | yes | **no** | no |
| Amphetamine | yes | yes | on a timer or trigger you set | no |
| `pmset schedule wake` | — | — | — | **yes** |

The last row is the one worth staring at. The first three are the same category of thing — stop an awake machine sleeping — and people compare them as though the category covered the whole problem. It does not. If your Mac is already asleep at 02:59 and a job is due at 03:00, none of the first three help at all.

## caffeinate

Built into macOS. It creates a power assertion and holds it for a duration or for the lifetime of a command:

```sh
caffeinate -s /usr/local/bin/nightly-backup   # until the command exits
caffeinate -t 3600                            # for an hour
caffeinate -w 4821                            # until PID 4821 exits
```

The `-s` form is the right shape for a job: the hold lasts exactly as long as the work and is released by the kernel when the process ends, including if it crashes or is killed. There is no state to clean up.

**Its limit is the lid.** A power assertion is a request not to go to *idle* sleep. Closing the lid takes a different, lower-level path. On a MacBook with no external display and no mains power, the lid closing puts the machine to sleep and your caffeinated job stops mid-run. This surprises people constantly, because the tool did nothing wrong — it is doing exactly what it says, and what it says is narrower than what people assume.

## pmset -a disablesleep 1

This is the blunt instrument, and it does work with the lid closed:

```sh
sudo pmset -a disablesleep 1    # the Mac now cannot sleep, at all
sudo pmset -a disablesleep 0    # back to normal
```

Two things make it dangerous in a way `caffeinate` is not.

**It is global.** There is no scoping to a process, a duration, or a reason. The Mac cannot sleep. Not idle sleep, not lid sleep, not low-power sleep. Everything.

**It persists.** This is the part that catches people. `disablesleep` is written into the power management preferences. It is **not** cleared when the process that set it exits, crashes, or is force-killed. A script that sets it and dies before its cleanup line leaves a Mac that will never sleep again until a human notices and runs `pmset` by hand — and the symptom is a laptop that arrives somewhere with a flat battery and a warm chassis, hours later, with nothing to point at the cause.

Any tool built on `disablesleep` therefore needs a dead-man switch: something that notices the owner is gone and clears the setting. goguma's privileged helper clears a stranded block after 60 seconds without contact from the daemon, for exactly this reason.

## Amphetamine

A well-made free menu bar app with a real feature set: triggers on app launch, on connecting a specific drive, on an audio device being active, plus timers and session control. If you want "keep the Mac awake whenever Logic is open", it is the tool.

Its shape is the same as the others though: it holds an awake machine awake, and **you have to remember to turn it off.** The triggers reduce how often you have to, but the failure mode is unchanged — a session left on, a laptop in a bag, a flat battery. That is the single most common complaint about every tool in this category, and it is a design consequence rather than a bug.

## What the comparison misses entirely

All three answer "how do I stop my Mac sleeping". Most people arriving at that question actually have one of two different problems:

**"My scheduled job didn't run."** The machine was asleep when it was due. Keeping it awake from now until then is the wrong shape — you would be running a laptop at desk power all night to do ninety seconds of work at 03:00. What you want is for it to *wake* for the job and sleep again afterwards, which needs [`pmset schedule`](../wake-mac-for-scheduled-job-pmset/), a different mechanism from all three above.

**"My agent stopped when I closed the lid."** Here you do want a hold, and it does have to survive the lid, so `disablesleep` is the right mechanism. But it should be scoped to the work rather than left on, and you cannot tell when the work is happening by [watching the process](../cannot-detect-ai-agent-working/) — the agent has to report it.

## Choosing

- **Wrapping one command, lid open** — `caffeinate -s`. Built in, correct, self-releasing.
- **A GUI toggle with triggers** — Amphetamine, or the free alternatives in [Amphetamine alternatives](../amphetamine-alternatives-mac/).
- **Lid closed, no external display** — you need `disablesleep`, so use something that manages it with an automatic release rather than setting it by hand.
- **A job that fires while the Mac is asleep** — none of them. You need a scheduled wake.
