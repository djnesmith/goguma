package daemon

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// TestARunningJobIsNotCutOffAtItsCeiling guards the failure that made a slow
// job permanently slow.
//
// The ceiling is a guess at how long a job takes. A job still running when it
// arrives has disproved the guess, not misbehaved. Releasing there freed the
// machine to sleep under a healthy job *and* discarded the run, truncated
// durations cannot train the estimator, so the next run inherited the same
// wrong ceiling and was cut off in exactly the same place, indefinitely.
func TestARunningJobIsNotCutOffAtItsCeiling(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	cfg := config.Default()

	job := &model.Job{ID: "slow", Name: "slow", Detection: model.DetectPattern}
	h := &hold{
		job:       job,
		fireAt:    now,
		openedAt:  now,
		ceiling:   5 * time.Minute,
		detected:  true,
		startedAt: now,
		pid:       4242,
	}
	d.mu.Lock()
	d.holds[job.ID] = h
	d.mu.Unlock()

	// Two minutes past a five-minute ceiling: the old behaviour released here.
	d.enforceCeilings(now.Add(7*time.Minute), cfg)
	d.mu.RLock()
	surviving := len(d.holds)
	d.mu.RUnlock()
	if surviving != 1 {
		t.Fatal("a job still running was released at its ceiling; the machine " +
			"is now free to sleep under it and the run teaches the estimator nothing")
	}

	// The backstop still exists. Past MaxCeiling the job is stuck, not slow.
	d.enforceCeilings(now.Add(cfg.MaxCeiling.D()+time.Minute), cfg)
	d.mu.RLock()
	surviving = len(d.holds)
	d.mu.RUnlock()
	if surviving != 0 {
		t.Errorf("%d holds survived the %s backstop", surviving, cfg.MaxCeiling.D())
	}
	d.bg.Wait()
}

// TestAWakeOnlyWindowStillEndsOnTime pins the other half: a job that cannot be
// observed has no "still running" to check, so its fixed window must still
// close exactly when it always did.
func TestAWakeOnlyWindowStillEndsOnTime(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	cfg := config.Default()

	job := &model.Job{ID: "blind", Name: "blind", Detection: model.DetectNone}
	d.mu.Lock()
	d.holds[job.ID] = &hold{
		job: job, fireAt: now, openedAt: now, ceiling: 3 * time.Minute,
	}
	d.mu.Unlock()

	d.enforceCeilings(now.Add(3*time.Minute+time.Second), cfg)
	d.mu.RLock()
	surviving := len(d.holds)
	d.mu.RUnlock()
	if surviving != 0 {
		t.Error("a wake-only window outlived its fixed period")
	}
	d.bg.Wait()
}

// TestAnObservedWakeOnlyJobIsNotCutOffAtItsCeiling guards the interaction
// between two features that were built separately.
//
// A hermes job is registered DetectNone because no process exists to watch,
// so `enforceCeilings` classified it as wake-only and closed its window on the
// timer. That was right until the scheduler-state observer arrived, which can
// see such a job running. The wake-only branch ran first and released anyway,
// throwing away the measurement and the protection at the same moment.
func TestAnObservedWakeOnlyJobIsNotCutOffAtItsCeiling(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)
	cfg := config.Default()

	job := &model.Job{ID: "hermes-job", Name: "hermes job", Detection: model.DetectNone}
	h := &hold{
		job: job, fireAt: now, openedAt: now, ceiling: 3 * time.Minute,
		// The observer saw the scheduler report it running.
		detected: true, startedAt: now,
	}
	d.mu.Lock()
	d.holds[job.ID] = h
	d.mu.Unlock()

	d.enforceCeilings(now.Add(5*time.Minute), cfg)

	d.mu.RLock()
	surviving := len(d.holds)
	d.mu.RUnlock()
	if surviving != 1 {
		t.Fatal("an observed job was released at its wake-only ceiling while still running")
	}
	d.bg.Wait()
}

// The unobserved case must be untouched: with nothing reporting it, the fixed
// window is still exactly right and must still close on time.
func TestAnUnobservedWakeOnlyJobStillClosesOnTime(t *testing.T) {
	d, _ := keepAwakeDaemon(t)
	now := time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)

	job := &model.Job{ID: "blind", Name: "blind", Detection: model.DetectNone}
	d.mu.Lock()
	d.holds[job.ID] = &hold{job: job, fireAt: now, openedAt: now, ceiling: 3 * time.Minute}
	d.mu.Unlock()

	d.enforceCeilings(now.Add(3*time.Minute+time.Second), config.Default())

	d.mu.RLock()
	surviving := len(d.holds)
	d.mu.RUnlock()
	if surviving != 0 {
		t.Error("an unobserved wake-only window outlived its fixed period")
	}
	d.bg.Wait()
}
