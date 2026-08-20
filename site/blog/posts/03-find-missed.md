---
slug: find-missed-scheduled-jobs-mac
order: 3
date: 2026-08-20
title: How to find which of your scheduled jobs your Mac has been missing
description: cron keeps no record of a job it skipped, so you have to reconstruct it — replay each schedule against the machine's own sleep history. Here is how, by hand.
answer: There is no log of a job that never ran, so you cannot look it up. You reconstruct it: read the sleep and wake intervals out of `pmset -g log`, replay each job's schedule against them, and count the fire times that landed inside a sleep. That count is the number nobody has and everybody wants.
faq:
  - q: Is there a log of cron jobs that did not run?
    a: No. cron only produces output for jobs that ran. A skipped occurrence generates no log line, no mail, and no exit code, because from cron's point of view nothing happened.
  - q: How do I see when my Mac was asleep?
    a: pmset -g log prints the sleep and wake history with timestamps and reasons. Filtering for Sleep and Wake entries gives you the intervals the machine was unavailable, usually going back several days.
  - q: How far back does the sleep log go?
    a: Typically a few days to a couple of weeks depending on how much else is logging. It is a rolling buffer, not permanent history, so an audit tells you about recent weeks rather than all time.
  - q: Which jobs should I check?
    a: Anything in crontab, and any scheduler that runs inside its own process. launchd StartCalendarInterval jobs re-fire on wake, so they are usually not worth auditing.
---

## Why you have to reconstruct it

A job that ran and failed leaves evidence — a non-zero exit code, output, a mail. A job that never started leaves nothing. cron did not error, because cron did not do anything. Your job's own logging never ran, because your job never ran.

So the question "which of my jobs have been missed?" cannot be answered by looking. It has to be computed, from two things you do have: what each job's schedule says, and when the machine was actually asleep.

## Step 1: get the sleep history

macOS records every sleep and wake:

```sh
pmset -g log | grep -E "Entering Sleep|Wake from"
```

You get lines with a timestamp and a reason — `Entering Sleep state due to 'Clamshell Sleep'`, `Wake from Standby due to EC.LidOpen`. Pair each sleep with the next wake and you have the intervals during which nothing could have run.

This is a rolling buffer, so it covers recent days to a couple of weeks rather than all history. That is usually enough: if a job is being missed, it is being missed repeatedly.

For a quick view of just the transitions:

```sh
pmset -g log | awk '/Entering Sleep|Wake from/ {print $1, $2, $4, $5, $6}'
```

## Step 2: list what is actually scheduled

Three places, and most people forget the third.

```sh
crontab -l                                    # your crontab
ls ~/Library/LaunchAgents /Library/LaunchAgents /Library/LaunchDaemons
```

The third is **application-level schedulers** — tools that keep their own job store and fire jobs from their own process. These appear in neither `crontab -l` nor `launchctl list`, because the OS is not involved at all. If one of those is where your real automation lives, an OS-only audit reports "nothing scheduled here" while every job you care about sits in a file it never looked at.

## Step 3: replay the schedule against the sleeps

For each job, expand its schedule into the fire times it should have had over the window, then check each one against the sleep intervals.

A cron expression like `0 3 * * *` over a fourteen-day window is fourteen fire times. If the machine was asleep across 03:00 on eleven of those days, that job has silently not run eleven times.

The arithmetic is simple; doing it for every job by hand is not, which is why almost nobody does it and why the misses accumulate unnoticed for months.

## What to do with the answer

The count sorts your jobs into three groups, and each wants something different.

**Missed often, and it matters.** These need a [scheduled wake](../wake-mac-for-scheduled-job-pmset/). This is the group the whole exercise exists to find.

**Missed often, and it does not matter.** A cache cleanup that runs at 04:00 and would be just as useful at 09:00. Move it to a launchd `StartCalendarInterval`, which [re-fires on wake by itself](../launchd-vs-cron-sleep/), and stop thinking about it.

**Never missed.** It fires during hours the machine is reliably awake. Leave it alone. Waking a Mac for a job that was never in trouble is a cost with no benefit.

That last group matters more than it looks. The temptation after an audit is to put a wake in front of everything. Most jobs do not need one, and every unnecessary wake is battery spent for nothing.

## One subtlety about "asleep"

A Mac is not simply awake or asleep. Overnight it cycles through brief dark wakes — powering up for a few seconds to fetch mail or run maintenance, then going back down. A fire time that lands inside one of those windows *might* have run.

Treat dark wake as sleep for auditing purposes. It is short, it is not scheduled around your job, and relying on your 03:00 job coinciding with a dark wake is not a strategy. If you are counting misses, counting a dark-wake coincidence as a hit will flatter the numbers and hide the problem.

## Doing it automatically

This is the first thing goguma does when you install it. It reads your crontab, your user and system launchd jobs, and application-level schedulers it knows about, replays each schedule against the machine's own sleep history, and reports which have been missing runs and how often — before you configure anything.

It also skips the launchd calendar jobs, because those already handle themselves, and it records what each job costs in battery so you can see which are worth waking for.
