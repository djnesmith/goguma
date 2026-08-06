package helper

import (
	"testing"
	"time"
)

// TestParseScheduledReadsRealPmsetOutput covers the read-back that the whole
// wake guarantee rests on.
//
// PRD §5.2 is explicit that a scheduled wake can be silently dropped by the
// system, so "pmset returned success" is not treated as proof. This parser is
// what turns that into a verifiable claim — if it silently matched nothing,
// verification would always fail open and the mitigation would be decorative.
func TestParseScheduledReadsRealPmsetOutput(t *testing.T) {
	// Captured verbatim from `pmset -g sched` on macOS 26.5, including the
	// competing Apple-owned entries that are the reason owner tagging exists.
	const out = `Scheduled power events:
 [0]  wake at 08/04/2026 11:54:47 by 'com.apple.alarm.user-invisible-com.apple.calaccessd.travelEngine.periodicRefreshTimer'
 [1]  wake at 08/05/2026 00:00:00 by 'com.apple.alarm.user-visible-com.apple.donotdisturb.server.ScheduleLifetimeMonitor.timer' User visible: true
 [2]  wake at 08/06/2026 08:58:30 by 'wakeguard'
 [3]  wake at 08/07/2026 02:07:00 by 'wakeguard'
`
	got := parseScheduled(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d of our entries, want 2 (Apple's must not be claimed)", len(got))
	}
	want := time.Date(2026, 8, 6, 8, 58, 30, 0, time.Local)
	if !got[0].Equal(want) {
		t.Errorf("first entry = %s, want %s", got[0], want)
	}
}

func TestParseScheduledIgnoresOtherOwners(t *testing.T) {
	// Claiming another application's wake would make VerifyWake report success
	// for an entry we never registered, and cancelling it would delete
	// someone else's alarm.
	const out = ` [0]  wake at 08/06/2026 08:58:30 by 'com.apple.alarm.something'
 [1]  poweron at 08/06/2026 09:00:00 by 'SomeOtherApp'
`
	if got := parseScheduled(out); len(got) != 0 {
		t.Errorf("claimed %d entries belonging to other owners: %v", len(got), got)
	}
}

func TestParseScheduledSurvivesGarbage(t *testing.T) {
	for _, in := range []string{
		"", "Scheduled power events:\n", "nonsense\n\n\n",
		" [0]  wake at not-a-date by 'wakeguard'\n",
		" [0]  wake by 'wakeguard'\n",
		"\x00\x01\x02",
	} {
		// Must not panic, and must not invent an entry.
		if got := parseScheduled(in); len(got) != 0 {
			t.Errorf("parseScheduled(%q) invented %v", in, got)
		}
	}
}

// TestWakeTimeFormatMatchesWhatPmsetAccepts pins the layout string.
//
// pmset takes "MM/dd/yy HH:mm:ss". Getting the layout wrong produces a command
// that either fails or, worse, schedules a wake at the wrong time — and since
// nothing else in the system would object, it would only surface as a job that
// silently did not run.
func TestWakeTimeFormatMatchesWhatPmsetAccepts(t *testing.T) {
	at := time.Date(2026, 8, 6, 8, 58, 30, 0, time.Local)
	got := at.Format(pmsetTimeLayout)
	if got != "08/06/26 08:58:30" {
		t.Errorf("formatted %q, want %q", got, "08/06/26 08:58:30")
	}

	// A single-digit month, day and hour must stay zero-padded; pmset rejects
	// the unpadded form.
	early := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	if got := early.Format(pmsetTimeLayout); got != "01/02/26 03:04:05" {
		t.Errorf("formatted %q, want zero-padded %q", got, "01/02/26 03:04:05")
	}
}
