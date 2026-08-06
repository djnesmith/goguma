package helper

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Mechanism names how sleep is actually being blocked, so `status` can report
// the truth rather than a generic success.
const Mechanism = "pmset disablesleep"

// pmsetTimeout bounds every pmset invocation. A wedged pmset must not be able
// to deadlock the helper's policy lock — if that happened while blocked, the
// block could never be cleared.
const pmsetTimeout = 10 * time.Second

// setSleepBlocked applies or clears the global SleepDisabled power setting.
//
// This is the blunt instrument, chosen deliberately. Adrafinil's team
// verified on-device (macOS 26.3) that the cleaner in-process paths do NOT
// keep a displayless lid-closed Mac awake:
//
//   - private RootDomainUserClient selector 12 (setClamShellSleepDisable)
//     returns success but the Mac still sleeps — it governs the
//     external-display clamshell path, not no-display lid close;
//   - IORegistryEntrySetCFProperty(IOPMrootDomain, "SleepDisabled", …)
//     returns kIOReturnNotPermitted even as root;
//   - IOPMSetSystemPowerSetting("SleepDisabled", …) returns success but
//     `pmset -g` still shows SleepDisabled 0 and the Mac sleeps, because
//     pmset coordinates IOPMSetPMPreferences and an activation step around
//     that call which the bare call does not reproduce.
//
// Only the full `pmset -a disablesleep` was verified to work. It is global
// and also suppresses idle sleep, but it is Apple's own tested implementation
// and it is the path that actually holds. See Docs/ARCHITECTURE.md.
//
// It runs only on state flips, so the subprocess cost is irrelevant.
func setSleepBlocked(blocked bool) error {
	v := "0"
	if blocked {
		v = "1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pmset", "-a", "disablesleep", v).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("pmset disablesleep %s timed out after %s", v, pmsetTimeout)
		}
		return fmt.Errorf("pmset disablesleep %s failed: %w: %s", v, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// readSleepBlocked reports the kernel's current SleepDisabled setting.
//
// The helper re-reads this rather than trusting its own memory because the
// setting is global: another process, or a sleep/wake cycle, can change it
// underneath us.
func readSleepBlocked() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pmset", "-g").Output()
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "SleepDisabled" {
			return fields[1] == "1", nil
		}
	}
	return false, nil
}

// wakeOwner tags our scheduled wake entries.
//
// pmset supports an owner string, and it matters: `pmset -g sched` on a normal
// Mac already shows several competing entries from Apple subsystems. Tagging
// lets us cancel precisely our own entry instead of using `cancelall`, which
// would delete the user's alarms and other apps' scheduled events.
const wakeOwner = "wakeguard"

const pmsetTimeLayout = "01/02/06 15:04:05"

// scheduleWake registers an OS wake at t.
//
// `wake` wakes a sleeping machine. `wakeorpoweron` additionally boots one that
// is powered off — better for job survival, but silently powering on a
// shut-down laptop is a surprise, so it is opt-in via config.
func scheduleWake(t time.Time, useWakeOrPowerOn bool) error {
	kind := "wake"
	if useWakeOrPowerOn {
		kind = "wakeorpoweron"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	stamp := t.Local().Format(pmsetTimeLayout)
	out, err := exec.CommandContext(ctx, "pmset", "schedule", kind, stamp, wakeOwner).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset schedule %s %q failed: %w: %s",
			kind, stamp, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cancelWake removes a previously scheduled entry. Cancelling a wake that is
// no longer registered is not an error — the kernel may already have consumed
// it, which is the normal case after the machine actually woke.
func cancelWake(t time.Time, useWakeOrPowerOn bool) error {
	kind := "wake"
	if useWakeOrPowerOn {
		kind = "wakeorpoweron"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	stamp := t.Local().Format(pmsetTimeLayout)
	_, err := exec.CommandContext(ctx, "pmset", "schedule", "cancel", kind, stamp, wakeOwner).CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("pmset schedule cancel timed out")
	}
	return nil
}

// scheduledWakes reads back our own registered entries, so the daemon can
// verify that a wake it asked for is genuinely present.
//
// This verification is the mitigation for the quirk called out in PRD §5.2:
// pmset entries can be silently dropped or overwritten by other apps, and a
// wake that was accepted at request time may simply not be there later. A job
// that never fires because its wake vanished is exactly the silent failure
// WakeGuard exists to prevent, so "did pmset return success" is not treated
// as sufficient evidence.
func scheduledWakes() ([]time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pmset", "-g", "sched").Output()
	if err != nil {
		return nil, err
	}
	return parseScheduled(string(out)), nil
}

// parseScheduled extracts our entries from `pmset -g sched` output, e.g.
//
//	[0]  wake at 08/05/2026 08:58:30 by 'wakeguard'
func parseScheduled(out string) []time.Time {
	var times []time.Time
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, "'"+wakeOwner+"'") {
			continue
		}
		idx := strings.Index(line, " at ")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+4:])
		by := strings.Index(rest, " by ")
		if by < 0 {
			continue
		}
		stamp := strings.TrimSpace(rest[:by])
		// pmset prints a four-digit year here even though it accepts two.
		t, err := time.ParseInLocation("01/02/2006 15:04:05", stamp, time.Local)
		if err != nil {
			if t, err = time.ParseInLocation(pmsetTimeLayout, stamp, time.Local); err != nil {
				continue
			}
		}
		times = append(times, t)
	}
	return times
}
