package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/scan"
)

type fireTimeObserver struct {
	scan.Provider
	next time.Time
	ok   bool
}

func (f *fireTimeObserver) Name() string    { return "fake" }
func (f *fireTimeObserver) Available() bool { return true }
func (f *fireTimeObserver) Where() string   { return "memory" }
func (f *fireTimeObserver) ObserveRun(context.Context, string) (scan.RunRecord, bool) {
	return scan.RunRecord{NextRun: f.next}, f.ok
}

// TestTheSchedulersOwnFireTimeWins guards a whole class of missed job.
//
// An interval scheduler counts from the *last* run, so "every 6 hours" moves
// every time a run is late. Parsing those same words as a fixed recurrence
// produces a time that never moves. The two agree at registration and then
// drift: observed live, goguma booked a wake for 05:36 for a job hermes
// actually ran at 08:46, nearly three hours after the Mac had been allowed
// back to sleep.
func TestTheSchedulersOwnFireTimeWins(t *testing.T) {
	d := testDaemon(t)
	now := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	// What the scheduler says: hours away from any fixed 6-hour grid.
	drifted := now.Add(7*time.Hour + 46*time.Minute)
	d.observers = map[string]scan.RunObserver{
		"fake": &fireTimeObserver{next: drifted, ok: true},
	}

	job := &model.Job{
		ID: "interval", Name: "interval", Source: "fake",
		Schedule: "0 */6 * * *", Enabled: true, Detection: model.DetectNone,
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}

	_, fire, _, found := d.nextWakeTarget(now, config.Default())
	if !found {
		t.Fatal("no wake target at all")
	}
	if !fire.Equal(drifted) {
		t.Errorf("fire = %s, want the scheduler's own %s, re-deriving it from "+
			"%q produces a time the scheduler stopped planning to use",
			fire, drifted, job.Schedule)
	}
}

// The wake is booked for the scheduler's own fire time, so the window must
// open for that same time. Opening from the parsed recurrence instead leaves
// the machine dark-woken with nothing holding sleep off: it re-sleeps and
// suspends the very run it woke for.
func TestTheWindowOpensForTheSchedulersFireTime(t *testing.T) {
	d := testDaemon(t)
	d.plat = &fakePlatform{}
	now := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	// The scheduler fires in one minute; the parsed 6-hour grid says 06:00.
	soon := now.Add(time.Minute)
	d.observers = map[string]scan.RunObserver{
		"fake": &fireTimeObserver{next: soon, ok: true},
	}
	job := &model.Job{
		ID: "interval", Name: "interval", Source: "fake",
		Schedule: "0 */6 * * *", Enabled: true, Detection: model.DetectNone,
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}

	d.openDueWindows(context.Background(), now, config.Default())

	d.mu.RLock()
	h, ok := d.holds["interval"]
	d.mu.RUnlock()
	if !ok {
		t.Fatal("no hold opened for the fire the wake was registered for; " +
			"the machine would re-sleep and suspend the run it woke for")
	}
	if !h.fireAt.Equal(soon) {
		t.Errorf("hold is for %s, want the scheduler's own %s", h.fireAt, soon)
	}
}

// goguma skip <job> must suppress the wake that is actually registered,
// which follows the scheduler's own fire time when there is one.
func TestSkipNextCoversTheSchedulersFireTime(t *testing.T) {
	d := testDaemon(t)
	drifted := time.Now().Add(3 * time.Hour)
	d.observers = map[string]scan.RunObserver{
		"fake": &fireTimeObserver{next: drifted, ok: true},
	}
	job := &model.Job{
		ID: "interval", Name: "interval", Source: "fake",
		Schedule: "0 */6 * * *", Enabled: true, Detection: model.DetectNone,
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}

	fire, err := d.SkipNext("interval")
	if err != nil {
		t.Fatal(err)
	}
	if !fire.Equal(drifted) {
		t.Errorf("skip reported %s, want the scheduler's own %s", fire, drifted)
	}
	if !d.isSkipped("interval", drifted) {
		t.Error("the wake registered for the scheduler's fire time is not suppressed; " +
			"the user was told it was skipped and it will still fire")
	}
}

// With no observer, or an unusable answer, the parsed schedule still governs.
// The fallback is what keeps crontab and launchd jobs working unchanged.
func TestAnUnusableSchedulerTimeFallsBackToTheSchedule(t *testing.T) {
	now := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)

	cases := map[string]*fireTimeObserver{
		"observer cannot read": {ok: false},
		"no next run recorded": {ok: true},
		"next run in the past": {ok: true, next: now.Add(-time.Hour)},
	}
	for name, obs := range cases {
		d := testDaemon(t)
		d.observers = map[string]scan.RunObserver{"fake": obs}
		job := &model.Job{
			ID: "j", Name: "j", Source: "fake",
			Schedule: "0 * * * *", Enabled: true, Detection: model.DetectNone,
		}
		if err := d.store.Add(job); err != nil {
			t.Fatal(err)
		}
		_, fire, _, found := d.nextWakeTarget(now, config.Default())
		if !found {
			t.Fatalf("%s: no wake target; the parsed schedule should still govern", name)
		}
		if !fire.Equal(now.Add(time.Hour)) {
			t.Errorf("%s: fire = %s, want the parsed 02:00", name, fire)
		}
	}
}
