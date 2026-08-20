---
slug: wake-mac-for-scheduled-job-pmset
order: 2
date: 2026-08-20
title: How to wake a Mac for a scheduled job with pmset
description: The complete method — computing the wake time, arming it, holding sleep off until the job finishes, and re-arming for next time — plus the four things that go wrong.
answer: `sudo pmset schedule wake "MM/dd/yyyy HH:mm:ss"` wakes a sleeping Mac at a chosen time. It is the only mechanism macOS offers for this. The parts nobody mentions are that a scheduled wake is **consumed** when it fires, that the machine will go straight back to sleep unless something holds it, and that `pmset schedule` replaces entries rather than appending them.
faq:
  - q: What is the command to wake a Mac at a specific time?
    a: sudo pmset schedule wake "08/21/2026 02:58:30" — the date format is MM/dd/yyyy and the time is 24-hour with seconds. Use `pmset -g sched` to see what is currently armed.
  - q: Does the Mac stay awake after a scheduled wake?
    a: No. It wakes, finds no reason to stay up, and returns to sleep after the normal idle timeout — often before a job has finished. You need caffeinate or a sleep block to hold it for the duration of the work.
  - q: Do I need to re-arm the wake after it fires?
    a: Yes. A one-shot scheduled wake is consumed when it happens. For a recurring job you must arm the next occurrence each time, which is the part that makes the manual approach impractical to maintain.
  - q: Does pmset schedule work with the lid closed?
    a: The wake itself does. Staying awake afterwards is the harder half — a closed lid on battery with no external display takes a different sleep path that caffeinate cannot hold off.
  - q: Does this work if the Mac is powered off?
    a: `pmset schedule poweron` can power a Mac on from off on machines that support it, but it is a much blunter instrument and behaves inconsistently across models. Wake-from-sleep is the reliable case.
---

## The command

```sh
sudo pmset schedule wake "08/21/2026 02:58:30"
```

The format is `MM/dd/yyyy HH:mm:ss`, 24-hour, seconds required. To see what is armed:

```sh
pmset -g sched
```

And to remove it:

```sh
sudo pmset schedule cancel wake "08/21/2026 02:58:30"
```

Note that cancelling requires the *exact* time you scheduled. This is the first thing that trips people up: a stale entry you can no longer remember the timestamp of has to be cleared with `pmset schedule cancelall`, which takes out everything, including any wake macOS itself arranged.

## Why waking is only half of it

A Mac that wakes with nothing to do goes back to sleep. The scheduled wake gets you a machine that is powered up at 02:58:30; it does not get you a machine that is still powered up at 03:00:45 when your backup is halfway through writing a tarball.

So the real recipe is two things, not one:

```sh
# arm the wake, 90 seconds before the job is due
sudo pmset schedule wake "08/21/2026 02:58:30"

# and in the job itself, hold sleep off while it runs
caffeinate -s /usr/local/bin/nightly-backup
```

`caffeinate -s` prevents system sleep for the lifetime of the command it wraps and releases when the command exits, which is the correct shape: the hold lasts exactly as long as the work.

With the lid open, on mains, that combination works. There are four ways it comes apart.

## The four things that go wrong

### 1. The wake is consumed

A one-shot scheduled wake fires once and is gone. Tomorrow's 03:00 job has no wake armed for it unless something armed one. For a recurring job this means the job itself has to schedule its own next wake as its last act — and if a run fails before reaching that line, the chain is broken silently and permanently.

`pmset repeat` exists for recurring wakes, but it holds exactly **one** repeating rule for the whole machine. Two jobs on different schedules cannot both use it.

### 2. `schedule` replaces rather than appends

Running `pmset schedule wake` twice with different times does not reliably give you two wakes. Entries are matched on type and time, and the scheduling database is shared with anything else on the system that arms wakes — Time Machine, Software Update, Power Nap. Arming and cancelling naively will eventually stamp on something you did not put there.

### 3. caffeinate does not survive the lid closing

This is the big one for laptops. `caffeinate` asserts against *idle* sleep. Closing the lid triggers clamshell sleep, a lower-level path that the assertion does not touch. On a closed MacBook with no external display and no mains power, the machine sleeps and takes your job with it. Covered properly in [why caffeinate doesn't work with the lid closed](../caffeinate-lid-closed/).

The mechanism that does hold a closed lid awake is `sudo pmset -a disablesleep 1` — and that one has [its own serious problem](../caffeinate-vs-pmset-vs-amphetamine/): it is a global setting that persists in the power management preferences and is **not** cleared when the process that set it dies. Anything that sets it and is then killed leaves a Mac that cannot sleep at all until someone notices.

### 4. Waking a flat battery is worse than not waking

This is the one almost nobody accounts for, and it is worth thinking through carefully.

Suppose your Mac is on battery at 11% and a job is due at 03:00. You wake it. The wake itself costs charge. The job runs, costs more. If you have a safety cutoff that releases the hold at 10%, it fires almost immediately — so you have spent energy on a wake, not completed the job, and arrived closer to a hard shutdown than if you had done nothing at all.

**A safety cutoff cannot un-wake a machine.** By the time it fires, the energy is already spent. Which means the check has to happen *before* the wake is armed, not after it fires — and the margin should be the job's own measured cost, not a flat number. Refusing to wake at 24% for a job that has historically used 0.5% of the battery protects nobody from anything.

## What this looks like automated

Doing all of the above by hand, per job, and keeping it correct as schedules change, is more work than most jobs are worth. Automated, the pieces are:

- Read the jobs already on the machine — crontab, launchd, whatever else — rather than asking you to redeclare them
- Compute the next fire time and arm a wake **90 seconds** before it, which is enough for the machine to be up and settled
- Hold sleep off from the wake until the job exits, using a mechanism that survives a closed lid
- Learn how long each job actually takes, so the hold fits the work instead of a guess
- Skip launchd's `StartCalendarInterval` jobs, which [re-fire on wake by themselves](../launchd-vs-cron-sleep/) and need no help
- Re-arm after every fire, so the chain never depends on a job succeeding
- Refuse the wake when the battery cannot afford it, using that job's own measured drain as the margin
- Release everything if a closed-lid machine gets hot or the charge falls

That list is what [goguma](../../) is. Each item is small; the reason to use a tool is that all eight have to be right at once, forever, and a mistake in any of them is silent.
