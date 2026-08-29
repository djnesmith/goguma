---
slug: what-woke-my-mac
order: 12
date: 2026-08-28
title: How to find out what woke your Mac
description: Your Mac keeps a log of every sleep and wake, with a reason for each. Here is how to read pmset -g log, what the wake reasons mean, and how to tell a real wake from a maintenance dark wake.
answer: Run `pmset -g log | grep -E "Wake from|DarkWake"` to see every wake with a timestamp and a cause. The token after "due to" names the reason — `lid` is the lid opening, `trackpadkeyboard` is input, `rtc` is a scheduled wake somebody armed, `wifibt` is the network card. Lines that say DarkWake rather than Wake are short maintenance wakes where the screen never turns on; they are normal and typically last a few seconds.
faq:
  - q: Why does my Mac wake up at night by itself?
    a: Usually one of three things — a scheduled wake something armed (visible in `pmset -g sched`), Wake for network access responding to traffic (the `womp` setting), or a maintenance dark wake, which is normal, brief, and does not turn on the screen.
  - q: What is a DarkWake in the pmset log?
    a: A wake in which the system comes up to do background housekeeping without turning on the display. macOS punctuates a long sleep with these; they typically last seconds. A line reading "DarkWake to FullWake" means a dark wake was promoted to a real one, usually because you touched the machine.
  - q: How do I see scheduled wakes on my Mac?
    a: '`pmset -g sched` lists every pending scheduled power event with the time it will fire and the name of what scheduled it.'
  - q: How far back does the pmset log go?
    a: Days, typically — it is capped by size rather than by time, so a busy machine keeps less history than a quiet one.
---

## The log that already has the answer

macOS records every transition in and out of sleep, with a timestamp and a cause. Nothing needs to be installed or enabled first:

```sh
pmset -g log | grep -E "Wake from|DarkWake"
```

Real lines from a real machine, trimmed for width:

```
16:27:28  DarkWake from Deep Idle : due to smc.sysState.Wake wifibt
16:37:34  Wake from Deep Idle     : due to smc.sysState.Wake lid
17:55:38  DarkWake to FullWake    : due to UserActivity Assertion
20:53:08  Wake from Hibernate     : due to trackpadkeyboard/UserActivity
```

Each line answers the question directly: when, from how deep a sleep, and why.

## Reading the reasons

The token after `due to` is the cause. The common ones:

| Token | What it means |
|---|---|
| `lid` | The lid was opened |
| `trackpadkeyboard` | Keyboard or trackpad input |
| `UserActivity` | An assertion declared a user present — usually you touching it |
| `rtc` | The real-time clock fired — **a scheduled wake something armed** |
| `wifibt` | The wireless hardware — network traffic, or a nearby device |
| `EC.ACAttach` / power tokens | The charger was plugged in |

Two of these deserve a closer look, because they are the ones that wake machines nobody is touching.

**`rtc` is a scheduled wake.** Something asked the machine to wake at that exact time. The pending ones are listed, with names attached:

```sh
$ pmset -g sched
Scheduled power events:
 [0]  wake at 08/28/2026 21:22:55 by 'goguma'
 [1]  wake at 08/29/2026 00:00:00 by 'com.apple...donotdisturb...timer'
```

The owner string tells you who to go and ask. Anything from `pmset schedule` shows here, including [wakes armed for scheduled jobs](../wake-mac-for-scheduled-job-pmset/), alongside Apple's own timers.

**`wifibt` with Wake for network access.** If the `womp` setting is on (it is by default on AC — "Wake for network access" in System Settings), the machine comes up briefly to answer network traffic. These are almost always dark wakes.

## Wake versus DarkWake — the distinction that explains most mysteries

A line that starts `Wake` is the real thing: the machine came up fully. A line that starts `DarkWake` is housekeeping — the system runs briefly with the display off, then goes back to sleep. macOS punctuates a long sleep with these, and they typically last a few seconds each.

If you are investigating "my Mac wakes up at night", sort the lines first: a string of DarkWakes is a machine behaving normally, not a problem to fix. A full `Wake` at 03:12 with `rtc` as the reason is a scheduled wake, and `pmset -g sched` will tell you whose. What dark wakes are for, and why your jobs do not run during them, is its own subject — [covered here](../dark-wake-power-nap/).

## Turning the question around

The same log answers the opposite question, which is usually the one that matters more: not "what woke it" but "what did it sleep through". Every interval between a `Sleep` line and the next `Wake` line is time your scheduled jobs did not exist for — and [cron does not tell you what it missed](../cron-jobs-dont-run-mac-asleep/). Replaying each job's schedule against these intervals is how you [find the runs that silently never happened](../find-missed-scheduled-jobs-mac/); it is also the first thing goguma does when installed, using exactly this log.
