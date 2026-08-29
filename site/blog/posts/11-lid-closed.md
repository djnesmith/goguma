---
slug: keep-macbook-awake-lid-closed
order: 11
date: 2026-08-28
title: How to keep a MacBook awake with the lid closed
description: Closing the lid sleeps a MacBook regardless of every timer and every keep-awake app. Here is the one supported way, the one mechanism that works without a display, and what each costs.
answer: Closing the lid puts a MacBook to sleep regardless of its energy settings. Apple's supported way to keep it running is clamshell mode, which requires power and an external display. Without a display, the only mechanism that holds a closed MacBook awake is `sudo pmset -a disablesleep 1` — a global switch that stays on until something sets it back, so it should always be paired with a way to turn it off.
faq:
  - q: Can I keep a MacBook awake with the lid closed without an external display?
    a: Yes, but only with `sudo pmset -a disablesleep 1`. Sleep timers, caffeinate, and menu bar keep-awake apps all stop working the moment the lid closes. disablesleep is the one switch that overrides lid sleep itself.
  - q: Does caffeinate work with the lid closed?
    a: No. caffeinate holds assertions that prevent idle sleep, and closing the lid is not idle sleep. The Mac sleeps anyway and the running caffeinate goes to sleep with it.
  - q: Is it safe to leave a MacBook running in a bag?
    a: It is the one genuinely risky case. A closed laptop sheds heat through a small area, a bag insulates it, and nothing on the screen can warn you. Anything that holds a closed lid awake should also watch temperature and battery, and let go when either goes wrong.
  - q: What is clamshell mode?
    a: Apple's supported closed-lid state. Connect power and an external display, and the MacBook keeps running with the lid shut, driving the external screen. It exists for desks, not for bags — unplug the display and the machine sleeps.
---

## The lid outranks everything

Every sleep timer in System Settings answers one question: how long to wait after you stop using the machine. The lid is not a timer. Closing it is an instruction, and macOS obeys it regardless of what the timers say, what caffeinate is holding, and what any menu bar app has toggled. That is why [caffeinate does not survive the lid closing](../caffeinate-lid-closed/) — not because caffeinate is badly built, but because it holds off *idle* sleep, and lid sleep is not idle sleep.

So "keep my MacBook awake with the lid closed" has exactly two honest answers, and they serve different situations.

## The supported way: clamshell mode

Connect the MacBook to power and an external display, and it keeps running with the lid closed, driving the external screen. This is Apple's intended closed-lid state and it needs no commands and no tools.

Its limits are the plug and the display. This is a desk arrangement. It answers "I want the laptop closed under the monitor," not "I want the job to finish while the laptop is in my bag."

## The way that works without a display

One mechanism overrides lid sleep itself:

```sh
sudo pmset -a disablesleep 1
```

With that set, the machine does not sleep — lid open, lid closed, on battery, at all. Set it back with:

```sh
sudo pmset -a disablesleep 0
```

Two things to understand before using it, both covered at length in [what disablesleep actually does](../pmset-disablesleep/):

- **It is a global switch, not a lease.** It stays on until something sets it to 0. If the script that set it crashes, the switch stays set, and the machine will run its battery flat in the bag with nothing on screen to tell you.
- **It is system-wide.** There is no "until this job finishes" scoping. You build that yourself, or you use a tool that does.

## The part that is actually about safety

A closed MacBook that stays awake is doing something no MacBook was shaped for: working hard with its heat vents against fabric and its screen — the only way it can tell you anything — dark. The failure modes are not hypothetical: heat that would be a fan noise on a desk is a shutdown in a backpack, and a battery that would show you a warning at 10% just dies.

Whatever holds the lid open awake should therefore also do three things:

- **Expire on its own.** A hold with no timeout is a flat battery waiting to happen.
- **Watch temperature.** With the lid closed there is no fan-noise feedback and no screen. The hold should release when the machine gets hot, because sleep is the correct response to heat you cannot see.
- **Watch the battery.** Same reasoning. Below a cutoff, let the machine sleep rather than run to zero.

This is the checklist [is it safe to keep a MacBook awake in a bag](../macbook-awake-in-bag-safe/) develops in detail. goguma exists because doing all of this by hand, correctly, every time, is the actual work — its holds are leases that expire, and every hold is dropped if a closed machine gets hot or the battery falls below a cutout.

## Which one you want

| Situation | Answer |
|---|---|
| Laptop closed under an external monitor | Clamshell mode — plug in the display and power |
| A job must finish while the lid is closed, no display | `disablesleep`, with a timeout and cutouts |
| An [AI coding agent is working](../keep-coding-agent-running-lid-closed/) with the lid shut | Same mechanism, scoped to the agent's activity |
| The machine is asleep and a 3am job is coming | Neither — that needs [a scheduled wake](../wake-mac-for-scheduled-job-pmset/) |

The last row matters: everything on this page keeps an awake machine awake. None of it wakes a machine that has already gone to sleep. Those are different mechanisms, and mixing them up is the most common way this goes wrong.
