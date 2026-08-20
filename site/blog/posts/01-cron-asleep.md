---
slug: cron-jobs-dont-run-mac-asleep
order: 1
date: 2026-08-20
title: Why your Mac's cron jobs don't run while it's asleep
description: cron skips a job whose time passed while the Mac was asleep — no error, no retry, no record. launchd behaves differently. Here is exactly what happens, and what to do about it.
answer: A sleeping Mac does not queue the cron jobs it missed and catch up on wake. It skips them outright, writes no error, and retries nothing — so the failure is silent, and most people find out weeks later. launchd is the exception: it re-fires a missed `StartCalendarInterval` job when the machine wakes.
faq:
  - q: Does cron catch up on jobs it missed while the Mac was asleep?
    a: No. cron evaluates its schedule against the current minute. Minutes that pass while the machine is asleep are never evaluated, so the job is skipped rather than queued, and nothing is written to any log.
  - q: Does launchd catch up on missed jobs?
    a: Yes, for calendar-based jobs. If a StartCalendarInterval fire time passes while the Mac is asleep, launchd runs the job once when the machine next wakes. It does not run it once per missed occurrence, and it does not help if the Mac was powered off.
  - q: Will scheduling a job during the day fix it?
    a: Only if the Mac is genuinely awake and not in a low power state at that moment. A laptop with the lid closed is asleep regardless of the hour, so a lunchtime job on a machine in a bag is missed exactly like a 3am one.
  - q: How do I know whether a job has been missed?
    a: With cron alone you cannot, because nothing is recorded. You have to compare each job's schedule against the machine's sleep and wake history to work out which fire times fell inside a sleep interval.
---

## What actually happens

cron is a loop. It wakes roughly once a minute, compares the current time against every line in every crontab, and runs whatever matches. That design has one consequence that matters enormously on a laptop and not at all on a server: **a minute that does not happen is a minute that is never evaluated.**

When your Mac sleeps at 23:40 and wakes at 08:15, the minutes in between do not exist as far as cron is concerned. It does not later notice that `0 3 * * *` should have fired. There is no backlog, because there is no record that anything was due.

The important part is what does *not* happen next:

- Nothing fails, so nothing appears in a log
- Nothing retries, because nothing knows it was missed
- No mail is sent, because cron only mails you the output of jobs that actually ran
- Your job's own error handling never runs, because your job never started

This is why the failure mode is so hard to catch. A crashed job leaves a stack trace. A job that never started leaves nothing at all. The symptom is not an error — it is the gradual realisation, weeks later, that a digest has been arriving on some days and not others.

## launchd is genuinely different, and this is the part people get wrong

macOS prefers launchd, and launchd handles this case. From Apple's own documentation:

> Unlike cron which skips job invocations when the computer is asleep, launchd will start the job the next time the computer wakes up.

So a `StartCalendarInterval` job whose fire time passed during sleep runs **once**, on wake. Two caveats that matter:

- **Once, not once per occurrence.** A job scheduled hourly that slept through nine fire times runs a single time on wake, not nine times.
- **Only sleep, not shutdown.** If the Mac was powered off, launchd has nothing to catch up from.

This distinction is the single most useful thing to know here, and it cuts both ways. If your job is a `StartCalendarInterval` LaunchAgent and running late is fine — an updater, a sync, an index rebuild — you already have the behaviour you want and should change nothing.

If your job is in crontab, or if running eight hours late is the same as not running (a report that has to be in someone's inbox by 08:00, a backup that must finish before you leave the house), then the sleep is a real problem.

## Why the usual advice doesn't fix it

**"Just use launchd."** Good advice when late is acceptable. It does not help when the point is that the job runs *at* 03:00, and it means rewriting schedules that already work.

**"Just schedule it during the day."** This assumes your Mac is awake during the day. A closed laptop is asleep at 14:00 exactly as much as at 03:00. The lid, not the hour, is what decides.

**"Just use caffeinate."** caffeinate stops a Mac falling asleep. It cannot wake one that already is. To use it for a 03:00 job you have to prevent sleep from the moment you stop working until the job finishes — that is a full night of a machine running at desk power to do ninety seconds of work. It also [does not survive the lid closing](../caffeinate-lid-closed/).

**"Just leave it plugged in."** Mains power changes when a Mac sleeps, not whether. It still sleeps.

## What actually works

There is only one mechanism on macOS that makes a sleeping machine wake at a chosen time, and it is `pmset schedule`. The full method — computing the wake time, arming it, holding sleep off long enough for the job to finish, and re-arming for next time — is in [how to wake a Mac for a scheduled job](../wake-mac-for-scheduled-job-pmset/).

The short version:

```sh
sudo pmset schedule wake "08/21/2026 02:58:30"
```

That wakes the machine at 02:58:30 so a 03:00 job has a machine to run on. You then need it to stay awake long enough for the job to finish, and you need to arm the next one after it fires, because a scheduled wake is consumed when it happens.

## Working out what you have already missed

You cannot ask cron, because cron kept no record. What you can do is reconstruct it: take each job's schedule, replay it against the machine's sleep and wake history, and count the fire times that fell inside a sleep interval.

macOS keeps that history. `pmset -g log` includes every sleep and wake with a timestamp and a reason, going back days. Replaying a cron expression against those intervals gives you the number nobody has: how many times each job has silently not run.

That is what [finding your silently missed jobs](../find-missed-scheduled-jobs-mac/) covers.
