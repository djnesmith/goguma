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
// to deadlock the helper's policy lock; if that happened while blocked, the
// block could never be cleared.
const pmsetTimeout = 10 * time.Second

// setSleepBlocked applies or clears the global SleepDisabled power setting.
//
// This is the blunt instrument, chosen deliberately. Adrafinil's team
// verified on-device (macOS 26.3) that the cleaner in-process paths do NOT
// keep a displayless lid-closed Mac awake:
//
//   - private RootDomainUserClient selector 12 (setClamShellSleepDisable)
//     returns success but the Mac still sleeps; it governs the
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
	return parseSleepDisabled(string(out)), nil
}

// parseSleepDisabled pulls the SleepDisabled field out of `pmset -g` output.
//
// Split out from readSleepBlocked so it can be tested against fixtures. The
// test that checked it used to sample the live machine twice, once through
// this parser and once through its own, and compare. That compares two
// observations of a value the daemon legitimately changes: every wake window
// takes the block and every close releases it, so the test failed whenever it
// ran during a job. What is actually worth asserting is the parse, and that
// needs no live machine at all.
//
// The field is separated from its value by tabs, not spaces, and the line is
// present for both 0 and 1, so neither the separator nor the absent case can
// be assumed.
func parseSleepDisabled(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "SleepDisabled" {
			return fields[1] == "1"
		}
	}
	return false
}

const pmsetTimeLayout = "01/02/06 15:04:05"

// scheduleWake registers an OS wake at t.
//
// `wake` wakes a sleeping machine. `wakeorpoweron` additionally boots one that
// is powered off, better for job survival, but silently powering on a
// shut-down laptop is a surprise, so it is opt-in via config.
func scheduleWake(t time.Time, useWakeOrPowerOn bool) error {
	kind := "wake"
	if useWakeOrPowerOn {
		kind = "wakeorpoweron"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	stamp := t.Local().Format(pmsetTimeLayout)
	out, err := exec.CommandContext(ctx, "pmset", "schedule", kind, stamp, wakeOwnerTag).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset schedule %s %q failed: %w: %s",
			kind, stamp, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cancelWake removes a previously scheduled entry. Cancelling a wake that is
// no longer registered is not an error; the kernel may already have consumed
// it, which is the normal case after the machine actually woke.
func cancelWake(e wakeEntry) error {
	kind := "wake"
	if e.powerOn {
		kind = "wakeorpoweron"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pmsetTimeout)
	defer cancel()

	stamp := e.at.Local().Format(pmsetTimeLayout)
	out, err := exec.CommandContext(ctx, "pmset", "schedule", "cancel", kind, stamp, e.owner).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("pmset schedule cancel timed out")
		}
		// A real refusal must surface: swallowing it reported "cancelled"
		// while the entry stayed registered, and for a wakeorpoweron entry
		// that means the machine keeps powering itself on.
		return fmt.Errorf("pmset schedule cancel failed: %w: %s",
			err, strings.TrimSpace(string(out)))
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
// goguma exists to prevent, so "did pmset return success" is not treated
// as sufficient evidence.
func scheduledWakes() ([]wakeEntry, error) {
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
//	[0]  wake at 08/05/2026 08:58:30 by 'goguma'
//
// Entries tagged with the binary's previous name are included so an upgrade
// can reconcile them away; the kind token is captured because cancel must
// repeat it exactly.
func parseScheduled(out string) []wakeEntry {
	var entries []wakeEntry
	for line := range strings.SplitSeq(out, "\n") {
		owner := ""
		switch {
		case strings.Contains(line, "'"+wakeOwnerTag+"'"):
			owner = wakeOwnerTag
		case strings.Contains(line, "'"+legacyWakeOwnerTag+"'"):
			owner = legacyWakeOwnerTag
		default:
			continue
		}
		idx := strings.Index(line, " at ")
		if idx < 0 {
			continue
		}
		kindFields := strings.Fields(line[:idx])
		powerOn := len(kindFields) > 0 &&
			strings.Contains(kindFields[len(kindFields)-1], "poweron")
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
		entries = append(entries, wakeEntry{at: t, powerOn: powerOn, owner: owner})
	}
	return entries
}
