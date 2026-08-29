---
slug: dark-wake-power-nap
order: 15
date: 2026-08-28
title: "Dark wake and Power Nap: when your Mac is awake but not really"
description: macOS punctuates a long sleep with brief wakes that never turn on the screen. What dark wakes are for, how to spot them in the log, and why your scheduled jobs do not run during them.
answer: A dark wake is a brief system wake with the display off, used for background housekeeping — checking in with the network, and on Power Nap machines fetching mail and running backups. They typically last a few seconds and appear in `pmset -g log` as DarkWake lines. They are not usable time for your own scheduled work — cron and launchd jobs cannot be counted on to run during one, so a machine that dark-wakes all night has still, for your purposes, slept through the night.
faq:
  - q: What is a dark wake on a Mac?
    a: A wake in which the system comes up with the display off, does brief background work, and returns to sleep. The pmset log records them as DarkWake lines, distinct from full Wake lines.
  - q: Is a dark wake the same as Power Nap?
    a: They are related but not identical. Dark wake is the mechanism — a display-off wake. Power Nap is a feature (the powernap setting in pmset) that uses such wakes on supported machines for tasks like fetching mail and running backups.
  - q: Do cron jobs run during a dark wake?
    a: Not usefully. Dark wakes are seconds long and exist for the system's own housekeeping. A job whose fire time falls during sleep is missed even if dark wakes punctuated that sleep, so the fix is a real scheduled wake, not Power Nap.
  - q: Should I turn Power Nap off?
    a: Usually no. Its wakes are brief and cheap, and on supported machines they keep mail, Find My, and Time Machine fresher than plain sleep would. Turn it off only if you have traced a specific problem to it.
---

## Not all wakes are equal

Sleep on a modern Mac is not the unbroken absence of activity it appears to be. Watch [the power log](../what-woke-my-mac/) after a night of "sleep":

```sh
pmset -g log | grep -E "Wake from|DarkWake"
```

```
16:27:28  DarkWake from Deep Idle : due to smc.sysState.Wake wifibt
16:37:34  Wake from Deep Idle     : due to smc.sysState.Wake lid
17:55:38  DarkWake to FullWake    : due to UserActivity Assertion
```

Two different things are called waking here. A `Wake` line is the machine genuinely coming up — display on, everything running, staying up until something lets it sleep. A `DarkWake` line is the half-state: the system comes up with the display off, does its errand, and drops back to sleep, typically within seconds.

The third line shows the promotion path: a `DarkWake to FullWake` is a dark wake that got upgraded to the real thing, usually because you touched the machine while it happened to be up.

## What dark wakes are for

The system uses them for its own maintenance: keeping its network presence alive, servicing timers, and — on machines with Power Nap enabled — the feature's advertised work. Power Nap is the `powernap` setting (`pmset -g` shows yours; it is on by default on supported machines) and it is a *policy* about what may happen during these display-off wakes: checking mail, App Store updates, Time Machine.

The distinction worth keeping: **dark wake is the mechanism, Power Nap is a feature built on it.** Turning `powernap` off does not end dark wakes; the system still uses them for its own bookkeeping.

## Why your jobs still count as missed

Here is the trap in reasoning: "the log shows the machine woke twelve times last night, so my 3am job had chances to run."

It did not, for two reasons:

- **Dark wakes are seconds long.** They are shaped for the system's errands, not for user work. A backup script, a build, a sync — none of it fits in the window, and the system is actively heading back to sleep the whole time.
- **Your schedulers were effectively absent.** [cron skips any minute that passes while the machine sleeps](../cron-jobs-dont-run-mac-asleep/), and a seconds-long display-off wake at 02:41 does not change what happens at 03:00. From your crontab's point of view, a night of dark wakes and a night of unbroken sleep are the same night.

This is also why counting missed jobs from the log requires care: naively treating every DarkWake as "the machine was up" produces a rosy picture in which nothing was ever missed. When [replaying a schedule against sleep history](../find-missed-scheduled-jobs-mac/), gaps of a few seconds between sleep intervals have to be treated as sleep — goguma coalesces any gap under 90 seconds for exactly this reason, because a machine that was technically up for four seconds at 02:41 was not, in any sense that matters to a job, awake.

## What to do instead

If the goal is background work happening at a specific time on a sleeping Mac, the mechanism is a real scheduled wake — `pmset schedule` for [a particular job's fire time](../wake-mac-for-scheduled-job-pmset/), or `pmset repeat` for [a fixed daily routine](../pmset-repeat-wake-schedule/). A scheduled wake brings the machine fully up at a time you chose, and with something holding sleep off for the job's duration, the work actually happens.

Power Nap is not that mechanism, and was never meant to be. It keeps Apple's things fresh during sleep. Yours still need the machine actually awake.
