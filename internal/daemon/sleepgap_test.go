package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// A daemon stuck in a call to the helper is not a sleeping machine.
//
// Sleep is inferred from a wall-clock gap between ticks, and the loop makes
// blocking helper calls with a 45s timeout, so the two look identical from the
// clock alone. On this machine three stalled pushes were logged as 2m20s of
// sleep while the jobs in that window ran and reported success, and
// recordSleptThrough writes a missed run for every fire inside a sleep
// interval. The stall is subtracted before the gap is judged.
func TestAStalledLoopIsNotSleep(t *testing.T) {
	d := &Daemon{}
	d.cfg.TickInterval = model.Duration(10 * time.Second)

	now := time.Now()
	d.lastTick = now.Add(-140 * time.Second)
	// All of it spent inside the loop's own work.
	d.busy = 140 * time.Second

	if gap, ok := d.sleepGap(now); ok {
		t.Fatalf("a %s stall was read as sleep; missed runs would be written for jobs that ran", gap)
	}
}

// The real thing still registers. A machine that sleeps does not run the
// loop, so the gap is not accounted for by busy time.
func TestARealSleepIsStillDetected(t *testing.T) {
	d := &Daemon{}
	d.cfg.TickInterval = model.Duration(10 * time.Second)

	now := time.Now()
	d.lastTick = now.Add(-8 * time.Hour)
	d.busy = 30 * time.Millisecond

	gap, ok := d.sleepGap(now)
	if !ok {
		t.Fatal("an eight-hour gap was not detected as sleep")
	}
	if gap < 7*time.Hour {
		t.Errorf("sleep measured as %s, want about 8h", gap)
	}
}

// A stall inside a genuine sleep window must narrow the interval, not cancel
// it: the machine slept, and the part of the gap the daemon was awake for is
// not part of that sleep.
func TestAStallInsideASleepNarrowsTheWindow(t *testing.T) {
	d := &Daemon{}
	d.cfg.TickInterval = model.Duration(10 * time.Second)

	now := time.Now()
	d.lastTick = now.Add(-10 * time.Minute)
	d.busy = 90 * time.Second

	gap, ok := d.sleepGap(now)
	if !ok {
		t.Fatal("a ten-minute gap with a 90s stall in it was not detected as sleep")
	}
	if want := 8*time.Minute + 30*time.Second; gap != want {
		t.Errorf("sleep measured as %s, want %s", gap, want)
	}
}

// Claiming the stall must be one-shot. Leaving it on the clock would discount
// the next window too, and a long enough carry-over would hide a real sleep.
func TestStallTimeIsClaimedOnce(t *testing.T) {
	d := &Daemon{}
	d.cfg.TickInterval = model.Duration(10 * time.Second)

	now := time.Now()
	d.lastTick = now.Add(-140 * time.Second)
	d.busy = 140 * time.Second
	d.sleepGap(now)

	if d.busy != 0 {
		t.Fatalf("busy is %s after being claimed; the next window would be discounted for a stall it did not have", d.busy)
	}
}
