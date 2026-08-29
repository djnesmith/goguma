---
slug: pmset-repeat-wake-schedule
order: 14
date: 2026-08-28
title: "pmset repeat: put your Mac's sleep and wake on a daily schedule"
description: macOS still has scheduled sleep and wake — the Energy Saver UI for it is gone, but pmset repeat does everything the panel did and more. Syntax, weekday codes, verification, and the gotchas.
answer: '`sudo pmset repeat wakeorpoweron MTWRF 07:45:00` wakes (or powers on) the Mac at 7:45 every weekday; `sudo pmset repeat cancel` clears it. Weekdays are a subset of MTWRFSU. Only one repeating rule of each kind exists at a time — a new one replaces the old — and `pmset -g sched` shows what is currently armed. The scheduled-sleep half is refused if anything is actively holding sleep off at that moment.'
faq:
  - q: How do I schedule my Mac to wake at the same time every day?
    a: 'sudo pmset repeat wakeorpoweron MTWRFSU 07:45:00 — the weekday string is any subset of MTWRFSU, so MTWRF is weekdays only. Verify with pmset -g sched.'
  - q: Where did the Energy Saver schedule UI go?
    a: Apple removed the Schedule panel from System Settings; the underlying mechanism remains and pmset repeat drives it directly.
  - q: What is the difference between pmset repeat and pmset schedule?
    a: repeat sets one standing rule that fires every matching day. schedule arms a one-shot event at an absolute date and time, which is consumed when it fires. Repeating rules are for routines; one-shots are for waking before a particular job.
  - q: Can I schedule different wake times for different days?
    a: Not with repeat alone — one repeating wake rule exists at a time, and setting a new one replaces it. Varying times per day means arming one-shot wakes with pmset schedule, and re-arming after each fire.
---

## The schedule outlived its settings panel

Old Mac OS had a Schedule button in Energy Saver: start up or wake at 7:45 on weekdays, sleep at midnight. The panel is gone from System Settings; the mechanism underneath it is not. `pmset repeat` is that mechanism, driven directly.

```sh
sudo pmset repeat wakeorpoweron MTWRF 07:45:00
```

That is: every weekday at 07:45, wake the machine if it is asleep — or boot it if it is powered off, which is what distinguishes `wakeorpoweron` from plain `wake`.

The weekday string is documented in `pmset(1)` as any subset of `MTWRFSU` — Monday through Sunday, one letter each, R for Thursday and U for Sunday. `M` alone is valid; so is `MTWRF`; so is the full week.

Both halves of the old panel work, and they can be set in one line — this example is from the man page itself:

```sh
sudo pmset repeat wakeorpoweron T 12:00:00 sleep MTWRFSU 20:00:00
```

Cancelling is one command, and it clears every repeating rule:

```sh
sudo pmset repeat cancel
```

## Verify what is actually armed

Never assume; the machine will tell you:

```sh
$ pmset -g sched
Scheduled power events:
 [0]  wake at 08/28/2026 21:22:55 by 'goguma'
```

`pmset -g sched` shows both kinds: repeating rules appear under their own heading when one is set, and every [one-shot scheduled event](../wake-mac-for-scheduled-job-pmset/) is listed with an owner naming who armed it. If a wake fires that you do not recognise, [the log names the reason](../what-woke-my-mac/) and this list names the culprit.

## The gotchas

**One rule of each kind.** There is a single repeating wake slot and a single repeating sleep slot. Setting a new rule replaces the old one silently. If you need Tuesday at 06:00 and Friday at 09:00, repeat cannot express it — that is one-shot territory.

**Scheduled sleep is polite.** The repeating sleep half will not force a machine down past active work — assertions held against sleep win, which is the same mechanism [caffeinate](../keep-mac-awake-terminal-command/) uses in the other direction. Treat scheduled sleep as "start trying to sleep at 20:00", not as a guarantee.

**Wake is a moment, not a state.** The 07:45 wake brings the machine up; the ordinary sleep timers then apply. If nothing touches it, it drifts back to sleep on the usual idle schedule. A wake scheduled so a job can run needs the job to *hold* the machine awake for its duration, or the machine may not still be up when the job's real work starts.

**Repeat expresses routines, not job schedules.** A daily 07:45 wake is a routine. A crontab with jobs at 02:00, 03:30, and hourly-on-Sundays is not — expressing it as repeat rules is impossible, and arming one-shots for each fire time, forever, [is a job for software](../cron-jobs-dont-run-mac-asleep/) rather than for a person. That is the half goguma automates: it reads the schedules you already have and keeps the next one-shot wake armed, so the standing-rule limitations stop mattering.
