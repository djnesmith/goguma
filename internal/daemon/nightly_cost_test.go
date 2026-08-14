package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
)

// The projection is the number the jobs window leads with, so the arithmetic
// behind it is worth pinning: firings across the night times what one firing
// has actually cost.
func TestNightlyCostMultipliesFiringsByMeasuredDrain(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}

	// Every 30 minutes: 16 firings across an 8 hour night.
	job := &model.Job{
		ID: "poller", Name: "poller", Schedule: "every 30m",
		Detection: model.DetectNone, Enabled: true,
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}

	// Two runs on battery, 1% each, so BatteryPerRun is 1.
	now := time.Now()
	for i := range 2 {
		start := now.Add(-time.Duration(i+1) * time.Hour)
		if err := d.store.AppendRun(model.Run{
			JobID: "poller", Outcome: model.OutcomeOK,
			Started: start, Ended: start.Add(30 * time.Second),
			Duration:     model.Duration(30 * time.Second),
			BatteryStart: 50, BatteryEnd: 49,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var view *jobViewForTest
	for _, v := range d.jobsList().Jobs {
		if v.Job.ID == "poller" {
			view = &jobViewForTest{fires: v.FiresPerNight, nightly: v.NightlyBatteryPct,
				perRun: v.Stats.BatteryPerRun}
		}
	}
	if view == nil {
		t.Fatal("job missing from jobsList")
	}
	if view.fires != 16 {
		t.Errorf("firings per night = %d, want 16 for a 30-minute job", view.fires)
	}
	if view.perRun <= 0 {
		t.Fatalf("no measured cost per run; the projection has nothing to stand on")
	}
	want := float64(view.fires) * view.perRun
	if view.nightly != want {
		t.Errorf("nightly = %.2f, want %.2f (%d firings x %.2f)",
			view.nightly, want, view.fires, view.perRun)
	}
}

// A job with no battery measurement must project nothing rather than zero:
// "0.0%" is the cheapest number in the column and would be attached to the one
// job nobody has evidence about.
func TestUnmeasuredJobProjectsNothing(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := d.store.Add(&model.Job{
		ID: "fresh", Name: "fresh", Schedule: "every 30m",
		Detection: model.DetectNone, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, v := range d.jobsList().Jobs {
		if v.Job.ID != "fresh" {
			continue
		}
		if v.FiresPerNight == 0 {
			t.Error("firings should still be known from the schedule alone")
		}
		if v.NightlyBatteryPct != 0 {
			t.Errorf("projected %.2f%% from no measurement", v.NightlyBatteryPct)
		}
	}
}

type jobViewForTest struct {
	fires   int
	nightly float64
	perRun  float64
}
