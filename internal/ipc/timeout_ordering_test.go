package ipc

import (
	"testing"
	"time"
)

// TestCallerDeadlineOutlastsTheHelpersOwnBudget guards an incoherence that
// produced an endless reconnect loop on a perfectly healthy machine.
//
// The helper bounds each pmset invocation at 10s. The daemon used to wait only
// DefaultTimeout (5s) for the reply, so any pmset between those two numbers
// timed out on the caller while the callee was still working; the helper then
// answered into a closed connection, the daemon declared it lost, reconnected,
// and repeated. Both sides were behaving as written; the deadlines simply
// disagreed.
//
// Whatever these constants become, the caller must outlast the callee.
func TestCallerDeadlineOutlastsTheHelpersOwnBudget(t *testing.T) {
	// Mirrors helper.pmsetTimeout, which lives in a package this one must not
	// import (the helper imports ipc). Kept as a literal on purpose: if that
	// constant moves, this test should be the thing that notices.
	const helperPmsetTimeout = 10 * time.Second

	if HelperTimeout <= helperPmsetTimeout {
		t.Fatalf("HelperTimeout (%s) must exceed the helper's pmset budget (%s); "+
			"a caller that gives up first turns every slow pmset into a lost helper",
			HelperTimeout, helperPmsetTimeout)
	}

	// And the CLI default must stay short: it is invoked from cron and must not
	// hang a job for a quarter of a minute.
	if DefaultTimeout >= HelperTimeout {
		t.Errorf("DefaultTimeout (%s) should stay well under HelperTimeout (%s)",
			DefaultTimeout, HelperTimeout)
	}
}
