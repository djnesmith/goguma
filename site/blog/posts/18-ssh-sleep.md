---
slug: ssh-sessions-mac-sleep
order: 18
date: 2026-08-28
title: Why SSH sessions drop when your Mac sleeps, and what actually survives
description: Sleep suspends the network, so every SSH connection dies with it — in both directions. What tmux and mosh actually solve, what ttyskeepawake does, and how to keep either end alive on purpose.
answer: A sleeping Mac has no network, so SSH dies in both directions — outbound sessions to servers drop, and nobody can reach the Mac itself. tmux or mosh on the remote end make the disconnection survivable; they do not prevent it. To keep the Mac from sleeping while someone is SSH'd into it, the ttyskeepawake setting (on by default) already does that. To keep an outbound session alive, the Mac must simply not sleep while it matters — a hold scoped to the work.
faq:
  - q: Why does my SSH session die when I close my MacBook?
    a: Closing the lid sleeps the Mac, sleep suspends the network stack, and the TCP connection under the SSH session goes with it. The server eventually notices the peer is gone and ends the session.
  - q: Does tmux stop SSH disconnecting?
    a: No — tmux runs on the server and keeps your programs and shell alive there when the connection drops, so you can reattach and continue. The disconnection still happens; tmux makes it cost nothing.
  - q: Can I SSH into a sleeping Mac?
    a: Not while it is asleep. With Wake for network access on, some traffic can wake it briefly on a local network, but for dependable remote access the honest answer is a machine that is awake — either held awake, or woken on a schedule you control.
  - q: Why does my Mac not sleep while someone is SSH'd in?
    a: The ttyskeepawake setting, on by default — while any terminal session is active the system skips idle sleep. A tty counts as inactive only once its idle time exceeds the sleep timer.
---

## Sleep is a network event

Every SSH connection is a TCP connection, and a sleeping Mac does not have those. Whatever else sleep suspends, it suspends the network stack, and both directions of SSH go down with it:

- **Outbound** — your laptop's session into a server dies when the laptop sleeps. The server's sshd notices a dead peer, eventually, and your remote shell — and anything running in the foreground of it — is gone.
- **Inbound** — nobody can SSH into a sleeping Mac, because there is nothing listening. The machine is not slow to answer; it is absent.

Most advice on this topic addresses one direction while sounding like it addresses both, which is how people end up with tmux installed and a job that still died.

## Outbound: make the disconnection cost nothing, or prevent it

**tmux (or screen) runs on the server, not on your Mac.** When your laptop sleeps and the connection drops, the tmux session on the server keeps your shell and your processes alive. You reattach in the morning and the overnight build is still there. This is the right tool when *disconnection is acceptable* and what matters is that remote work survives it.

**mosh smooths the reconnection** — it picks the session back up when your Mac returns, without a fresh login. Same category: it makes dropping and returning cheap. It does not keep anything alive on your Mac's side.

**Neither prevents the disconnect.** If the session itself must stay up — a long interactive process you are watching, a port-forward something depends on — then the Mac must not sleep while it matters, and the clean way is [a hold scoped to the work](../keep-mac-awake-terminal-command/), not a permanent settings change. With the lid closed, that becomes [a different problem](../keep-macbook-awake-lid-closed/) with a different answer.

## Inbound: the Mac as the server

Two settings govern this, both visible in [`pmset -g`](../pmset-sleep-settings-explained/):

**`ttyskeepawake`, on by default, is why an active SSH session keeps the Mac up.** From `pmset(1)`: it prevents idle system sleep while any tty is active, and a tty counts as inactive only once its idle time exceeds the system sleep timer. In practice: while you are working in the session, the Mac stays up; walk away, and the idle clock eventually runs out on both.

**`womp` — Wake for network access — keeps a sleeping Mac reachable, barely.** The machine wakes briefly for certain traffic on the local network. These are [dark wakes](../dark-wake-power-nap/), seconds long, and fine for what they are for; a machine you depend on reaching over SSH at arbitrary times is a machine that should actually be awake.

And note what `ttyskeepawake` does not cover: the lid. An SSH session into a MacBook whose lid closes ends the way [everything ends when the lid closes](../caffeinate-lid-closed/).

## The overnight case

The pattern behind most SSH-and-sleep frustration is a Mac that needs to be *available* — as the client driving remote work, or as the server being reached — during hours when it would naturally sleep. Piecemeal fixes stack up: tmux for the disconnects, womp for reachability, a longer sleep timer for the evening.

The stable arrangement is simpler to state: the machine should be awake exactly when something needs it, and asleep otherwise. For sessions, that is a hold scoped to the session's work. For scheduled overnight work, it is [a wake armed for the job's fire time](../wake-mac-for-scheduled-job-pmset/) — which is the arrangement goguma maintains automatically, and the reason [a sleeping Mac's cron jobs](../cron-jobs-dont-run-mac-asleep/) are the adjacent problem this one usually turns out to be.
