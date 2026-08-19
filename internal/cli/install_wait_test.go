package cli

import (
	"strings"
	"testing"
)

// The installer waits for adoption rather than reporting a partial scan.
//
// It used to sync once, list once, and print whatever had turned up in that
// instant. A daemon two seconds old has not finished working out what is on the
// machine, so setup announced a fraction of the jobs, or none, and the rest
// appeared in the menu bar ten seconds after the terminal said it was done. The
// number a new user was handed was wrong at the moment they read it.
//
// Checked as source rather than behaviour because the alternative is a fake
// daemon whose scan is deliberately slow, which would test the fake.
func TestSetupWaitsForTheScanToSettle(t *testing.T) {
	root := repoRoot(t)
	src := readDoc(t, root, "internal/cli/install.go")

	if !strings.Contains(src, "func waitForAdoption(") {
		t.Fatal("install.go no longer waits for adoption; a partial scan will be reported as the answer")
	}
	if !strings.Contains(src, "stable >= settledAfter") {
		t.Error("the wait no longer settles on a stable count, so it is a fixed sleep " +
			"rather than a wait, and wrong on both a fast machine and a slow one")
	}
	// Zero has to hold longer than a real count before it is believed: early on
	// it means the scan has not begun, and settling on it would tell somebody
	// with a full crontab that they have no scheduled work. But it must settle
	// eventually, or a machine that genuinely has none waits the full allowance
	// for an answer that was correct in the first second.
	if !strings.Contains(src, "settledAtZero") {
		t.Error("zero settles on the same threshold as a real count, so either a " +
			"slow scan reports 'no jobs' or an empty machine waits the whole allowance")
	}
}

// And it says so honestly afterwards.
func TestSetupDoesNotPromiseJobsItAlreadyWaitedFor(t *testing.T) {
	root := repoRoot(t)
	src := readDoc(t, root, "internal/cli/install.go")

	for _, stale := range []string{"still looking for scheduled jobs", "Jobs keep arriving"} {
		if strings.Contains(src, stale) {
			t.Errorf("install still says %q, which contradicts waiting for the scan", stale)
		}
	}
}
