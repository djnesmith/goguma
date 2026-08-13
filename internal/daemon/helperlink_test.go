package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestStatusPopulatesTheCachedVersion covers the reporting bug where the helper
// was shown as connected but nameless.
//
// Status is the only call that returns the helper's version, and nothing called
// it, so the cache stayed empty for the daemon's whole life and every surface
// printed a blank: `goguma version` ended with "helper" and nothing after.
// Repush now refreshes it; this pins the half that does the filling.
//
// Deliberately not exercising Repush itself: on a machine with a helper running
// it would push the zero-value desired state and release a live sleep block.
func TestStatusPopulatesTheCachedVersion(t *testing.T) {
	h := newHelperLink(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if v := h.Version(); v != "" {
		t.Fatalf("a fresh link already reports version %q", v)
	}

	if _, err := h.Status(); err != nil {
		t.Skip("no privileged helper is listening; this needs a real one")
	}
	if h.Version() == "" {
		t.Error("Status() succeeded but left the cached version empty, " +
			"so status and doctor would report a connected helper with no version")
	}
}

// A cancel the helper never heard must stay pending so Repush retries it.
// Losing it leaves the wake registered with the OS forever: the machine
// keeps waking for a job that was skipped, removed, or disabled.
func TestCancelWakeRemembersAFailedCancel(t *testing.T) {
	h := newHelperLink(slog.New(slog.NewTextHandler(io.Discard, nil)))
	at := time.Now().Add(time.Hour)
	h.mu.Lock()
	h.scheduledAt, h.scheduledKind = &at, true
	h.mu.Unlock()

	// No helper is listening in a test environment, so the call must fail.
	if err := h.CancelWake(); err == nil {
		t.Skip("a live helper answered; this test needs the failure path")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.scheduledAt != nil {
		t.Error("scheduledAt survived a cancel; Repush would re-assert the cancelled wake")
	}
	if !h.cancelPending {
		t.Error("a failed cancel was not remembered; nothing will ever retry it")
	}
}
