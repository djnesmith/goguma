package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// TestIntervalWakeTargetDoesNotRecede is the end-to-end regression test for
// interval jobs never being woken for.
//
// nextWakeTarget re-parses every job's schedule on every tick. While interval
// schedules were unanchored, each parse answered "one interval from now", so
// the target moved forward by exactly the time between ticks. scheduleNextWake
// compares the new target against the registered one, sees a different time,
// and re-registers, every tick, forever. The wake was always in the future
// and never arrived, so an interval job's machine was never woken.
//
// The assertion is the one the live reproduction failed: the same job, polled
// four seconds apart, must report the same fire time and the same wake time.
func TestIntervalWakeTargetDoesNotRecede(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	created := time.Date(2026, 8, 1, 5, 47, 3, 0, time.Local)
	if err := d.store.Add(&model.Job{
		Name:      "watchdog",
		Schedule:  "every 6h",
		Detection: model.DetectNone,
		Enabled:   true,
		CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}

	now := created.Add(5 * time.Hour)
	_, fire1, wake1, ok := d.nextWakeTarget(now, d.cfg)
	if !ok {
		t.Fatal("no wake target for an enabled interval job")
	}

	later := now.Add(4 * time.Second)
	_, fire2, wake2, ok := d.nextWakeTarget(later, d.cfg)
	if !ok {
		t.Fatal("no wake target on the second tick")
	}

	if !fire1.Equal(fire2) {
		t.Errorf("fire time moved from %s to %s between two ticks four seconds apart",
			fire1.Format(time.RFC3339), fire2.Format(time.RFC3339))
	}
	if !wake1.Equal(wake2) {
		t.Errorf("wake time moved from %s to %s; scheduleNextWake would re-register "+
			"on every tick and the wake would never arrive",
			wake1.Format(time.RFC3339), wake2.Format(time.RFC3339))
	}
	if want := created.Add(6 * time.Hour); !fire1.Equal(want) {
		t.Errorf("fire = %s, want %s; six hours from the job's created_at",
			fire1.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestIntervalJobListAgreesWithTheWakeScheduler guards the failure that would
// be worse than the one being fixed.
//
// The job list feeds the GUI's countdown and the wake scheduler drives the
// machine. They parse the same schedule in different files, so if they
// anchored it differently the machine would wake at a time the user was never
// shown, and neither number would look wrong on its own.
func TestIntervalJobListAgreesWithTheWakeScheduler(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := d.store.Add(&model.Job{
		Name:      "watchdog",
		Schedule:  "every 6h",
		Detection: model.DetectNone,
		Enabled:   true,
		CreatedAt: time.Now().Add(-5 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	resp := d.jobsList()
	if len(resp.Jobs) != 1 || resp.Jobs[0].NextFire == nil {
		t.Fatalf("expected one job with a next fire time, got %+v", resp.Jobs)
	}
	_, fire, _, ok := d.nextWakeTarget(time.Now(), d.cfg)
	if !ok {
		t.Fatal("no wake target")
	}
	if !resp.Jobs[0].NextFire.Equal(fire) {
		t.Errorf("the job list shows %s and the wake scheduler targets %s",
			resp.Jobs[0].NextFire.Format(time.RFC3339), fire.Format(time.RFC3339))
	}
}
