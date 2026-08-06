package scan

import (
	"context"
	"time"
)

// RunRecord is a scheduler's own account of one job's most recent run.
//
// This is the third rung of the detection ladder, between "the tool calls us"
// (a hook) and "we watch a process". It exists because in-process schedulers —
// ones that run jobs inside a long-lived daemon rather than spawning a process
// per job — are invisible to process watching: nothing appears, nothing exits,
// so there is nothing whose lifetime equals the job's. Those schedulers do
// almost always write their run state to disk, because that is how they
// survive a restart, and reading it needs no cooperation, no configuration and
// no consent.
type RunRecord struct {
	// Running reports that the scheduler currently considers this job in
	// flight.
	Running bool

	// StartedAt is when the current or most recent run began. Zero when the
	// scheduler does not record it.
	StartedAt time.Time

	// CompletedAt is when the most recent run finished. Zero if it never has.
	CompletedAt time.Time

	// Status is the scheduler's own word for the outcome, normalised to "ok"
	// or "error". Empty when it does not say.
	Status string
}

// Succeeded reports a completed run the scheduler called good.
func (r RunRecord) Succeeded() bool { return r.Status == "ok" }

// Duration is how long the last completed run took, or zero when either end
// of it is unknown. Never negative: clocks and file writes race, and a
// negative duration would poison the estimator far worse than a missing one.
func (r RunRecord) Duration() time.Duration {
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() {
		return 0
	}
	d := r.CompletedAt.Sub(r.StartedAt)
	if d < 0 {
		return 0
	}
	return d
}

// RunObserver is implemented by providers whose scheduler records its own run
// state somewhere readable.
//
// The `ok` return is load-bearing and must be honoured by callers. These files
// are private to their schedulers: no version marker, no compatibility
// promise, and a field can be renamed in a patch release without anyone being
// told. An observer that cannot make sense of what it reads returns false so
// the caller falls back to a fixed window, rather than reporting a confident
// wrong duration. A wrong number is worse than an admitted guess.
type RunObserver interface {
	Provider

	// ObserveRun returns the scheduler's record for one job. ok is false when
	// the job is unknown, or when the file cannot be read or understood.
	ObserveRun(ctx context.Context, jobID string) (rec RunRecord, ok bool)
}

// Observers returns the providers on this machine that can report run state.
func Observers() []RunObserver {
	var out []RunObserver
	for _, p := range Providers() {
		if o, isObserver := p.(RunObserver); isObserver && p.Available() {
			out = append(out, o)
		}
	}
	return out
}
