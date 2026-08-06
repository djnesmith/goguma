package helper

import (
	"errors"
	"testing"
	"time"
)

// TestRepeatedScheduleDoesNotAccumulateEntries is the regression test for six
// identical wake events found in `pmset -g sched` on a live machine.
//
// The daemon re-asserts the same wake time routinely. The old code only
// cancelled the previous entry when the time had *changed*, so an unchanged
// re-assertion skipped the cancel and scheduled anyway — and `pmset schedule`
// does not deduplicate, so each call added another identical entry.
func TestRepeatedScheduleDoesNotAccumulateEntries(t *testing.T) {
	f := &fakeOps{}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Round(time.Second)

	for i := range 6 {
		if err := s.ScheduleWake(at, false); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.wakes) != 1 {
		t.Errorf("the OS holds %d wake entries after 6 identical requests, want 1: %v",
			len(f.wakes), f.wakes)
	}
	// The first call registers; the five that follow must be no-ops.
	if len(f.schedules) != 1 {
		t.Errorf("scheduleAt called %d times, want 1", len(f.schedules))
	}
}

// TestScheduleReconcilesEntriesThisProcessDidNotCreate covers the second half
// of the same bug: `scheduledAt` is in-memory, so a helper restart forgets
// every entry it owns while those entries survive in the system's schedule.
// Reconciling against the OS rather than against memory is what makes a
// restart converge instead of adding one more.
func TestScheduleReconcilesEntriesThisProcessDidNotCreate(t *testing.T) {
	stale1 := time.Now().Add(30 * time.Minute).Round(time.Second)
	stale2 := time.Now().Add(45 * time.Minute).Round(time.Second)
	want := time.Now().Add(time.Hour).Round(time.Second)

	// A fresh Service, as after a restart: no memory of these at all.
	f := &fakeOps{wakes: []time.Time{stale1, stale2}}
	s := withOps(f)

	if err := s.ScheduleWake(want, false); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.wakes) != 1 || !f.wakes[0].Equal(want) {
		t.Errorf("wake entries = %v, want exactly [%v]", f.wakes, want)
	}
	if len(f.cancels) != 2 {
		t.Errorf("cancelled %d stale entries, want 2", len(f.cancels))
	}
}

// TestScheduleKeepsAnAlreadyRegisteredWake makes sure the deduplication does
// not become "never schedule anything": an entry the OS already holds at the
// requested time is the goal state, and a changed time still re-registers.
func TestScheduleKeepsAnAlreadyRegisteredWake(t *testing.T) {
	at := time.Now().Add(time.Hour).Round(time.Second)
	f := &fakeOps{wakes: []time.Time{at}}
	s := withOps(f)

	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.wakes) != 1 || !f.wakes[0].Equal(at) {
		t.Errorf("wake entries = %v, want the existing [%v] untouched", f.wakes, at)
	}
	if len(f.cancels) != 0 {
		t.Errorf("cancelled %d entries, want 0 — the wanted wake was already registered", len(f.cancels))
	}
	f.mu.Unlock()

	// Moving the wake must replace it, not add a second.
	moved := at.Add(15 * time.Minute)
	if err := s.ScheduleWake(moved, false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.wakes) != 1 || !f.wakes[0].Equal(moved) {
		t.Errorf("wake entries = %v, want exactly [%v]", f.wakes, moved)
	}
}

// TestScheduleStillRegistersWhenTheScheduleCannotBeRead ensures an unreadable
// wake list degrades to the old behaviour rather than skipping the wake.
// Failing to wake the machine is the one outcome worse than a duplicate entry.
func TestScheduleStillRegistersWhenTheScheduleCannotBeRead(t *testing.T) {
	f := &fakeOps{listErr: errListUnavailable}
	s := withOps(f)
	at := time.Now().Add(time.Hour).Round(time.Second)

	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatalf("ScheduleWake returned %v; a wake must still be attempted", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.schedules) != 1 {
		t.Errorf("scheduleAt called %d times, want 1", len(f.schedules))
	}
}

var errListUnavailable = errors.New("pmset unavailable")

// TestScheduleCollapsesPreExistingDuplicates covers cleanup of the mess an
// earlier build already left in the system schedule — six identical entries in
// the case that prompted this. Deduplicating future calls is not enough if the
// entries already registered stay there forever.
func TestScheduleCollapsesPreExistingDuplicates(t *testing.T) {
	at := time.Now().Add(time.Hour).Round(time.Second)
	f := &fakeOps{wakes: []time.Time{at, at, at, at, at, at}}
	s := withOps(f)

	if err := s.ScheduleWake(at, false); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.wakes) != 1 || !f.wakes[0].Equal(at) {
		t.Errorf("wake entries = %d %v, want exactly one at %v", len(f.wakes), f.wakes, at)
	}
}
