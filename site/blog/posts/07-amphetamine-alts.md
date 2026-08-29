---
slug: amphetamine-alternatives-mac
order: 7
date: 2026-08-20
title: Amphetamine alternatives for macOS, compared honestly
description: Every keep-awake app for the Mac, what each actually does with the lid closed, and the question that separates them — what happens when you forget to turn it off.
answer: The free options worth knowing are **Caffeine**, **KeepingYouAwake**, **Clapet**, **Wide Awake**, **Clamshell** and **Adrafinil**. The two questions that actually separate them: does it hold a **closed lid** with no external display, and does it **release by itself**? Most hold an awake Mac awake and wait to be switched off. None of them, Amphetamine included, can wake a Mac that is already asleep.
faq:
  - q: What is the best free alternative to Amphetamine?
    a: KeepingYouAwake and Caffeine are the closest like-for-like free menu bar toggles. If you specifically need lid-closed with no external display, Clamshell and Adrafinil target that case; if you want the hold scoped to an agent session rather than left on, Adrafinil and goguma both do that.
  - q: Which keep-awake apps work with the lid closed?
    a: Only ones that use pmset disablesleep underneath. Tools built purely on power assertions, which is most of the classic menu bar toggles, cannot hold a closed lid with no external display, because the assertion does not cover the clamshell sleep path.
  - q: Is Amphetamine still maintained?
    a: Yes, and it remains the most feature-complete of the toggles, with app, drive and audio triggers plus session timers. Its shape is a manual switch, so the usual complaint is forgetting to turn it off.
  - q: Which one wakes a sleeping Mac for a scheduled job?
    a: None of the keep-awake apps do — that needs pmset schedule, a different mechanism. goguma is built around that case rather than around the toggle.
  - q: Do any of them have safety cutoffs?
    a: Wide Awake, Adrafinil and goguma each release the hold on thermal or battery conditions. Most of the simpler toggles do not, which matters mainly for lid-closed use where you cannot see anything is wrong.
---

## The two questions that matter

Feature lists in this category all look similar. Two things actually separate the tools:

**Does it hold a closed lid?** With no external display and no mains power. Tools built on power assertions — the classic menu bar toggles — cannot, because [an assertion does not cover the clamshell sleep path](../caffeinate-lid-closed/). Only tools that reach for [`pmset disablesleep`](../pmset-disablesleep/) manage it.

**Does it release by itself?** A toggle you have to remember to switch off has one predictable failure: a laptop that arrives somewhere flat and warm. Everything else is preference.

## The comparison

| | free | closed lid | releases by itself | safety cutoffs | wakes a sleeping Mac |
|---|---|---|---|---|---|
| **Amphetamine** | yes | yes | timers and triggers | no | no |
| **Caffeine** | yes | no | no | no | no |
| **KeepingYouAwake** | yes | no | optional timer | no | no |
| **Clapet** | yes | yes | on lid open | no | no |
| **Wide Awake** | yes | yes | no | **thermal, battery** | no |
| **Clamshell** | freemium | **yes, its whole point** | no | no | no |
| **Adrafinil** | yes, MIT | yes | **on agent activity** | thermal, battery | no |
| **goguma** | yes, MIT | yes | **on job or agent activity** | thermal, battery | **yes** |
| `caffeinate` | built in | no | yes, with the command | no | no |

## The classic toggles

**Amphetamine** is the most feature-complete and still the default recommendation for good reason: app-launch triggers, drive-based triggers, audio-activity triggers, session timers, per-session control. If you want "stay awake whenever Logic is open", nothing else matches it. Free, and the trigger system genuinely reduces how often you have to remember.

**Caffeine** is the original and the simplest: one toggle, nothing else. It is assertion-based, so it does not hold a closed lid.

**KeepingYouAwake** is Caffeine with a timer and an active project. Same assertion limit.

**Clapet** is narrower and cleverer — it targets the lid specifically, keeping the Mac awake when you close it and letting it sleep when you open it again. Worth knowing if lid behaviour is the only thing you want to change.

## The lid-closed specialists

**Clamshell** exists for one job: keeping an Apple silicon MacBook awake with the lid shut and **no external display**, removing the dummy-plug requirement. If that is your entire problem, it is the most direct answer on the list.

**Wide Awake** is notable for shipping a thermal and battery cutoff — it turns itself off before overheating. In a category where most tools will happily cook a laptop in a bag, that is a real distinction and it deserves more credit than it usually gets.

## The agent-aware ones

A newer group scopes the hold to *work happening* rather than a switch being on.

**Adrafinil** (MIT, open source) detects agent activity through hooks it installs into Claude Code, Codex and others, holds sleep off with `pmset disablesleep` while they work, and releases when they stop. It has thermal and battery cutoffs and a chime when you close the lid. Its team also published the on-device finding that the private `setClamShellSleepDisable` API does not actually hold a displayless closed lid on macOS 26.3 — a genuinely useful negative result that saved everyone else the experiment.

**goguma** (MIT, open source) overlaps on the agent half — hooks for Claude Code, Codex CLI, Cursor and Gemini CLI — but is built around a different problem: waking a Mac that is already asleep, for scheduled jobs it finds on your machine. Holds are leases that expire on their own rather than waiting to be released, and its privileged helper clears a stranded sleep block after 60 seconds without contact, because [`disablesleep` persists after the process that set it dies](../caffeinate-vs-pmset-vs-amphetamine/).

## The thing none of them do

Every tool above answers "stop my Mac sleeping". A large share of the people searching for them actually have the opposite problem: **the Mac was already asleep when something was due.**

For that, keeping it awake is the wrong shape — you would run a laptop at desk power all night to do ninety seconds of work at 03:00. What you want is a machine that wakes for the job and sleeps again afterwards, which needs [`pmset schedule`](../wake-mac-for-scheduled-job-pmset/) rather than any assertion or toggle.

That is the gap goguma is built for, and it is why it is on this list without really being in this category.

## Choosing

- **A toggle with good triggers** → Amphetamine, still.
- **Simplest possible toggle** → KeepingYouAwake.
- **Only care about lid behaviour** → Clapet, or Clamshell for the no-display case.
- **Want a safety cutoff** → Wide Awake, Adrafinil or goguma.
- **Keeping a coding agent alive with the lid shut** → Adrafinil or goguma; both scope the hold to the work.
- **A scheduled job that fires while the Mac is asleep** → none of the toggles. You need a wake.
- **Wrapping one command with the lid open** → `caffeinate -s`, already installed.
