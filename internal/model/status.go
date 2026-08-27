package model

import "time"

// Status is the full daemon snapshot. This is the exact shape emitted by
// `goguma status --json` and consumed by the menu bar app, so it is a
// stable public contract: fields may be added, but not renamed or removed
// without a version bump.
type Status struct {
	// Version of this payload shape, so a newer GUI can refuse an older
	// daemon rather than silently misrender it.
	SchemaVersion int `json:"schema_version"`

	DaemonRunning bool   `json:"daemon_running"`
	DaemonVersion string `json:"daemon_version,omitempty"`

	// Holding is true while sleep is being blocked for at least one job.
	Holding bool   `json:"holding"`
	Holds   []Hold `json:"holds"`

	// KeepAwakeUntil is when the manual keep-awake window ends, nil when there
	// is none. The window is already one of Holds, but it is surfaced here so a
	// client can render the countdown without a second call and without having
	// to know the reserved job id it hides behind.
	KeepAwakeUntil *time.Time `json:"keep_awake_until,omitempty"`

	// NextWake is the OS wake time currently registered with the platform,
	// nil if nothing is scheduled. Distinct from NextFire: this is the
	// pre-buffer wake, NextFire is when the job itself runs.
	NextWake *time.Time `json:"next_wake,omitempty"`
	NextJob  string     `json:"next_job,omitempty"`
	NextFire *time.Time `json:"next_fire,omitempty"`

	// WakeScheduled reports whether the platform accepted our wake request.
	// False with a non-empty WakeError means jobs will be silently missed,
	// the single most important failure to surface.
	WakeScheduled bool   `json:"wake_scheduled"`
	WakeError     string `json:"wake_error,omitempty"`

	// WakeSuppressed explains why no wake is registered when the machine is
	// being left asleep on purpose. Separate from WakeError so a working
	// safeguard is never reported as a broken scheduler.
	WakeSuppressed string `json:"wake_suppressed,omitempty"`

	// NoWakeReason says why nothing is scheduled, when the reason is not the
	// battery: no jobs, all of them off, or no readable schedule. Empty when a
	// wake exists or when the absence is transient.
	NoWakeReason string `json:"no_wake_reason,omitempty"`

	// Helper state. A disconnected helper means clamshell (lid-closed) holds
	// cannot work, even though idle holds still can.
	HelperConnected bool   `json:"helper_connected"`
	HelperVersion   string `json:"helper_version,omitempty"`
	SleepBlocked    bool   `json:"sleep_blocked"`

	Power  PowerState `json:"power"`
	Paused bool       `json:"paused"`

	// Starting reports that the daemon has not finished its first tick, so
	// nothing in Power has been measured yet and no wake has been computed.
	// Surfaces should say so rather than render the zero values, which read as
	// findings ("0% on battery", "nothing scheduled") when they are absences.
	Starting bool    `json:"starting,omitempty"`
	Cutout   *Cutout `json:"cutout,omitempty"`
	LastRun  *Run    `json:"last_run,omitempty"`

	// Warnings are user-actionable problems the daemon has detected, such as
	// a job whose match pattern has never fired. Surfaced in `status` and the
	// GUI so a broken config is loud rather than silent.
	Warnings []Warning `json:"warnings,omitempty"`

	// AgentSessions are coding agents observed working while `agent_hooks` is
	// off, and therefore NOT being held for.
	//
	// Always empty when the setting is on, because each of those is a real
	// hold and appears in Holds instead. Reporting them separately is the
	// whole point: with the setting off the machine will sleep straight
	// through a working agent, and the only thing that stops it is the user
	// deciding to hold it awake. This is what lets a surface say so while
	// there is still time to act, rather than afterwards.
	AgentSessions []AgentSession `json:"agent_sessions,omitempty"`
}

// AgentSession is an agent goguma can see working but is deliberately not
// holding sleep off for.
//
// Carries no deadline or ceiling, unlike Hold, because there is nothing to
// expire: it is an observation, not a commitment. It stops being reported when
// the agent says it has stopped, or when it has gone quiet for longer than a
// hold's lease would have survived.
type AgentSession struct {
	// Label is the session's own name for itself, the same string a hold for
	// it would have carried.
	Label string `json:"label"`

	// Since is when this session was first seen working.
	Since time.Time `json:"since"`
}

// Hold is one active sleep-block, one per job in its wake window.
type Hold struct {
	JobID     string    `json:"job_id"`
	JobName   string    `json:"job_name"`
	Reason    string    `json:"reason,omitempty"`
	OpenedAt  time.Time `json:"opened_at"`
	StartedAt time.Time `json:"started_at,omitzero"`

	// Deadline is when the ceiling force-releases this hold.
	Deadline time.Time `json:"deadline"`
	Ceiling  Duration  `json:"ceiling"`

	// Detected is true once the job process has actually been observed. A
	// hold that is open but undetected well past fire time is the visible
	// symptom of a bad match pattern.
	Detected  bool          `json:"detected"`
	Detection DetectionMode `json:"detection"`
	PID       int           `json:"pid,omitempty"`
}

// Elapsed is how long this hold has been blocking sleep.
func (h Hold) Elapsed(now time.Time) time.Duration { return now.Sub(h.OpenedAt) }

// Remaining is how long until the ceiling force-releases this hold.
func (h Hold) Remaining(now time.Time) time.Duration { return h.Deadline.Sub(now) }

// PowerState is the machine context the safety cutouts act on.
type PowerState struct {
	LidClosed     bool     `json:"lid_closed"`
	OnAC          bool     `json:"on_ac"`
	BatteryPct    int      `json:"battery_pct"`
	CPUTempC      *float64 `json:"cpu_temp_c,omitempty"`
	ThermalSource string   `json:"thermal_source,omitempty"`
}

// CutoutKind identifies which safety valve fired.
type CutoutKind string

const (
	CutoutThermal    CutoutKind = "thermal"
	CutoutLowBattery CutoutKind = "low_battery"
)

// Cutout records a fired safety release. Because the underlying job is often
// still running and would immediately re-acquire, a cutout latches until the
// hazard genuinely recedes; Latched reflects that.
type Cutout struct {
	Kind     CutoutKind `json:"kind"`
	FiredAt  time.Time  `json:"fired_at"`
	Detail   string     `json:"detail"`
	Latched  bool       `json:"latched"`
	Released int        `json:"released_holds"`
}

// WarningKind classifies daemon-detected configuration problems.
type WarningKind string

const (
	WarnNeverDetected  WarningKind = "never_detected"
	WarnWakeFailed     WarningKind = "wake_failed"
	WarnHelperDown     WarningKind = "helper_down"
	WarnCeilingHits    WarningKind = "ceiling_hits"
	WarnScheduleParse  WarningKind = "schedule_parse"
	WarnCommandChanged WarningKind = "command_changed"
	// WarnPowerOnCannotRun: `use_wake_or_power_on` is on, but this machine
	// would stop at an unlock screen and run nothing.
	//
	// The setting reads as though powering on were equivalent to waking. On a
	// FileVault machine it is not: the Mac lights up, waits for a password, and
	// the job is missed exactly as it would have been asleep, except a boot has
	// been paid for. FileVault is on by default on current Macs.
	WarnPowerOnCannotRun WarningKind = "power_on_cannot_run"
	// WarnUncovered: scheduled jobs exist on this machine that goguma is
	// not waking for. Silence here is the worst failure it has: the user
	// installed a tool to stop missing jobs and is still missing them.
	WarnUncovered WarningKind = "uncovered"
	// WarnRetired: a job goguma was waking for no longer exists in the
	// scheduler that created it, so it has been dropped.
	//
	// Worth saying out loud rather than leaving to be noticed. Adoption is
	// silent by design, which is right when jobs are appearing; the same
	// silence applied to a job disappearing means the list quietly gets
	// shorter and the machine quietly stops waking for something, and the
	// user finds out by counting rows.
	WarnRetired WarningKind = "retired"
	// WarnAdvisory: a signed notice from goguma's own feed, or a newer
	// release. Never a problem with the user's machine, which is why it is
	// its own kind rather than folded in with the faults.
	WarnAdvisory WarningKind = "advisory"
)

// Warning is a user-actionable problem. Each carries a Fix string that is
// literally the command to run, so `status` can tell the user what to do
// rather than only what is wrong.
type Warning struct {
	Kind    WarningKind `json:"kind"`
	JobID   string      `json:"job_id,omitempty"`
	Message string      `json:"message"`
	Fix     string      `json:"fix,omitempty"`
}
