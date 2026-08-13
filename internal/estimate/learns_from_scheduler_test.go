package estimate

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

func measuredRuns(n int, d time.Duration) []model.Run {
	out := make([]model.Run, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, model.Run{
			Outcome:  model.OutcomeOK,
			Duration: model.Duration(d),
			// Real runs always carry the job's mode; see hold.finish.
			Detection: model.DetectNone,
		})
	}
	return out
}

// History is keyed by name alone, so a re-added name with a different
// detection mode is a different job wearing the same slug. Inheriting an
// observed job's hour-long p95 into a wake-only hold would pin the machine
// awake for the hour, nightly, with nothing to ever correct it.
func TestAWakeOnlyJobIgnoresAForeignJobsHistory(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "db-dump", Detection: model.DetectNone}

	foreign := make([]model.Run, 0, 30)
	for i := 0; i < 30; i++ {
		foreign = append(foreign, model.Run{
			Outcome: model.OutcomeOK, Duration: model.Duration(time.Hour),
			Detection: model.DetectPattern,
		})
	}

	res := Compute(job, foreign, cfg)
	if res.Ceiling != cfg.WakeOnlyHold.D() {
		t.Errorf("ceiling %s inherited a foreign job's history; want the blind %s window",
			res.Ceiling, cfg.WakeOnlyHold.D())
	}
}

// TestAWakeOnlyJobLearnsOnceItIsMeasured is the payoff for reading a
// scheduler's own run records.
//
// A hermes job is registered DetectNone (no process exists to watch), but its
// scheduler records a real start, end and status for every run. Once those
// arrive the ceiling must follow them. Keying the fixed window off the declared
// mode meant a job with a shelf full of true durations kept a blind 3-minute
// guess forever, wasting the difference on every single run.
func TestAWakeOnlyJobLearnsOnceItIsMeasured(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "hermes-job", Detection: model.DetectNone}

	// Measured at 20s, well under the 3m wake-only window.
	res := Compute(job, measuredRuns(cfg.MinRunsForEstimate, 20*time.Second), cfg)

	if res.Ceiling >= cfg.WakeOnlyHold.D() {
		t.Fatalf("ceiling %s did not improve on the blind window %s despite %d measured runs",
			res.Ceiling, cfg.WakeOnlyHold.D(), cfg.MinRunsForEstimate)
	}
	if res.ColdStart {
		t.Error("reported cold start with a full set of measurements")
	}
	if res.P95 == 0 {
		t.Error("no p95 computed from measured runs")
	}
}

// Until there are enough measurements it must stay on the fixed window; one
// fast sample must not collapse the ceiling and start truncating the job.
func TestAWakeOnlyJobKeepsTheFixedWindowUntilItHasEnough(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "hermes-job", Detection: model.DetectNone}

	res := Compute(job, measuredRuns(cfg.MinRunsForEstimate-1, 20*time.Second), cfg)
	if res.Ceiling != cfg.WakeOnlyHold.D() {
		t.Errorf("ceiling = %s, want the fixed window %s until %d runs are in",
			res.Ceiling, cfg.WakeOnlyHold.D(), cfg.MinRunsForEstimate)
	}
}

// With nothing measured at all, behaviour is exactly as before.
func TestAnUnmeasuredWakeOnlyJobIsUnchanged(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "blind", Detection: model.DetectNone}

	res := Compute(job, nil, cfg)
	if res.Ceiling != cfg.WakeOnlyHold.D() {
		t.Errorf("ceiling = %s, want %s", res.Ceiling, cfg.WakeOnlyHold.D())
	}
	if res.Reason == "" {
		t.Error("no explanation offered for the fixed window")
	}
}

// An explicit override still wins over anything learned.
func TestAnExplicitMaxRuntimeStillWins(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "pinned", Detection: model.DetectNone, MaxRuntime: model.Duration(90 * time.Second)}

	res := Compute(job, measuredRuns(10, 5*time.Second), cfg)
	if res.Ceiling != 90*time.Second {
		t.Errorf("ceiling = %s, want the explicit 90s override", res.Ceiling)
	}
}
