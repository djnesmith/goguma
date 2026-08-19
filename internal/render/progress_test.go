package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestWaitForStopsAsSoonAsItSucceeds. The allowance is twenty seconds; a
// service that answers in one must not cost the other nineteen.
func TestWaitForStopsAsSoonAsItSucceeds(t *testing.T) {
	r := NewPlain(&bytes.Buffer{})
	calls := 0
	start := time.Now()
	ok := r.WaitFor("waiting", 40, time.Millisecond, func() bool {
		calls++
		return calls == 3
	})
	if !ok {
		t.Fatal("reported failure for a check that succeeded")
	}
	if calls != 3 {
		t.Errorf("check ran %d times, want 3", calls)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("took %s; it should stop at the first success", elapsed)
	}
}

// TestWaitForGivesUpAfterItsAllowance, rather than blocking forever on a
// service that is never coming.
func TestWaitForGivesUpAfterItsAllowance(t *testing.T) {
	r := NewPlain(&bytes.Buffer{})
	calls := 0
	if r.WaitFor("waiting", 5, time.Millisecond, func() bool { calls++; return false }) {
		t.Fatal("reported success for a check that never succeeded")
	}
	if calls != 5 {
		t.Errorf("check ran %d times, want 5", calls)
	}
}

// TestWaitForWritesNothingWhenNotATerminal.
//
// The bar rewrites its line with carriage returns, which in a log file or a CI
// transcript is one unreadable smear. `goguma install` is run from scripts and
// from the app's own first run, and neither has a terminal.
func TestWaitForWritesNothingWhenNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlain(&buf)
	r.WaitFor("waiting", 3, time.Millisecond, func() bool { return false })
	if buf.Len() != 0 {
		t.Errorf("wrote %q to a non-terminal; expected silence", buf.String())
	}
	if strings.Contains(buf.String(), "\r") {
		t.Error("carriage returns reached a non-terminal")
	}
}
