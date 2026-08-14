package ipc

import (
	"encoding/json"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// ProtocolVersion is bumped on any breaking change to a request or response
// shape. The menu bar app ships independently of the daemon and can be a
// different version, so both sides check this and say so plainly rather than
// misrendering each other's data.
const ProtocolVersion = 1

// Op identifies a request. Namespaced by subject so the set stays legible as
// it grows.
type Op string

const (
	// Client -> daemon.
	OpPing       Op = "ping"
	OpStatus     Op = "status"
	OpJobsList   Op = "jobs.list"
	OpJobsAdd    Op = "jobs.add"
	OpJobsPut    Op = "jobs.put"
	OpJobsRemove Op = "jobs.remove"
	OpJobsEnable Op = "jobs.enable"
	OpHistory    Op = "history"
	OpConfigGet  Op = "config.get"
	OpConfigSet  Op = "config.set"
	OpSkipNext   Op = "skip_next"
	OpSleepNow   Op = "sleep_now"
	OpKeepAwake  Op = "keep_awake"
	OpPause      Op = "pause"
	OpResume     Op = "resume"
	OpTestMatch  Op = "match.test"
	OpSync       Op = "sync"

	// goguma-mark -> daemon. These are the exact-detection path: the
	// wrapper announces the job's real start and exit rather than the daemon
	// inferring them from the process table.
	OpMarkStart Op = "mark.start"
	OpMarkEnd   Op = "mark.end"

	// Daemon -> helper. Deliberately tiny: the helper holds no policy, so
	// these are the entire privileged surface of the product.
	OpHelperSetSleepBlocked Op = "helper.set_sleep_blocked"
	OpHelperScheduleWake    Op = "helper.schedule_wake"
	OpHelperVerifyWake      Op = "helper.verify_wake"
	OpHelperCancelWake      Op = "helper.cancel_wake"
	OpHelperStatus          Op = "helper.status"
)

// Request is the envelope for every call.
type Request struct {
	Protocol int             `json:"protocol"`
	Op       Op              `json:"op"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Response is the envelope for every reply. OK is checked before Payload.
type Response struct {
	Protocol int             `json:"protocol"`
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// ---- Payloads: client -> daemon ----

// JobRef names a job by id or name; the CLI passes whatever the user typed.
type JobRef struct {
	Ref string `json:"ref"`
}

type EnableReq struct {
	Ref     string `json:"ref"`
	Enabled bool   `json:"enabled"`
}

type JobsListResp struct {
	Jobs []JobView `json:"jobs"`

	// Groups is every group name in use, sorted. Sent so a client can render
	// empty groups and offer existing names without deriving them from the job
	// list, which would silently drop a group whose only job was just removed.
	Groups []string `json:"groups,omitempty"`
}

// JobView is a job joined with everything the UI needs to render a row, so a
// list view is one round trip rather than N+1.
type JobView struct {
	Job model.Job `json:"job"`

	NextFire *time.Time `json:"next_fire,omitempty"`
	NextWake *time.Time `json:"next_wake,omitempty"`

	// ScheduleDisplay is the human rendering ("daily at 09:00").
	ScheduleDisplay string `json:"schedule_display"`
	// ScheduleShort is the same rendering without the clock detail ("twice
	// daily"), for compact rows where NextFire is shown alongside and the
	// times in ScheduleDisplay would only repeat it.
	ScheduleShort string `json:"schedule_short,omitempty"`
	// ScheduleError is set when the stored expression no longer parses, which
	// otherwise manifests only as a job that mysteriously never runs.
	ScheduleError string `json:"schedule_error,omitempty"`

	Stats model.Stats `json:"stats"`

	// CeilingReason explains how the ceiling was derived, for `--explain`.
	CeilingReason string `json:"ceiling_reason,omitempty"`

	// Holding is true if this job currently has an open wake window.
	Holding bool `json:"holding"`
}

type HistoryReq struct {
	Ref   string `json:"ref"`
	Limit int    `json:"limit,omitempty"`
}

type HistoryResp struct {
	Job  model.Job   `json:"job"`
	Runs []model.Run `json:"runs"`
	// ScheduleDisplay is the human rendering ("twice daily, at 09:00 and
	// 21:00"). Sent alongside the job because the raw expression on
	// model.Job is a storage format, not something to show a person, and
	// every other list response already carries this.
	ScheduleDisplay string `json:"schedule_display,omitempty"`
	// ScheduleShort is the cadence alone ("twice daily"), for the header of a
	// history view that has no room for the clock list.
	ScheduleShort string      `json:"schedule_short,omitempty"`
	Stats         model.Stats `json:"stats"`
}

type ConfigResp struct {
	Config config.Config `json:"config"`
	// Warnings reports values that were clamped on load, so a hand-edited
	// config that is being partly ignored says so.
	Warnings []string `json:"warnings,omitempty"`

	// WakeFloorBasePct is the charge below which no wake is scheduled for a
	// job whose battery cost has never been measured.
	//
	// Reported rather than derived by the client. It is not simply the cutout
	// plus the rearm margin: the floor rises with a job's own measured drain,
	// so the arithmetic lives with the daemon that applies it. The GUI showed
	// `cutout + rearm` for a while, which was the flat-margin rule from before
	// the drain-aware floor landed and displayed 15% where the daemon would
	// wake at 11%.
	WakeFloorBasePct int `json:"wake_floor_base_pct"`
}

type ConfigSetReq struct {
	// Key/Value form for `goguma config set <key> <value>`.
	Key   string `json:"key"`
	Value string `json:"value"`
}

type SkipNextReq struct {
	// Ref empty means "the next wake, whichever job it belongs to".
	Ref string `json:"ref,omitempty"`
}

// KeepAwakeReq asks for a manual keep-awake window: sleep held off for a
// fixed period regardless of whether any job is running.
//
// Asking again while one is open replaces it rather than extending it: the
// user is stating a new deadline from now, not adding to an old one.
type KeepAwakeReq struct {
	// Duration is how long to stay awake. Zero or negative cancels.
	For model.Duration `json:"for"`
}

// KeepAwakeResp reports the window that is actually in force, which is not
// always the one that was asked for.
type KeepAwakeResp struct {
	Active bool       `json:"active"`
	Until  *time.Time `json:"until,omitempty"`
	// Clamped says the request was outside the permitted range and was
	// adjusted, so the caller can say so rather than appearing to ignore what
	// the user typed.
	Clamped bool `json:"clamped,omitempty"`
}

// MatchTestReq asks the daemon to evaluate a pattern against the live process
// table. This is what makes `add` able to say "that pattern matches nothing"
// at configuration time rather than silently failing at 3am.
type MatchTestReq struct {
	Pattern string `json:"pattern"`
}

type MatchTestResp struct {
	Valid   bool          `json:"valid"`
	Error   string        `json:"error,omitempty"`
	Matches []ProcessInfo `json:"matches"`
}

type ProcessInfo struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// ---- Payloads: goguma-mark -> daemon ----

// MarkStartReq is sent the instant a wrapped job begins.
type MarkStartReq struct {
	Job string `json:"job"`
	PID int    `json:"pid"`
	// Command is recorded for display and to detect that a job's underlying
	// command changed since it was registered.
	Command string `json:"command,omitempty"`
}

// MarkEndReq is sent when a wrapped job exits, carrying the real exit status.
// Pattern detection cannot observe exit codes at all, which is the second
// reason to prefer the wrapper beyond timing accuracy.
type MarkEndReq struct {
	Job      string `json:"job"`
	PID      int    `json:"pid"`
	ExitCode int    `json:"exit_code"`
}

// MarkResp tells the wrapper whether the daemon accepted the mark. The
// wrapper ignores failures and runs the job regardless; goguma must never
// be the reason a user's job does not run.
type MarkResp struct {
	Known bool `json:"known"`
}

// ---- Payloads: daemon -> helper ----

type SetSleepBlockedReq struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
}

type ScheduleWakeReq struct {
	At time.Time `json:"at"`
	// UseWakeOrPowerOn also boots a powered-off machine.
	UseWakeOrPowerOn bool `json:"use_wake_or_power_on"`
}

type VerifyWakeReq struct {
	At time.Time `json:"at"`
}

type VerifyWakeResp struct {
	// Registered is whether the OS genuinely holds a wake at the requested
	// time. "The scheduling command succeeded" is not proof of this: pmset
	// entries can be dropped by other apps, and rtcwake can program an alarm
	// the firmware ignores or offsets.
	Registered bool `json:"registered"`
}

type HelperStatusResp struct {
	Version      string     `json:"version"`
	SleepBlocked bool       `json:"sleep_blocked"`
	ScheduledAt  *time.Time `json:"scheduled_at,omitempty"`
	// Mechanism names what is actually holding sleep off, so `status` can be
	// specific rather than claiming a generic success.
	Mechanism string `json:"mechanism,omitempty"`
}
