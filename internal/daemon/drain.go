package daemon

import (
	"github.com/junnam586/goguma/internal/model"
)

// drainSamplesConsidered is how far back to look for battery-run evidence.
//
// The same window the duration estimator uses, for the same reason: a job's
// cost changes when the job changes, and evidence from a fortnight ago is
// describing a program that may no longer exist.
const drainSamplesConsidered = 20

// minDrainSamples is how many battery runs are needed before the measurement
// is trusted at all.
//
// Two, not one. A single run can be a percent purely because the reading
// crossed a boundary while the job was running: the battery was at 61.6% and
// reported 62 at the start and 61 at the end, having spent almost nothing. Two
// agreeing samples is a low bar, but it is the difference between measuring a
// job and measuring a rounding edge.
const minDrainSamples = 2

// expectedDrainPct is how much battery a job has actually been consuming,
// in whole percent, or -1 when there is nothing to read.
//
// Only runs recorded on battery carry a figure at all; on AC both endpoints
// are -1, because there is no drain to measure and inventing one from elapsed
// time would produce a number that looks measured and is not.
//
// Returns the largest recent sample rather than the mean. This feeds a safety
// floor, and the question it answers is "could this run take the machine under
// the cutout", which is about the bad case, not the typical one. A job that
// usually costs nothing and occasionally costs four percent is a job that can
// occasionally strand the machine.
func (d *Daemon) expectedDrainPct(job *model.Job) int {
	if job == nil {
		return -1
	}
	runs, err := d.store.Runs(job.ID)
	if err != nil || len(runs) == 0 {
		return -1
	}
	if len(runs) > drainSamplesConsidered {
		runs = runs[len(runs)-drainSamplesConsidered:]
	}

	worst, samples := 0, 0
	for _, r := range runs {
		// Both endpoints must be real readings. -1 means AC or an unread
		// battery, and a hold that ended while charging would otherwise
		// produce a negative "drain" that silently lowers the floor.
		if r.BatteryStart < 0 || r.BatteryEnd < 0 {
			continue
		}
		used := r.BatteryStart - r.BatteryEnd
		if used < 0 {
			// Charged mid-run. Real, and evidence of nothing.
			continue
		}
		samples++
		if used > worst {
			worst = used
		}
	}
	if samples < minDrainSamples {
		return -1
	}
	return worst
}
