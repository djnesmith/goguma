---
slug: pmset-sleep-settings-explained
order: 17
date: 2026-08-28
title: Every pmset sleep setting, explained
description: "sleep, displaysleep, standby, hibernatemode 0, 3 and 25, womp, ttyskeepawake, powernap: what each setting in pmset -g actually controls, with the defaults and the traps."
answer: "Run `pmset -g` to see your Mac's power settings. The ones that matter: `sleep` is the idle timer for the whole system, `displaysleep` for the screen alone. `hibernatemode` picks the sleep style — 0 is memory-only (desktop default), 3 keeps memory powered plus a disk image (laptop default), 25 powers memory off and restores from disk. `standby` moves a long-sleeping laptop into that deeper state, `womp` is Wake for network access, `ttyskeepawake` keeps the Mac up while an SSH session is active, and settings can differ per power source — `-b` battery, `-c` charger, `-a` both."
faq:
  - q: What is hibernatemode 3 vs 25?
    a: 'Mode 3, the laptop default, keeps memory powered during sleep and also writes a disk image as insurance: wakes are instant, and power loss costs nothing. Mode 25 writes the image and powers memory off — slower to sleep and wake, better battery, and the man page names it the setting to use if what you want is hibernation.'
  - q: Why do my pmset settings differ on battery and charger?
    a: 'Settings exist per power source. pmset -b sets battery values, -c charger values, -a both — and defaults differ, which is why a Mac behaves differently plugged in. pmset -g shows the set currently in effect.'
  - q: What does womp mean in pmset?
    a: Wake on magic packet — the same thing as Wake for network access in System Settings. With it on, a sleeping Mac can be woken over the network, and it wakes briefly on its own to keep that reachability alive.
  - q: What does ttyskeepawake do?
    a: While any terminal session (such as an SSH login) is active, the system will not idle-sleep. A tty counts as inactive only once its idle time exceeds the system sleep timer.
---

## Reading your own machine

Everything below is visible in one command:

```sh
pmset -g
```

That prints the settings currently in effect — plus, at the top, anything currently overriding them, because assertions from running processes outrank stored settings. A `SleepDisabled 1` line, for instance, means [something has switched sleep off entirely](../pmset-disablesleep/).

One structural fact makes sense of the rest: **most settings exist twice.** There is a battery set and a charger set, and they usually differ — which is the answer to a whole family of "why does it only do this unplugged?" mysteries. `pmset -b` writes the battery values, `-c` the charger values, `-a` both at once.

## The timers

| Setting | Controls |
|---|---|
| `sleep` | Minutes of idleness before the whole system sleeps. 0 disables idle sleep. |
| `displaysleep` | The screen alone. Almost always shorter than `sleep`. |
| `disksleep` | Disk spindown. Vestigial on SSDs. |

These are *idle* timers: they answer "how long after the human stops". They are overridden by assertions — that is [how caffeinate works](../keep-mac-awake-terminal-command/) — and they are irrelevant to the lid, which sleeps the machine [regardless of any timer](../keep-macbook-awake-lid-closed/).

## The sleep styles: hibernatemode

`hibernatemode` decides what "asleep" physically means. The man page documents three values, and warns you to use caution with all of them:

- **0** — sleep is memory-only. Nothing written to disk; wake is instant; power loss loses the session. The desktop default.
- **3** — memory stays powered *and* a copy is written to disk. Wake is instant from memory, and if power fails, the machine restores from the image instead of losing everything. The laptop default.
- **25** — the image is written and memory is powered off. Slower to sleep, slower to wake, and the best battery of the three. In the man page's own words: if you want hibernation, this is the setting.

Adjacent to it, `standby`: with it on, a laptop that has been asleep for a while writes the image and drops into the deeper state anyway — mode 3 sleep that matures into mode 25. The delay is governed by `standbydelayhigh` and `standbydelaylow`, split by whether the battery is above or below `highstandbythreshold` (default 50%). This is why [a wake in the log can say "Wake from Hibernate"](../what-woke-my-mac/) on a machine nominally in mode 3 — it slept long enough to graduate. To never write images at all, the man page's recipe is `hibernatemode`, `standby`, and `autopoweroff` all 0.

## The network and terminal settings

- **`womp`** — wake on magic packet; the "Wake for network access" checkbox. A sleeping Mac stays reachable, at the cost of brief [dark wakes](../dark-wake-power-nap/) to maintain its network presence.
- **`powernap`** — permits background work (mail, backups) during those display-off wakes on supported machines.
- **`ttyskeepawake`** — while a terminal session is active, no idle sleep. This is why a Mac someone is SSH'd into stays up — and the fine print is that a tty only counts as inactive once its idle time exceeds the sleep timer. [More on SSH and sleep](../ssh-sessions-mac-sleep/).

## What none of these do

Settings shape when the machine sleeps and how deeply. No setting here wakes it at a time of your choosing — that is a different mechanism, [`pmset schedule`](../wake-mac-for-scheduled-job-pmset/) for one-shots and [`pmset repeat`](../pmset-repeat-wake-schedule/) for routines. And no timer value rescues [a cron job whose moment passed during sleep](../cron-jobs-dont-run-mac-asleep/); by the time the machine is back, the minute is gone.
