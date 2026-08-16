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

// The control loop must not be able to block on the helper.
//
// This is the regression that cost the most: a helper mid-restart accepts the
// connection and never answers, HelperTimeout is 45s, and the push was made
// from the two-second poll on the loop goroutine. Three of those back to back
// stalled the daemon for 2m20s, which `detectSleepGap` recorded as the machine
// having slept and wrote missed runs against, while the helper's dead-man
// switch released the sleep block for want of contact.
func TestPushDoesNotWaitOnTheHelper(t *testing.T) {
	h := newHelperLink(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// The pump is deliberately not started: nothing may drain the queue, and
	// the caller must still come straight back.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			h.Push(true, "jobs: something")
			h.Repush()
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Push blocked on the helper; a stalled socket read freezes the control loop again")
	}
}

// Latest state wins, and a burst collapses.
//
// The queue is one slot deep on purpose. What matters is not that every push
// is delivered but that the one the daemon currently wants is, so a hundred
// pushes during a stalled call must leave the final desired state behind, not
// the first.
func TestABurstOfPushesLeavesTheLatestState(t *testing.T) {
	h := newHelperLink(slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := range 100 {
		h.Push(i%2 == 0, "push")
	}
	h.Push(false, "idle")

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wantBlocked {
		t.Error("the desired state is stale; the helper would be told to block sleep after the daemon stopped wanting it")
	}
	if h.blockReason != "idle" {
		t.Errorf("desired reason is %q, want %q", h.blockReason, "idle")
	}
}

// Settle must return even when the pump never stops, or a helper that has
// stopped answering turns shutdown into a hang and launchd kills the daemon
// before it clears the block.
func TestSettleGivesUp(t *testing.T) {
	h := newHelperLink(slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := time.Now()
	h.Settle(50 * time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Settle waited %s on a pump that never started", d)
	}
}
