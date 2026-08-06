package model

import "time"

// Outcome is how a job's wake window ended. These strings appear in
// history files and webhook payloads, so they are part of the contract.
type Outcome string

const (
	// OutcomeOK — the job was observed to start and exit normally within its
	// ceiling. This is the only outcome that feeds the duration estimator.
	OutcomeOK Outcome = "ok"

	// OutcomeFailed — the job ran to completion but reported a non-zero exit
	// code. Only detectable via wakeguard-mark; pattern detection cannot see
	// exit codes. The duration is still real, so it does feed the estimator.
	OutcomeFailed Outcome = "failed"

	// OutcomeCeiling — the job was still running when its ceiling expired and
	// the hold was force-released. The machine was allowed to sleep out from
	// under a live job, so the recorded duration is a lower bound, not a real
	// runtime, and must not train the estimator.
	OutcomeCeiling Outcome = "ceiling"

	// OutcomeNeverDetected — the window opened and closed without the daemon
	// ever seeing the job. Almost always a wrong --match pattern. Surfaced
	// loudly, because it means this job's whole configuration is inert.
	OutcomeNeverDetected Outcome = "never_detected"

	// OutcomeCutout — a thermal or low-battery safety cutout force-released
	// the hold mid-job. Not the job's fault; excluded from the estimator.
	OutcomeCutout Outcome = "cutout"
)

// TrainsEstimator reports whether a run's duration reflects a real completed
// runtime. Truncated runs (ceiling, cutout) would drag the estimate toward
// the ceiling itself — a feedback loop where one hung run permanently pins
// the ceiling high — so they are excluded.
func (o Outcome) TrainsEstimator() bool {
	return o == OutcomeOK || o == OutcomeFailed
}

// Run is one observed execution of a job. Appended as a line to
// history/<job-id>.jsonl.
type Run struct {
	JobID string `json:"job_id"`

	// WindowOpened is when WakeGuard began holding sleep off, which precedes
	// Started by roughly the wake buffer. The gap between the two is the
	// overhead WakeGuard itself costs, and is worth being able to audit.
	WindowOpened time.Time `json:"window_opened"`

	// Started is when the job process was first observed. Zero if the job was
	// never detected.
	Started time.Time `json:"started,omitzero"`

	// Ended is when the process was observed to exit, or when the hold was
	// force-released.
	Ended time.Time `json:"ended,omitzero"`

	// Duration is Ended-Started: the job's real runtime, which is what the
	// estimator learns from. Distinct from HoldDuration.
	Duration Duration `json:"duration"`

	// HoldDuration is how long sleep was actually blocked, window open to
	// release. This is the number that costs battery, and the delta against
	// Duration is exactly the waste WakeGuard exists to minimise.
	HoldDuration Duration `json:"hold_duration"`

	// BatteryStart and BatteryEnd bracket the hold, in percent. Both are -1
	// when the figure would be meaningless: on AC there is no drain to
	// measure, and an unread battery must never be confused with a flat one.
	//
	// Recorded rather than derived because the only honest answer comes from
	// two real readings. Estimating a drain from elapsed time would invent a
	// number that looks measured.
	BatteryStart int `json:"battery_start"`
	BatteryEnd   int `json:"battery_end"`

	Outcome   Outcome       `json:"outcome"`
	ExitCode  *int          `json:"exit_code,omitempty"`
	Detection DetectionMode `json:"detection"`

	// Ceiling is the cap that applied to this run, recorded so history can
	// show how close each run came to being truncated.
	Ceiling Duration `json:"ceiling"`

	// WokeMachine is true when WakeGuard scheduled an OS wake for this run
	// and the machine was actually asleep beforehand — i.e. this run would
	// have been silently skipped without WakeGuard. It is the tool's core
	// value metric, so it is recorded per-run rather than inferred later.
	WokeMachine bool `json:"woke_machine"`

	// PID of the observed process, for debugging a misbehaving match.
	PID int `json:"pid,omitempty"`
}

// Stats is the rolled-up view of a job's history shown by `list` and
// `history`, and used by the GUI's sparkline.
type Stats struct {
	JobID string `json:"job_id"`
	Runs  int    `json:"runs"`

	// Typical is the median completed runtime — what the user should expect.
	Typical Duration `json:"typical"`

	// P95 is the 95th-percentile completed runtime, the basis of the ceiling.
	P95 Duration `json:"p95"`

	// Ceiling is what the estimator will actually enforce on the next run.
	Ceiling Duration `json:"ceiling"`

	// ColdStart is true while there is too little history to trust, meaning
	// Ceiling is the conservative default rather than a learned value.
	ColdStart bool `json:"cold_start"`

	// BatteryDrained is how much charge this job cost, in percentage points,
	// summed over the runs that happened on battery power.
	//
	// This replaced a "wasted hold time" duration. That number was a proxy —
	// it said how long the Mac stayed up beyond the job, and left the reader
	// to guess what that cost. This is the cost itself, in the unit anyone
	// deciding whether a job is worth waking for actually thinks in.
	//
	// Runs on AC contribute nothing, because they cost nothing. A job that
	// only ever fires while plugged in reports 0, which is the truth.
	BatteryDrained int `json:"battery_drained"`

	LastRun     *Run  `json:"last_run,omitempty"`
	Failures    int   `json:"failures"`
	CeilingHits int   `json:"ceiling_hits"`
	NeverSeen   int   `json:"never_detected"`
	Recent      []Run `json:"recent,omitempty"`
}

// Drained is the charge this run cost, in percentage points.
//
// Zero unless both ends were measured on battery and the level actually fell.
// Charging mid-run produces a rise, not a negative cost.
func (r Run) Drained() int {
	if r.BatteryStart < 0 || r.BatteryEnd < 0 {
		return 0
	}
	if d := r.BatteryStart - r.BatteryEnd; d > 0 {
		return d
	}
	return 0
}

// MeasuredOnBattery reports whether this run's cost could be measured at all.
// Distinguishes "cost nothing, it was plugged in" from "cost nothing measured".
func (r Run) MeasuredOnBattery() bool {
	return r.BatteryStart >= 0 && r.BatteryEnd >= 0
}
