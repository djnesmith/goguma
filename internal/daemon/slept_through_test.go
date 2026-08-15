package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/schedule"
)

// A fire the machine slept through used to leave no trace at all: the history
// showed 11:00, 15:00 and 20:00 and simply no 06:00, and the only way to spot
// it was to know the schedule and count rows.
func TestAFireSleptThroughIsRecorded(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 10, 0, 0, 0, 0, time.Local)
	if err := d.store.Add(&model.Job{
		ID: "sync", Name: "sync", Schedule: "0 6 * * *",
		Detection: model.DetectNone, Enabled: true, CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}

	// Asleep from midnight to 09:00, straight through the 06:00 fire.
	sleep := time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local)
	wake := time.Date(2026, 8, 14, 9, 0, 0, 0, time.Local)
	d.recordSleptThrough(schedule.SleepInterval{Sleep: sleep, Wake: wake}, wake)

	runs, err := d.store.Runs("sync")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("recorded %d runs, want 1 for the fire that was slept through", len(runs))
	}
	if runs[0].Outcome != model.OutcomeSlept {
		t.Errorf("outcome = %q, want %q", runs[0].Outcome, model.OutcomeSlept)
	}
	if h := runs[0].WindowOpened.Hour(); h != 6 {
		t.Errorf("recorded against %02d:00, want the 06:00 fire", h)
	}
}

// Overlapping sleep intervals are normal across a suspend/resume cycle, so
// the same miss must not be written twice and read as two.
func TestASleptFireIsNotRecordedTwice(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 10, 0, 0, 0, 0, time.Local)
	if err := d.store.Add(&model.Job{
		ID: "sync", Name: "sync", Schedule: "0 6 * * *",
		Detection: model.DetectNone, Enabled: true, CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	sleep := time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local)
	wake := time.Date(2026, 8, 14, 9, 0, 0, 0, time.Local)

	d.recordSleptThrough(schedule.SleepInterval{Sleep: sleep, Wake: wake}, wake)
	d.recordSleptThrough(schedule.SleepInterval{Sleep: sleep, Wake: wake}, wake)

	runs, _ := d.store.Runs("sync")
	if len(runs) != 1 {
		t.Errorf("recorded %d runs for one missed fire", len(runs))
	}
}

// A job registered this morning did not miss last night.
func TestFiresBeforeAJobExistedAreNotItsMisses(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	// Created after the fire it would otherwise be blamed for.
	created := time.Date(2026, 8, 14, 8, 0, 0, 0, time.Local)
	if err := d.store.Add(&model.Job{
		ID: "fresh", Name: "fresh", Schedule: "0 6 * * *",
		Detection: model.DetectNone, Enabled: true, CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	sleep := time.Date(2026, 8, 14, 0, 0, 0, 0, time.Local)
	wake := time.Date(2026, 8, 14, 9, 0, 0, 0, time.Local)
	d.recordSleptThrough(schedule.SleepInterval{Sleep: sleep, Wake: wake}, wake)

	if runs, _ := d.store.Runs("fresh"); len(runs) != 0 {
		t.Errorf("blamed a job for %d fires that predate it", len(runs))
	}
}
