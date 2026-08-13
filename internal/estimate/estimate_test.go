package estimate

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

func run(d time.Duration, outcome model.Outcome) model.Run {
	return model.Run{Duration: model.Duration(d), Outcome: outcome}
}

func okRuns(ds ...time.Duration) []model.Run {
	out := make([]model.Run, 0, len(ds))
	for _, d := range ds {
		out = append(out, run(d, model.OutcomeOK))
	}
	return out
}

func TestColdStartUsesDefaultCeiling(t *testing.T) {
	cfg := config.Default()

	// Below MinRunsForEstimate the learned ceiling is not trusted: one or two
	// fast runs would otherwise produce a confidently wrong, very tight cap.
	for n := 0; n < cfg.MinRunsForEstimate; n++ {
		got := Compute(nil, okRuns(repeat(2*time.Second, n)...), cfg)
		if !got.ColdStart {
			t.Errorf("with %d runs: expected cold start", n)
		}
		if got.Ceiling != cfg.DefaultCeiling.D() {
			t.Errorf("with %d runs: ceiling = %s, want the %s default",
				n, got.Ceiling, cfg.DefaultCeiling)
		}
	}
}

func TestCeilingConvergesOnRealRuntime(t *testing.T) {
	cfg := config.Default()

	// A job that consistently takes ~40s should end up with a ceiling near
	// 40s x 1.2, not stuck at the 5m cold-start default. This is the core
	// battery claim in the PRD, so it is asserted directly.
	runs := okRuns(38*time.Second, 41*time.Second, 39*time.Second,
		42*time.Second, 40*time.Second, 44*time.Second)

	got := Compute(nil, runs, cfg)
	if got.ColdStart {
		t.Fatal("expected the estimator to have left cold start")
	}
	if got.Ceiling >= cfg.DefaultCeiling.D() {
		t.Errorf("ceiling %s did not drop below the %s cold-start default",
			got.Ceiling, cfg.DefaultCeiling)
	}
	// p95 of these is 44s; x1.2 = 52.8s.
	if got.Ceiling < 45*time.Second || got.Ceiling > 60*time.Second {
		t.Errorf("ceiling = %s, expected roughly 53s (p95 44s x1.2)", got.Ceiling)
	}
	if got.Typical < 39*time.Second || got.Typical > 42*time.Second {
		t.Errorf("typical = %s, expected the median near 40s", got.Typical)
	}
}

func TestTruncatedRunsDoNotTrainTheEstimator(t *testing.T) {
	cfg := config.Default()

	// A run cut off at its ceiling is not evidence of how long the job takes;
	// it is evidence of how long the cap was. Feeding it back would ratchet
	// the ceiling upward and pin it there permanently.
	honest := okRuns(10*time.Second, 11*time.Second, 12*time.Second, 10*time.Second)
	baseline := Compute(nil, honest, cfg)

	polluted := append(append([]model.Run{}, honest...),
		run(5*time.Minute, model.OutcomeCeiling),
		run(5*time.Minute, model.OutcomeCutout),
		run(0, model.OutcomeNeverDetected),
	)
	got := Compute(nil, polluted, cfg)

	if got.Ceiling != baseline.Ceiling {
		t.Errorf("ceiling moved from %s to %s after adding truncated runs; "+
			"they must not train the estimator", baseline.Ceiling, got.Ceiling)
	}
}

func TestFailedRunsDoTrainTheEstimator(t *testing.T) {
	cfg := config.Default()

	// A job that ran to completion and exited non-zero still produced a real
	// duration measurement, so it should count.
	runs := []model.Run{
		run(20*time.Second, model.OutcomeOK),
		run(21*time.Second, model.OutcomeFailed),
		run(19*time.Second, model.OutcomeOK),
		run(20*time.Second, model.OutcomeFailed),
	}
	got := Compute(nil, runs, cfg)
	if got.Samples != 4 {
		t.Errorf("samples = %d, want 4 (failed runs carry real durations)", got.Samples)
	}
}

func TestExplicitMaxRuntimeWins(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{MaxRuntime: model.Duration(90 * time.Second)}

	// A value the user set explicitly must never be silently overridden by
	// the estimator, however much history exists.
	got := Compute(job, okRuns(repeat(time.Second, 50)...), cfg)
	if got.Ceiling != 90*time.Second {
		t.Errorf("ceiling = %s, want the explicit 90s override", got.Ceiling)
	}
}

func TestCeilingIsClamped(t *testing.T) {
	cfg := config.Default()

	t.Run("floor", func(t *testing.T) {
		// A job that always takes 200ms must not get a sub-second ceiling, or
		// one marginally slower run would be force-released.
		got := Compute(nil, okRuns(repeat(200*time.Millisecond, 10)...), cfg)
		if got.Ceiling != cfg.MinCeiling.D() {
			t.Errorf("ceiling = %s, want the %s floor", got.Ceiling, cfg.MinCeiling)
		}
	})

	t.Run("cap", func(t *testing.T) {
		got := Compute(nil, okRuns(repeat(10*time.Hour, 10)...), cfg)
		if got.Ceiling != cfg.MaxCeiling.D() {
			t.Errorf("ceiling = %s, want the %s cap", got.Ceiling, cfg.MaxCeiling)
		}
	})
}

func TestOnlyRecentHistoryIsConsidered(t *testing.T) {
	cfg := config.Default()

	// A job that legitimately got slower should converge on its new duration
	// rather than being held to a stale baseline.
	var runs []model.Run
	runs = append(runs, okRuns(repeat(time.Second, 40)...)...)
	runs = append(runs, okRuns(repeat(60*time.Second, cfg.HistoryWindow)...)...)

	got := Compute(nil, runs, cfg)
	if got.Typical < 55*time.Second {
		t.Errorf("typical = %s, expected it to track the recent 60s runs, not the old 1s ones",
			got.Typical)
	}
}

func TestSummarizeAveragesBatteryPerRun(t *testing.T) {
	cfg := config.Default()

	// What the job cost, from two real battery readings per run. Runs on AC
	// contribute nothing because they cost nothing, a job that only ever
	// fires while plugged in is genuinely free.
	runs := []model.Run{
		{Outcome: model.OutcomeOK, BatteryStart: 80, BatteryEnd: 78}, // 2%
		{Outcome: model.OutcomeOK, BatteryStart: 78, BatteryEnd: 77}, // 1%
		{Outcome: model.OutcomeOK, BatteryStart: -1, BatteryEnd: -1}, // on AC
		{Outcome: model.OutcomeOK, BatteryStart: 60, BatteryEnd: 62}, // charging
	}
	st := Summarize(&model.Job{ID: "x"}, runs, cfg)

	// 2% + 1% + 0% (charging) over the three runs measured on battery = 1.0%
	// per run. The AC run is excluded from both halves: counting it in the
	// denominator would report a cheaper job for having been plugged in.
	if st.BatteryPerRun != 1.0 {
		t.Errorf("per run = %.2f%%, want 1.00%% (3%% over 3 battery runs, AC excluded)",
			st.BatteryPerRun)
	}
}

// A charging run must never read as a negative cost, and an unmeasured one
// must never be confused with a free one.
func TestDrainedIsNeverNegativeOrInvented(t *testing.T) {
	if d := (model.Run{BatteryStart: 50, BatteryEnd: 60}).Drained(); d != 0 {
		t.Errorf("charging produced a drain of %d", d)
	}
	if d := (model.Run{BatteryStart: -1, BatteryEnd: 40}).Drained(); d != 0 {
		t.Errorf("half-measured run produced a drain of %d", d)
	}
	if r := (model.Run{BatteryStart: -1, BatteryEnd: -1}); r.MeasuredOnBattery() {
		t.Error("an AC run claimed to be measured on battery")
	}
	if d := (model.Run{BatteryStart: 40, BatteryEnd: 37}).Drained(); d != 3 {
		t.Errorf("drain = %d, want 3", d)
	}
}

func TestSummarizeCountsProblemOutcomes(t *testing.T) {
	cfg := config.Default()
	runs := []model.Run{
		run(time.Second, model.OutcomeOK),
		run(time.Second, model.OutcomeFailed),
		run(time.Second, model.OutcomeCeiling),
		run(0, model.OutcomeNeverDetected),
		run(0, model.OutcomeNeverDetected),
	}
	st := Summarize(&model.Job{ID: "x"}, runs, cfg)

	if st.Failures != 1 || st.CeilingHits != 1 || st.NeverSeen != 2 {
		t.Errorf("failures=%d ceiling=%d neverSeen=%d, want 1/1/2",
			st.Failures, st.CeilingHits, st.NeverSeen)
	}
}

func TestPercentileUsesObservedValues(t *testing.T) {
	in := []time.Duration{
		1 * time.Second, 2 * time.Second, 3 * time.Second,
		4 * time.Second, 100 * time.Second,
	}
	// Nearest-rank always returns a duration that actually occurred, which
	// makes the resulting ceiling explainable.
	if got := percentile(in, 0.95); got != 100*time.Second {
		t.Errorf("p95 = %s, want 100s", got)
	}
	if got := percentile(in, 0.5); got != 3*time.Second {
		t.Errorf("p50 = %s, want 3s", got)
	}
	if got := percentile(nil, 0.95); got != 0 {
		t.Errorf("p95 of nothing = %s, want 0", got)
	}
}

func repeat(d time.Duration, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = d
	}
	return out
}
