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
func TestReadSleepBlockedAgainstTheRealSystem(t *testing.T) {
	if testing.Short() {
		t.Skip("on-device probe skipped in short mode")
	}
	if _, err := exec.LookPath("pmset"); err != nil {
		t.Skip("pmset unavailable")
	}

	// Sampled together, and retried on disagreement.
	//
	// Both reads observe the same live system, and that system changes: a
	// wake window opening or closing flips SleepDisabled underneath a test
	// that reads it twice. This failed exactly once, at :58, while a job's
	// hold was being taken — a real mismatch that says nothing about the
	// parser. Retrying keeps what the test is actually for (a parser that
	// always returns false, or that misreads the field, still fails every
	// attempt) without failing for being observed at a bad moment.
	const attempts = 5
	var blocked, wantBlocked bool
	var agreed bool
	for i := 0; i < attempts; i++ {
		var err error
		blocked, err = readSleepBlocked()
		if err != nil {
			t.Fatalf("reading the real sleep state failed: %v", err)
		}
		out, err := exec.Command("pmset", "-g").Output()
		if err != nil {
			t.Fatal(err)
		}
		wantBlocked = false
		for _, line := range splitLinesForTest(string(out)) {
			fields := fieldsForTest(line)
			if len(fields) >= 2 && fields[0] == "SleepDisabled" {
				wantBlocked = fields[1] == "1"
			}
		}
		if blocked == wantBlocked {
			agreed = true
			break
		}
	}
	t.Logf("SleepDisabled on this machine: %v", blocked)
	if !agreed {
		t.Errorf("readSleepBlocked() = %v, but `pmset -g` reports %v, over %d attempts",
			blocked, wantBlocked, attempts)
	}
}

// TestScheduledWakesAgainstTheRealSystem runs the read-back that the whole
// wake guarantee depends on.
//
// PRD §5.2: a scheduled wake can be silently dropped, so success from the
// scheduling command is not treated as proof. This is the check that turns
// that into something verifiable — and it must not claim other applications'
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
	t.Logf("wake entries owned by wakeguard: %d", len(ours))

	// The machine has Apple-owned entries; none of them may be claimed.
	out, err := exec.Command("pmset", "-g", "sched").Output()
	if err != nil {
		t.Fatal(err)
	}
	total, mine := 0, 0
	for _, line := range splitLinesForTest(string(out)) {
		if !containsForTest(line, " at ") {
			continue
		}
		total++
		if containsForTest(line, "'"+wakeOwner+"'") {
			mine++
		}
	}
	t.Logf("scheduled power events on this machine: %d total, %d ours", total, mine)

	if len(ours) != mine {
		t.Errorf("scheduledWakes() returned %d entries, but %d lines carry our owner tag",
			len(ours), mine)
	}
	if total > 0 && mine == 0 && len(ours) != 0 {
		t.Error("entries were claimed despite none carrying our owner tag")
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

func fieldsForTest(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		if i > start {
			out = append(out, s[start:i])
		}
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
