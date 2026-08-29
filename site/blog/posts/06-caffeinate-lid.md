---
slug: caffeinate-lid-closed
order: 6
date: 2026-08-20
title: Why caffeinate doesn't work when you close the lid
description: caffeinate asserts against idle sleep. Closing the lid is a different sleep path entirely, which is why your job dies the moment the MacBook shuts — and what actually holds it.
answer: `caffeinate` creates a power assertion against **idle** sleep. Closing the lid triggers clamshell sleep, a separate and lower-level path that the assertion does not cover, so a MacBook with no external display and no mains power sleeps anyway. The only mechanism that holds a closed lid awake is `pmset -a disablesleep 1`.
faq:
  - q: Does caffeinate work with the lid closed?
    a: No, not on a MacBook with no external display. caffeinate prevents idle sleep, and closing the lid takes a different path. With an external display and mains power the Mac enters clamshell mode and stays awake, but that is the display and power doing it, not caffeinate.
  - q: What does hold a MacBook awake with the lid closed?
    a: sudo pmset -a disablesleep 1 is the only supported mechanism. It is global and it persists after the process that set it dies, so it needs to be set back to 0 explicitly.
  - q: Does caffeinate -d help?
    a: No. -d prevents the display sleeping, which is a narrower assertion, not a broader one. None of caffeinate's flags reach the clamshell path.
  - q: Will a dummy HDMI plug work?
    a: Often yes — it makes macOS believe an external display is attached, which enables clamshell mode. It is a hardware workaround for a software problem, and it requires mains power on most models.
  - q: Do private APIs like setClamShellSleepDisable work?
    a: No. Verified on-device on macOS 26.3, the private RootDomainUserClient selector 12 returns success but the Mac still sleeps — it governs the external-display clamshell path, not lid close with no display.
---

## Two different sleeps

macOS does not have one sleep. The two that matter here are:

**Idle sleep.** Nothing has happened for a while, so the machine powers down. This is what the Energy Saver timeout controls, and it is what power assertions are for. `caffeinate` takes an assertion — `PreventUserIdleSystemSleep` — that tells the kernel "not yet, something is happening".

**Clamshell sleep.** The lid closed. This is not a timeout being reached; it is a hardware event with its own handling, and on a laptop it is meant to be unconditional. A machine you put in a bag should sleep.

`caffeinate` speaks to the first one. It has nothing to say about the second. When you close the lid, the assertion is still perfectly valid and perfectly held — and the machine sleeps anyway, because it was never the thing being asked.

This is why the failure feels like a bug. The tool is working. The scope is just narrower than the name suggests.

## The exception that confuses everyone

Close the lid on a MacBook that has an external display and mains power attached and it stays awake. That is **clamshell mode**, and it is macOS deciding the lid is irrelevant because you are clearly using the machine at a desk.

People then conclude that closing the lid is fine and caffeinate is holding it. Unplug at a café and the same setup dies within seconds. The display and the power adapter were doing the work.

The conditions vary by model, but the general shape on Apple silicon is: external display **and** mains power. Battery alone, with no display, sleeps — [the ways around that are their own subject](../keep-macbook-awake-lid-closed/).

## What actually holds it

One supported mechanism:

```sh
sudo pmset -a disablesleep 1
```

This does hold a closed lid awake on battery with no display. It is also global, unscoped, and — the part that matters — **persistent**. It is written into the power management preferences and is not cleared when whatever set it exits. Kill the script before its cleanup line and the Mac cannot sleep again until someone works out why.

So anything using it responsibly needs three things: it should be set only while work is actually happening, it should be released the moment the work ends, and something independent should clear it if the thing that set it disappears.

## The routes that don't work, tested

It is worth recording the dead ends, because they look plausible and cost a day each.

**Private in-process APIs.** There is a private `RootDomainUserClient` selector 12, `setClamShellSleepDisable`, which reads like exactly the right thing. Verified on-device on **macOS 26.3**: it returns success and the Mac still sleeps. It governs the external-display clamshell path, not lid close with no display attached. This finding is [Adrafinil's](https://github.com/kageroumado), whose team tested it and published the result; goguma cites it in its own source rather than repeating the experiment.

**More caffeinate flags.** `-d` (display), `-i` (idle), `-m` (disk), `-u` (user active). All of these are narrower assertions, not broader ones. None reaches the clamshell path.

**IOKit assertions directly.** `IOPMAssertionCreateWithName` with `kIOPMAssertionTypePreventUserIdleSystemSleep` is what caffeinate itself uses. Calling it yourself gets you caffeinate's behaviour, including its limit.

**A dummy HDMI plug.** This genuinely works — it makes macOS believe a display is attached, enabling clamshell mode. It is a hardware fix for a software problem, needs mains power on most models, and does not travel well in a bag.

## The thing to be careful about

A closed MacBook that cannot sleep is a sealed aluminium box with no airflow, and you cannot see that anything is wrong. That is genuinely the dangerous configuration, and it is worth reading [is it safe to keep a MacBook awake in a bag](../macbook-awake-in-bag-safe/) before leaving one running.

The short version: whatever holds the lid awake should drop the hold if the machine gets hot or the battery falls, and it should only ever be held while work is actually happening.

## If the real problem is a scheduled job

If you came here because a job did not run overnight, holding sleep off is the wrong tool even once it works. You would be running a laptop all night to do a minute of work at 03:00. What you want is a machine that [wakes for the job and sleeps again afterwards](../wake-mac-for-scheduled-job-pmset/) — a different mechanism, and one none of the keep-awake tools offer.
