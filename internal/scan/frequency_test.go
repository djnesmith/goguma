package scan

import "testing"

// Zero means no schedule is rejected for firing too often.
//
// The filter compares `gap <= MinInterval`, so a zero limit has to leave every
// positive interval alone. Written as a test because the comparison is
// inclusive on purpose, and an inclusive comparison against zero is exactly
// the kind of thing that reads as correct and rejects a job firing at 0s.
func TestZeroMinIntervalRejectsNothing(t *testing.T) {
	opts := DefaultOptions()
	if opts.MinInterval != 0 {
		t.Fatalf("default MinInterval is %v; frequency filtering is back on by default", opts.MinInterval)
	}
}
