---
slug: launchd-vs-cron-sleep
order: 4
date: 2026-08-20
title: launchd vs cron on macOS — which one survives sleep
description: launchd re-fires a missed calendar job on wake; cron skips it silently. Which one you are using decides whether a sleeping Mac is a problem or a non-event.
answer: **launchd re-fires a missed `StartCalendarInterval` job once when the Mac wakes. cron does not — it skips the occurrence entirely and records nothing.** So the same schedule, expressed two ways, has completely different behaviour on a laptop. If running late is acceptable, launchd already solves this. If the job has to run *at* its time, neither does.
faq:
  - q: Does launchd run missed jobs after the Mac wakes?
    a: Yes, for StartCalendarInterval jobs. If the fire time passed while the machine was asleep, launchd runs the job once on wake. It does not run it once per missed occurrence, and it does not apply if the Mac was powered off.
  - q: Does cron run missed jobs after wake?
    a: No. cron matches the current minute against its schedule. Minutes that pass while asleep are never evaluated, so the occurrence is skipped with no error and no log entry.
  - q: Should I move my cron jobs to launchd?
    a: If running hours late is acceptable — an updater, a sync, an index rebuild — yes, it removes the problem entirely. If the job has to run at a specific time, launchd's catch-up does not help and you need a scheduled wake instead.
  - q: What about StartInterval instead of StartCalendarInterval?
    a: StartInterval counts elapsed time rather than wall clock, and sleep does not count toward it. A job set to every 3600 seconds does not fire repeatedly to catch up after an eight-hour sleep; it fires once and the interval restarts.
  - q: Does this apply to Homebrew services?
    a: Yes. `brew services` writes LaunchAgents, so a Homebrew-managed service using StartCalendarInterval gets launchd's catch-up behaviour.
---

## The difference in one table

| | cron | launchd `StartCalendarInterval` | launchd `StartInterval` |
|---|---|---|---|
| Missed while asleep | **skipped silently** | **runs once on wake** | interval restarts |
| Missed while powered off | skipped | skipped | interval restarts |
| Runs once per missed occurrence | — | no, once total | no |
| Records that it was missed | no | no | no |
| Runs at the exact scheduled time | only if awake | only if awake | approximately |

Both of those rows are stated outright in `launchd.plist(5)` — the man page on your own machine, which is worth reading before trusting any blog post about this, including this one:

> Unlike cron which skips job invocations when the computer is asleep, launchd will start the job the next time the computer wakes up. If multiple intervals transpire before the computer is woken, those events will be coalesced into one event upon wake from sleep.

That second sentence is the source for "once, not once per occurrence" below.

## Why cron behaves this way

cron is a loop over wall-clock minutes. Each minute it wakes, compares now against every crontab line, runs the matches, and sleeps again. A minute that does not happen — because the machine was asleep — is never compared against anything.

There is no queue, so there is nothing to drain on wake. There is no record, because a job that never started produced no output, no exit code, and no mail. From cron's point of view nothing went wrong, which is precisely why the failure is invisible for weeks.

## Why launchd behaves differently

launchd holds a calendar entry as a description of *when a thing should have happened*, not as a minute to match. On wake it can see that a fire time has passed and act on it.

Two limits worth stating precisely, because both catch people:

**Once, not once per occurrence.** A job scheduled `every hour` that slept through nine fire times runs **one** time on wake, not nine. If your job is idempotent this is exactly what you want. If each run processes a queue or produces a dated artefact, you have eight missing artefacts and one confusing catch-up run.

**Sleep only, not shutdown.** Powered off, launchd is not running and has nothing to reconcile.

## `StartInterval` is a third behaviour

`StartInterval` — every N seconds — is neither of the above. It counts elapsed time, and sleep does not count. Set a job to run every 3600 seconds, sleep for eight hours, and on wake you do not get eight catch-up runs; you get one run and the timer restarts.

This is usually the desired behaviour for a poller, and it is a genuine reason to prefer `StartInterval` over `StartCalendarInterval` when the exact time does not matter.

## Which to use

**Use launchd `StartCalendarInterval` when late is fine.** Updaters, syncs, backups that only need to happen daily, index rebuilds, cleanup. The catch-up is free and correct, and you should not put a scheduled wake in front of it — waking a laptop at 03:00 for a job that would have run perfectly well at 08:15 is a pure waste of battery.

**Use cron when you like cron.** The syntax is more compact, it is portable, and for a machine that is awake it is fine. Just know that on a laptop each expression is also a silent-failure surface.

**Neither solves "must run at 03:00".** A report someone expects at 08:00, a backup that has to finish before you leave, anything with a downstream consumer — launchd's catch-up gives you the job at 08:15 when you open the lid, which for these is the same as not running. That needs a [scheduled wake](../wake-mac-for-scheduled-job-pmset/).

## The practical consequence for tooling

Any tool that wakes a Mac for scheduled jobs has to know this distinction, or it does real harm. Waking at 03:00 for a `StartCalendarInterval` job that would have self-healed on wake costs battery to achieve nothing.

goguma marks those jobs **self-healing** and leaves them alone: it adopts them so you can see them, reports them as needing no wake, and arms nothing. The jobs it does wake for are the ones that genuinely lose a run — crontab entries, and schedulers that run inside their own process rather than through the OS.

If you are auditing this by hand, the rule is: for each job, ask whether a run eight hours late is a run or a miss. That answer, not the schedule syntax, decides whether sleep is a problem.

## Finding out what you actually have

Most machines have both, plus a few things that are neither. `crontab -l` shows one. `launchctl list` shows another. Application-level schedulers — anything that keeps its own job store and runs it from its own process — show up in neither, which is [how jobs go missing without anyone noticing](../find-missed-scheduled-jobs-mac/).
