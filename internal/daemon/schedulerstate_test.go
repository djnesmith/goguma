package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/scan"
)

// fakeObserver stands in for a scheduler that keeps its own run records.
type fakeObserver struct {
	scan.Provider
	rec RunFixture
}

type RunFixture struct {
	rec scan.RunRecord
	ok  bool
}

func (f *fakeObserver) Name() string    { return "fake" }
func (f *fakeObserver) Available() bool { return true }
func (f *fakeObserver) Where() string   { return "memory" }
func (f *fakeObserver) ObserveRun(context.Context, string) (scan.RunRecord, bool) {
	return f.rec.rec, f.rec.ok
}

func daemonObserving(t *testing.T, fx RunFixture) (*Daemon, *hold, time.Time) {
	t.Helper()
	d := testDaemon(t)
	d.observers = map[string]scan.RunObserver{"fake": &fakeObserver{rec: fx}}

	opened := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	h := &hold{
		job: &model.Job{
			ID: "sync", Name: "sync", Source: "fake", Detection: model.DetectNone,
		},
		fireAt: opened, openedAt: opened, ceiling: 3 * time.Minute,
	}
	d.mu.Lock()
	d.holds[h.job.ID] = h
	d.mu.Unlock()
	return d, h, opened
}

// A job the scheduler says is running must be treated as started, so the
// ceiling extends around it instead of cutting it off.
func TestSchedulerReportedStartMarksTheJobRunning(t *testing.T) {
	start := time.Date(2026, 8, 6, 15, 0, 30, 0, time.UTC)
	d, h, opened := daemonObserving(t, RunFixture{
		rec: scan.RunRecord{Running: true, StartedAt: start},
		ok:  true,
	})
	d.pollSchedulerState(context.Background(), opened.Add(time.Minute))

	d.mu.RLock()
	defer d.mu.RUnlock()
	if !h.detected {
		t.Fatal("a job the scheduler reports as running was not marked detected")
	}
	if !h.startedAt.Equal(start) {
		t.Errorf("startedAt = %s, want the scheduler's own %s", h.startedAt, start)
	}
	if len(d.holds) != 1 {
		t.Error("the hold was released while the job was still running")
	}
}

// The whole point: a completion record ends the hold early, with a real
// duration, instead of waiting out a fixed window.
func TestSchedulerReportedFinishReleasesWithARealDuration(t *testing.T) {
	start := time.Date(2026, 8, 6, 15, 0, 30, 0, time.UTC)
	done := start.Add(20 * time.Second)
	d, h, opened := daemonObserving(t, RunFixture{
		rec: scan.RunRecord{Running: true, StartedAt: start},
		ok:  true,
	})
	d.pollSchedulerState(context.Background(), opened.Add(30*time.Second))

	// Now the scheduler reports it finished.
	d.observers["fake"] = &fakeObserver{rec: RunFixture{
		rec: scan.RunRecord{Running: false, StartedAt: start, CompletedAt: done, Status: "ok"},
		ok:  true,
	}}
	// Noticed a poll later than it actually happened.
	d.pollSchedulerState(context.Background(), done.Add(5*time.Second))

	d.mu.RLock()
	remaining := len(d.holds)
	d.mu.RUnlock()
	if remaining != 0 {
		t.Fatal("a finished job kept holding sleep off; this is the waste the feature exists to remove")
	}

	d.bg.Wait() // the run record is persisted off the hot path
	runs, _ := d.store.Runs(h.job.ID)
	if len(runs) == 0 {
		t.Fatal("no run was recorded")
	}
	r := runs[len(runs)-1]
	if got := r.Duration.D(); got != 20*time.Second {
		t.Errorf("recorded duration %s, want 20s; the scheduler's own start and end, "+
			"not the moment the daemon happened to look", got)
	}
	if r.Outcome != model.OutcomeOK {
		t.Errorf("outcome = %q, want ok", r.Outcome)
	}
}

// A stale completion from the *previous* run must not close the window the
// moment it opens.
func TestAnOldCompletionDoesNotCloseANewWindow(t *testing.T) {
	opened := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	stale := opened.Add(-6 * time.Hour)
	d, _, _ := daemonObserving(t, RunFixture{
		rec: scan.RunRecord{Running: false, CompletedAt: stale, Status: "ok"},
		ok:  true,
	})
	d.pollSchedulerState(context.Background(), opened.Add(10*time.Second))

	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.holds) != 1 {
		t.Fatal("a completion from the previous run closed this one's window immediately")
	}
}

// The degradation contract, at the level that matters: an observer that cannot
// read its file changes nothing at all.
func TestUnreadableSchedulerStateLeavesTheFixedWindowAlone(t *testing.T) {
	d, h, opened := daemonObserving(t, RunFixture{ok: false})
	d.pollSchedulerState(context.Background(), opened.Add(time.Minute))

	d.mu.RLock()
	defer d.mu.RUnlock()
	if h.detected {
		t.Error("claimed detection from an observer that reported failure")
	}
	if len(d.holds) != 1 {
		t.Error("released a hold on the strength of an unreadable file")
	}
}

// A failed run must be recorded as failed, not quietly as ok.
func TestSchedulerReportedFailureIsRecordedAsAFailure(t *testing.T) {
	start := time.Date(2026, 8, 6, 15, 0, 10, 0, time.UTC)
	done := start.Add(time.Minute)
	d, h, opened := daemonObserving(t, RunFixture{
		rec: scan.RunRecord{Running: false, StartedAt: start, CompletedAt: done, Status: "error"},
		ok:  true,
	})
	d.pollSchedulerState(context.Background(), opened.Add(2*time.Minute))

	d.bg.Wait() // the run record is persisted off the hot path
	runs, _ := d.store.Runs(h.job.ID)
	if len(runs) == 0 {
		t.Fatal("no run recorded")
	}
	if got := runs[len(runs)-1].Outcome; got != model.OutcomeFailed {
		t.Errorf("outcome = %q, want failed", got)
	}
}

// TestObserverKeyMatchesTheSourceAdoptionStores closes the seam between the
// two halves of this feature.
//
// The observer map is keyed on Provider.Name(); a hold looks itself up by
// job.Source, which adoption sets from Candidate.Source. If those two strings
// ever drift apart the lookup silently misses, every scheduler-reported job
// falls back to a fixed window, and nothing anywhere reports a problem; the
// feature would simply never fire. Unit tests using a fake observer cannot
// catch that, because they choose both sides of the key themselves.
func TestObserverKeyMatchesTheSourceAdoptionStores(t *testing.T) {
	for _, o := range scan.Observers() {
		if o.Name() == "" {
			t.Fatal("an observer with no name can never be matched to a job")
		}
		// Adoption stores string(Candidate.Source); Providers report Name().
		// For every observer, some known Source must equal its Name.
		var matched bool
		for _, s := range []scan.Source{
			scan.SourceCrontab, scan.SourceLaunchd, scan.SourceSystemd, scan.SourceHermes,
		} {
			if string(s) == o.Name() {
				matched = true
			}
		}
		if !matched {
			t.Errorf("observer %q matches no scan.Source, so no adopted job can ever use it",
				o.Name())
		}
	}
}

// And the map the daemon builds must be keyed the same way.
func TestResolveObserversIsKeyedByName(t *testing.T) {
	m := resolveObservers()
	for name, o := range m {
		if name != o.Name() {
			t.Errorf("observer map key %q does not match Name() %q", name, o.Name())
		}
	}
}

// TestAnInferredStartMeasuresFromTheFireTimeNotTheWindow guards an
// over-measurement found in live testing, not in a unit test.
//
// Hermes stamps `fire_claim` only for externally-dispatched runs, so a job
// fired by its own in-process ticker is never seen running; it goes from idle
// to complete between two polls, and the start has to be inferred. Inferring
// it as the window opening charges every run the full wake buffer: a real
// 17-second job was recorded as 2m25s, and three of those would have taught
// the estimator a ceiling of nearly the whole window.
func TestAnInferredStartMeasuresFromTheFireTimeNotTheWindow(t *testing.T) {
	opened := time.Date(2026, 8, 6, 22, 23, 0, 0, time.UTC)
	fire := opened.Add(2 * time.Minute) // the wake buffer
	done := fire.Add(17 * time.Second)

	d := testDaemon(t)
	d.observers = map[string]scan.RunObserver{"fake": &fakeObserver{rec: RunFixture{
		// Never seen running: no claim, only a completion.
		rec: scan.RunRecord{Running: false, CompletedAt: done, Status: "ok"},
		ok:  true,
	}}}
	job := &model.Job{ID: "ticker", Name: "ticker", Source: "fake", Detection: model.DetectNone}
	h := &hold{job: job, fireAt: fire, openedAt: opened, ceiling: 3 * time.Minute}
	d.mu.Lock()
	d.holds[job.ID] = h
	d.mu.Unlock()

	d.pollSchedulerState(context.Background(), done.Add(2*time.Second))
	d.bg.Wait()

	runs, _ := d.store.Runs(job.ID)
	if len(runs) == 0 {
		t.Fatal("no run recorded")
	}
	got := runs[len(runs)-1].Duration.D()
	if got != 17*time.Second {
		t.Errorf("recorded %s, want 17s; measuring from the fire time, not the window "+
			"opening (which would add the whole %s wake buffer)", got, fire.Sub(opened))
	}
}

// A fire that was already held for and completed must not be re-opened while
// `now` is still inside its buffer window. Re-opening held the machine awake
// for a job that just finished and recorded a phantom never_detected run for
// every real run.
func TestAServedFireIsNotReopenedInsideItsBuffer(t *testing.T) {
	d := testDaemon(t)
	d.plat = &fakePlatform{}
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	fire := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	job := &model.Job{
		ID: "backup", Name: "backup", Schedule: "0 3 * * *",
		Enabled: true, Detection: model.DetectPattern, Match: "rsync",
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}

	// The window for the 03:00 fire opened, served the job, and closed.
	h := &hold{job: job, fireAt: fire, openedAt: fire.Add(-90 * time.Second), ceiling: time.Minute}
	d.mu.Lock()
	d.holds[job.ID] = h
	d.finishHoldLocked(h, fire.Add(50*time.Second), model.OutcomeOK)
	d.mu.Unlock()
	d.bg.Wait() // the run record is persisted off the hot path

	// 55 seconds after the fire, still inside the buffer look-back.
	d.openDueWindows(context.Background(), fire.Add(55*time.Second), config.Default())

	d.mu.RLock()
	defer d.mu.RUnlock()
	if _, reopened := d.holds[job.ID]; reopened {
		t.Fatal("a completed fire was re-opened inside its buffer; every real run " +
			"would gain a phantom never_detected companion")
	}
}

// An explicit --max-runtime must actually release a detected job. The
// still-running extension exists for learned estimates; ignoring a cap the
// user typed made the ceiling-hit warning's own suggested fix a no-op.
func TestAnExplicitMaxRuntimeIsEnforcedOnADetectedJob(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	h := &hold{
		job: &model.Job{
			ID: "idx", Name: "idx", Detection: model.DetectPattern, Match: "rebuild",
			MaxRuntime: model.Duration(20 * time.Minute),
		},
		fireAt: start, openedAt: start,
		ceiling: 20 * time.Minute, detected: true, startedAt: start,
	}
	d.mu.Lock()
	d.holds["idx"] = h
	d.mu.Unlock()

	d.enforceCeilings(start.Add(21*time.Minute), config.Default())

	d.mu.RLock()
	_, held := d.holds["idx"]
	d.mu.RUnlock()
	if held {
		t.Fatal("a job 1m past its explicit --max-runtime is still holding the machine awake")
	}
	d.bg.Wait()
	runs, _ := d.store.Runs("idx")
	if len(runs) == 0 || runs[len(runs)-1].Outcome != model.OutcomeCeiling {
		t.Error("the explicit cut-off was not recorded as a ceiling hit")
	}
}

// A wake-only run that outgrows its window finishes after the release. The
// scheduler's completion record is the only evidence the ceiling is too
// small; without learning from it the estimator is stuck and the machine
// sleeps out from under the job identically forever.
func TestALateCompletionTeachesTheEstimator(t *testing.T) {
	d := testDaemon(t)
	if err := d.store.Load(); err != nil {
		t.Fatal(err)
	}
	opened := time.Date(2026, 8, 6, 2, 58, 30, 0, time.UTC)
	fired := opened.Add(90 * time.Second)
	job := &model.Job{
		ID: "gmail-sync", Name: "gmail-sync", Source: "fake",
		Schedule: "0 3 * * *", Enabled: true, Detection: model.DetectNone,
	}
	if err := d.store.Add(job); err != nil {
		t.Fatal(err)
	}
	// Stale observer state: last completion long before this window.
	d.observers = map[string]scan.RunObserver{"fake": &fakeObserver{rec: RunFixture{
		ok: true, rec: scan.RunRecord{CompletedAt: fired.Add(-time.Hour)},
	}}}
	d.mu.Lock()
	d.holds[job.ID] = &hold{job: job, fireAt: fired, openedAt: opened, ceiling: 30 * time.Second}
	d.mu.Unlock()

	// The window expires with the job unseen and closes as ok.
	d.enforceCeilings(fired.Add(31*time.Second), config.Default())
	d.mu.RLock()
	_, held := d.holds[job.ID]
	d.mu.RUnlock()
	if held {
		t.Fatal("the wake-only window did not close at its ceiling")
	}

	// Four minutes after the fire, the scheduler records the real completion.
	done := fired.Add(4 * time.Minute)
	d.observers["fake"] = &fakeObserver{rec: RunFixture{
		ok: true, rec: scan.RunRecord{StartedAt: fired, CompletedAt: done, Status: "ok"},
	}}
	d.pollSchedulerState(context.Background(), done.Add(2*time.Second))
	d.bg.Wait()

	runs, _ := d.store.Runs(job.ID)
	var learned time.Duration
	for _, r := range runs {
		if r.Duration.D() > learned {
			learned = r.Duration.D()
		}
	}
	if learned != 4*time.Minute {
		t.Errorf("learned %s, want the real 4m; the estimator has no path back up", learned)
	}
}
