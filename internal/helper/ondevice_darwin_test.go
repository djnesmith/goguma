package helper

import (
	"os/exec"
	"testing"
)

// TestReadSleepBlockedAgainstTheRealSystem exercises the actual pmset call.
//
// Reading power-management state needs no privilege, so the real function can
// be run here rather than faked. That matters: this is the value `status`
// reports and the value `Recover` decides on at boot, and if the parse
// silently returned false for a blocked machine, a stranded block would never
// be cleared and nothing would look wrong.
//
// Only the two write operations genuinely require root. Everything else in
// this package can be, and now is, verified on the machine itself.
// TestParseSleepDisabled pins the parse against real `pmset -g` output.
//
// This is the half of the old on-device test that was worth keeping. The
// fixtures are captured verbatim from a live machine, tabs included: the
// separator is not a space, and the line is emitted for both values rather
// than omitted when sleep is allowed, so a parser cannot assume either.
func TestParseSleepDisabled(t *testing.T) {
	const blockedOutput = "System-wide power settings:\n SleepDisabled\t\t1\nCurrently in use:\n sleep\t0\n"
	const allowedOutput = "System-wide power settings:\n SleepDisabled\t\t0\nCurrently in use:\n sleep\t10\n"

	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"blocked, tab separated", blockedOutput, true},
		{"allowed, tab separated", allowedOutput, false},
		{"space separated", "SleepDisabled 1\n", true},
		{"trailing carriage return", "SleepDisabled\t1\r\n", true},
		{"field absent entirely", "Currently in use:\n sleep\t10\n", false},
		{"empty output", "", false},
		{"value is not a number", "SleepDisabled\tyes\n", false},
		{"name appears without a value", "SleepDisabled\n", false},
		{"must not match a longer field name", "SleepDisabledUntil\t1\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSleepDisabled(c.out); got != c.want {
				t.Errorf("parseSleepDisabled(%q) = %v, want %v", c.out, got, c.want)
			}
		})
	}
}

// TestReadSleepBlockedAgainstTheRealSystem checks that the real pmset call
// works on this machine.
//
// It deliberately does not assert which value comes back. SleepDisabled is
// global mutable state that this very daemon changes: it takes the block when
// a wake window opens and releases it when the job finishes, so any assertion
// about the value is a race against a job starting. Correctness of the parse
// is covered hermetically by TestParseSleepDisabled; what is left to verify
// here is only that the command runs and its output is understood.
func TestReadSleepBlockedAgainstTheRealSystem(t *testing.T) {
	if testing.Short() {
		t.Skip("on-device probe skipped in short mode")
	}
	if _, err := exec.LookPath("pmset"); err != nil {
		t.Skip("pmset unavailable")
	}

	blocked, err := readSleepBlocked()
	if err != nil {
		t.Fatalf("reading the real sleep state failed: %v", err)
	}
	t.Logf("SleepDisabled on this machine: %v", blocked)
}

// TestScheduledWakesAgainstTheRealSystem runs the read-back that the whole
// wake guarantee depends on.
//
// PRD §5.2: a scheduled wake can be silently dropped, so success from the
// scheduling command is not treated as proof. This is the check that turns
// that into something verifiable, and it must not claim other applications'
// entries, of which a normal Mac has several.
func TestScheduledWakesAgainstTheRealSystem(t *testing.T) {
	if testing.Short() {
		t.Skip("on-device probe skipped in short mode")
	}
	if _, err := exec.LookPath("pmset"); err != nil {
		t.Skip("pmset unavailable")
	}

	ours, err := scheduledWakes()
	if err != nil {
		t.Fatalf("reading the real wake schedule failed: %v", err)
	}
	t.Logf("wake entries owned by goguma: %d", len(ours))

	// The machine has Apple-owned entries; none of them may be claimed.
	out, err := exec.Command("pmset", "-g", "sched").Output()
	if err != nil {
		t.Fatal(err)
	}
	// Legacy entries from the binary's previous name are deliberately
	// claimed too: an upgrade must be able to reconcile them away instead of
	// leaving an orphaned wake nothing can cancel.
	total, mine := 0, 0
	for _, line := range splitLinesForTest(string(out)) {
		if !containsForTest(line, " at ") {
			continue
		}
		total++
		if containsForTest(line, "'"+wakeOwnerTag+"'") ||
			containsForTest(line, "'"+legacyWakeOwnerTag+"'") {
			mine++
		}
	}
	t.Logf("scheduled power events on this machine: %d total, %d ours", total, mine)

	if len(ours) != mine {
		t.Errorf("scheduledWakes() returned %d entries, but %d lines carry our owner tags",
			len(ours), mine)
	}
	if total > 0 && mine == 0 && len(ours) != 0 {
		t.Error("entries were claimed despite none carrying our owner tags")
	}
}

// Small local helpers, kept here so the test does not depend on the shape of
// any string utility in the package under test.

func splitLinesForTest(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func containsForTest(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
