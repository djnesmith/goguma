---
slug: pmset-disablesleep
order: 16
date: 2026-08-28
title: "pmset disablesleep: the sleep switch with no owner"
description: One command overrides every kind of sleep on a Mac, including the lid. It is also undocumented, global, and stays set when the thing that set it dies. How to use it without stranding it.
answer: '`sudo pmset -a disablesleep 1` stops a Mac sleeping entirely — idle timers, the lid, everything — until `sudo pmset -a disablesleep 0` sets it back. It does not appear in the settings list of `man pmset`, and unlike an assertion it is not tied to any process: kill whatever set it and the switch stays set. Anything that automates it needs a dead-man switch that clears the flag if the owner disappears.'
faq:
  - q: What does pmset disablesleep do?
    a: It sets a system-wide flag that prevents every kind of sleep, including lid-close sleep, until it is explicitly set back to 0. While set, pmset -g shows SleepDisabled 1.
  - q: Is disablesleep the same as caffeinate?
    a: No, and the difference is the whole point. caffeinate holds an assertion owned by a process — the process dies, the hold dies. disablesleep is a stored setting owned by nobody; the process that set it dying changes nothing.
  - q: How do I check whether disablesleep is set?
    a: 'Run pmset -g and look for the SleepDisabled line: 1 means sleep is disabled.'
  - q: Why would anyone use it instead of caffeinate?
    a: Because it is the only mechanism that keeps a lid-closed MacBook awake without an external display. caffeinate holds off idle sleep; the lid is not idle sleep.
---

## The strongest switch, with the sharpest edge

```sh
sudo pmset -a disablesleep 1
```

With that set, the machine does not sleep. Not on the idle timers, not on battery, and — the reason anyone reaches for it — [not when the lid closes](../keep-macbook-awake-lid-closed/). Nothing else on macOS overrides lid sleep without an external display attached. This does.

```sh
sudo pmset -a disablesleep 0
```

sets it back. Between those two commands, the machine's sleep is simply off, and `pmset -g` says so:

```sh
$ pmset -g | grep SleepDisabled
 SleepDisabled		1
```

It is worth noticing what is *not* true of it: `disablesleep` does not appear in the settings list of `man pmset`. It works, it has worked for years, and Apple documents it nowhere on the page that documents everything else — which is a fair summary of its status. Use it knowingly.

## Owned by nobody

The important contrast is with [caffeinate and its assertions](../keep-mac-awake-terminal-command/). An assertion is owned: a process holds it, and when that process exits — cleanly, by crash, by kill — the assertion is released and the machine can sleep. The system even lists the owner in `pmset -g assertions`.

`disablesleep` is not an assertion. It is a stored flag. The script that set it can crash, be killed, or simply forget, and the flag stays set. Nothing notices. Nothing times out. The machine just never sleeps again until someone works out why and sets it back — and the symptom, a Mac that quietly stops sleeping, does not point at its cause.

On a desktop that is an electricity bill. On a laptop [closed in a bag](../macbook-awake-in-bag-safe/), it is a flat battery or a hot machine, with the screen — the only thing that could have warned you — dark.

## The discipline it demands

If a human sets it by hand for an afternoon, the discipline is a reminder to set it back. If software sets it, the bar is higher, and this is exactly the design problem [every tool built on this mechanism](../caffeinate-vs-pmset-vs-amphetamine/) has to answer:

- **Pair every set with a guaranteed unset.** Not "the script clears it at the end" — the script crashing *is* the case that matters. The unset has to survive the owner's death.
- **A dead-man switch, not good intentions.** Something independent of the setter should clear the flag when the setter stops confirming it is alive. goguma's answer: the privileged helper that holds the flag clears it after 60 seconds without contact from the daemon, so a crashed daemon strands nothing.
- **Cutouts, because the screen is closed.** Whatever holds a closed machine awake should release on temperature and low battery, since with the lid shut those are exactly the conditions nobody can see developing.

## When it is the right tool

For all its sharpness, there are exactly two situations where it is the honest answer: a lid-closed machine with no display that must keep working, and testing what your own software does when sleep genuinely cannot happen. For everything else — a script, a build, a download — [a scoped assertion](../keep-mac-awake-terminal-command/) does the job and cleans up after itself.

And like everything on this branch of the problem, it keeps an awake machine awake. A machine that is already asleep at 03:00 needs [a scheduled wake](../wake-mac-for-scheduled-job-pmset/), which no amount of sleep-disabling can retroactively provide.
