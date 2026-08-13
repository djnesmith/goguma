// Package estimate turns a job's run history into the ceiling the daemon
// enforces on its next run.
//
// The ceiling is a safety valve, not a prediction. Process-exit detection is
// what normally ends a hold: the instant the job exits, sleep is allowed
// again. The ceiling only matters when that signal never arrives (a hung job,
// or a match pattern that stopped matching), and its job is to stop goguma
// holding a machine awake indefinitely. That framing is why it is set from a
// high percentile rather than an average: it should sit above essentially
// every real run, and be reached only when something is wrong.
package estimate

import (
	"math"
	"sort"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// Result is a computed ceiling plus the evidence behind it, so the CLI can
// explain *why* a job has the ceiling it does instead of showing a bare
// number the user has no way to reason about.
type Result struct {
	Ceiling   time.Duration
	Typical   time.Duration
	P95       time.Duration
	Samples   int
	ColdStart bool
	// Reason is a short human explanation ("p95 of 12 runs x1.2").
	Reason string
}

// Compute derives the ceiling for a job from its history.
//
// An explicit MaxRuntime on the job always wins: it is documented as an
// override, and silently ignoring a value the user set would be worse than
// any estimate.
func Compute(job *model.Job, runs []model.Run, cfg config.Config) Result {
	// History survives Remove so the same job coming back keeps its learned
	// ceiling. But history is keyed by name alone, and a re-added name with a
	// different detection mode is a different job wearing the same slug. For
	// an observable job a foreign ceiling self-corrects (the still-running
	// extension holds, the real duration is recorded); a wake-only hold burns
	// its entire ceiling every run, so inheriting an old observed job's
	// hour-long p95 would pin the machine awake for the hour, nightly, with
	// nothing to ever correct it. Unobservable jobs therefore learn only from
	// runs recorded under their own mode.
	if job != nil && !job.Detection.Observable() {
		same := make([]model.Run, 0, len(runs))
		for _, r := range runs {
			if r.Detection == job.Detection {
				same = append(same, r)
			}
		}
		runs = same
	}

	// A wake-only job gets a fixed window *only while nothing has measured it*.
	//
	// This used to key off the declared detection mode alone, which was right
	// when the mode was the only thing that decided observability. It is not
	// any more: a scheduler that keeps its own run records (hermes writes a
	// completion timestamp and a status for every cron job) gives real
	// durations for a job registered DetectNone. Refusing to learn from them
	// because of the label would have thrown away the entire point of reading
	// those records: the ceiling would stay a fixed guess forever while the
	// history sitting underneath it held the true answer.
	//
	// So the question is now "is there anything to learn from", not "what does
	// this job call itself".
	if job != nil && !job.Detection.Observable() && !hasUsableHistory(runs, cfg) {
		// Says what happens, not what the mode is called. "The job cannot
		// be observed" describes goguma's predicament; what the user needs
		// is that the Mac stays up for a set time whether the job takes
		// that long or not. When the window's length came from the user's
		// own flag, the reason must say so: `list --explain` exists to
		// answer "why is this window 10m", and describing the override with
		// the generic fixed-window prose named the wrong mechanism.
		if job.MaxRuntime > 0 {
			return Result{
				Ceiling: job.MaxRuntime.D(),
				Reason: "set explicitly with --max-runtime; goguma can't tell when " +
					"this job finishes, so the Mac stays awake for the whole window",
			}
		}
		return Result{
			Ceiling: cfg.WakeOnlyHold.D(),
			Reason: "goguma can't tell when this job starts or finishes, " +
				"so the Mac stays awake for a set time rather than until the job is done",
		}
	}

	if job != nil && job.MaxRuntime > 0 {
		// The override wins, but the measured statistics still describe the
		// job: leaving P95 zero here made `history` print "p95 0s" over ten
		// completed runs, as if the job had never been measured.
		durs := completedDurations(runs)
		return Result{
			Ceiling: job.MaxRuntime.D(),
			Typical: medianOf(durs),
			P95:     percentile(durs, 0.95),
			Samples: len(runs),
			Reason:  "set explicitly with --max-runtime",
		}
	}

	durs := completedDurations(runs)
	// Only the most recent window is considered, so a job that legitimately
	// got slower (bigger backup, more data) converges to its new duration
	// instead of being held to a year-old baseline.
	if len(durs) > cfg.HistoryWindow {
		durs = durs[len(durs)-cfg.HistoryWindow:]
	}

	res := Result{Samples: len(durs)}
	if len(durs) > 0 {
		res.Typical = medianOf(durs)
		res.P95 = percentile(durs, 0.95)
	}

	if len(durs) < cfg.MinRunsForEstimate {
		res.ColdStart = true
		res.Ceiling = cfg.DefaultCeiling.D()
		res.Reason = coldStartReason(len(durs), cfg.MinRunsForEstimate)
		return res
	}

	raw := time.Duration(float64(res.P95) * cfg.CeilingMultiplier)
	res.Ceiling = clamp(raw, cfg.MinCeiling.D(), cfg.MaxCeiling.D())
	res.Reason = reasonFor(res, raw, cfg)
	return res
}

func coldStartReason(have, need int) string {
	if have == 0 {
		return "cold start: no completed runs yet"
	}
	return "cold start: " + plural(have, "run") + " of " + itoa(need) + " needed"
}

func reasonFor(res Result, raw time.Duration, cfg config.Config) string {
	base := "p95 of " + plural(res.Samples, "run") + " ×" + trimFloat(cfg.CeilingMultiplier)
	switch {
	case raw < cfg.MinCeiling.D():
		return base + ", raised to the " + model.HumanDuration(cfg.MinCeiling.D()) + " minimum"
	case raw > cfg.MaxCeiling.D():
		return base + ", capped at the " + model.HumanDuration(cfg.MaxCeiling.D()) + " maximum"
	default:
		return base
	}
}

// completedDurations extracts the runtimes that legitimately describe how
// long this job takes, in chronological order.
//
// Truncated runs are excluded deliberately. A run that hit its ceiling was
// cut short *by the ceiling*, so feeding its duration back in would teach the
// estimator that the job takes exactly as long as the cap, a feedback loop
// where one hung run ratchets the ceiling up and pins it there. Cutouts are
// excluded for the same reason.
func completedDurations(runs []model.Run) []time.Duration {
	out := make([]time.Duration, 0, len(runs))
	for _, r := range runs {
		if !r.Outcome.TrainsEstimator() {
			continue
		}
		if d := r.Duration.D(); d > 0 {
			out = append(out, d)
		}
	}
	return out
}

// percentile returns the p-th percentile using nearest-rank, which for the
// small samples here (typically under 20 runs) is more predictable than
// interpolation: the result is always an actually-observed duration.
func percentile(in []time.Duration, p float64) time.Duration {
	if len(in) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(p * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1]
}

func medianOf(in []time.Duration) time.Duration {
	if len(in) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

func clamp(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Summarize rolls a job's history into the stats shown by `list`, `history`,
// and the GUI.
func Summarize(job *model.Job, runs []model.Run, cfg config.Config) model.Stats {
	res := Compute(job, runs, cfg)
	st := model.Stats{
		Runs:      len(runs),
		Typical:   model.Duration(res.Typical),
		P95:       model.Duration(res.P95),
		Ceiling:   model.Duration(res.Ceiling),
		ColdStart: res.ColdStart,
	}
	if job != nil {
		st.JobID = job.ID
	}

	var drained, onBattery int
	for i := range runs {
		r := &runs[i]
		switch r.Outcome {
		case model.OutcomeFailed:
			st.Failures++
		case model.OutcomeCeiling:
			st.CeilingHits++
		case model.OutcomeNeverDetected:
			st.NeverSeen++
		}
		// What the job actually cost, from two real battery readings rather
		// than a duration standing in for one. Only runs on battery can be
		// counted, and only those form the denominator.
		if r.MeasuredOnBattery() {
			drained += r.Drained()
			onBattery++
		}
	}
	if onBattery > 0 {
		st.BatteryPerRun = float64(drained) / float64(onBattery)
	}

	if len(runs) > 0 {
		last := runs[len(runs)-1]
		st.LastRun = &last
		const recentN = 20
		start := max(len(runs)-recentN, 0)
		st.Recent = runs[start:]
	}
	return st
}

// hasUsableHistory reports whether enough runs carry a real measured duration
// for an estimate to mean anything.
//
// Deliberately the same threshold the learned path uses. A wake-only job that
// has just started being measured should not jump straight from a fixed window
// to a ceiling derived from one sample; that is the overconfidence
// MinRunsForEstimate exists to prevent, and it applies no differently when the
// measurements arrive from a scheduler's file rather than from a process.
func hasUsableHistory(runs []model.Run, cfg config.Config) bool {
	return len(completedDurations(runs)) >= cfg.MinRunsForEstimate
}
