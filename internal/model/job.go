// Package model defines the core domain types shared by the CLI, daemon,
// helper, and GUI. Everything here is plain data with stable JSON shapes —
// `wakeguard status --json` and the menu bar app both decode these directly,
// so field names are part of the public contract.
package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// DetectionMode is how the daemon learns that a job started and stopped.
type DetectionMode string

const (
	// DetectMark is exact: the job is wrapped in `wakeguard-mark`, which
	// pings the daemon on start and exit. Gives a true duration from run one
	// and a real exit code.
	DetectMark DetectionMode = "mark"

	// DetectPattern is best-effort: the daemon scans the process table for a
	// regexp match. Requires no change to the user's crontab, but a wrong
	// pattern means the job is never observed — the daemon reports that as
	// DetectionNeverMatched rather than failing silently.
	DetectPattern DetectionMode = "pattern"

	// DetectNone attempts no detection at all: wake the machine, hold for a
	// bounded window, release.
	//
	// This exists for jobs the user cannot instrument. An application that
	// runs schedules inside its own process — Hermes, n8n, a self-hosted
	// runner — gives no command line to wrap and no distinct process to match.
	// Forcing one of the other two modes on such a job would mean every run
	// is recorded as never-detected and warned about, which is noise about a
	// configuration that is in fact correct.
	//
	// The tradeoff is explicit: the hold lasts a fixed window rather than the
	// job's real runtime, so it cannot self-tune and costs more battery. That
	// is the honest price of not being able to see the job.
	DetectNone DetectionMode = "none"
)

func (d DetectionMode) Valid() bool {
	return d == DetectMark || d == DetectPattern || d == DetectNone
}

// Observable reports whether this mode can actually see the job run. Callers
// use it to decide whether an undetected window is a problem worth reporting
// or simply how this job works.
func (d DetectionMode) Observable() bool {
	return d == DetectMark || d == DetectPattern
}

// Job is a registered schedule that WakeGuard holds a wake window for.
// WakeGuard never executes a job — the user's existing cron/launchd/systemd
// entry does that. A Job only describes when to be awake and how to tell
// whether the work is still running.
type Job struct {
	// ID is the stable slug used in file paths and IPC. Derived from Name.
	ID   string `json:"id"`
	Name string `json:"name"`

	// Schedule is a cron expression ("0 9 * * *"), a descriptor ("@daily"),
	// or an interval ("every 6h"). Parsed by internal/schedule.
	Schedule string `json:"schedule"`

	// TZ is the IANA zone the schedule is evaluated in. Empty means local.
	// Stored explicitly so a laptop crossing time zones keeps firing when the
	// user expects, and so the GUI can show an unambiguous next-run time.
	TZ string `json:"tz,omitempty"`

	Detection DetectionMode `json:"detection"`

	// Match is the regexp tested against process command lines. Only
	// meaningful when Detection is DetectPattern.
	Match string `json:"match,omitempty"`

	// Command is the original scheduler command line, recorded at import so
	// the CLI can show what this job actually runs and so `import` can detect
	// that an entry it already adopted has since changed.
	Command string `json:"command,omitempty"`

	// Source records where this job came from (crontab, launchd, systemd,
	// manual) purely for display and re-import reconciliation.
	Source string `json:"source,omitempty"`

	// Group is an optional folder name for organising the job list. Flat and
	// free-text: a nested hierarchy is a lot of machinery for a list that is
	// rarely longer than a screen, and it can be added later without a
	// migration because an empty Group already means "ungrouped".
	Group string `json:"group,omitempty"`

	// MaxRuntime optionally overrides the learned ceiling. Zero means "let
	// the estimator decide" — the documented default. This exists as an
	// escape hatch, deliberately not as the primary mechanism (PRD §6).
	MaxRuntime Duration `json:"max_runtime,omitempty"`

	// WakeBuffer is how long before fire time to wake the machine. Zero means
	// use the global config default.
	WakeBuffer Duration `json:"wake_buffer,omitempty"`

	// Enabled jobs are scheduled; disabled ones are kept for their history
	// but never wake the machine.
	Enabled bool `json:"enabled"`

	// Managed marks a job WakeGuard adopted automatically from a scheduler it
	// watches, rather than one a human registered.
	//
	// The distinction matters for removal. A managed job is retired when it
	// disappears from its source, because otherwise deleting a job in the app
	// that owns it would leave WakeGuard waking the machine for something that
	// no longer exists. A hand-registered job is never removed automatically,
	// because a human's explicit intent should not be undone by a scan.
	Managed bool `json:"managed,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// Validate reports whether the job is internally coherent. Called on every
// mutation path (CLI add, GUI edit, config file load) so a hand-edited
// jobs.json can't wedge the daemon.
func (j *Job) Validate() error {
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("job name is required")
	}
	if j.ID == "" {
		return fmt.Errorf("job %q has no id", j.Name)
	}
	// Slug can no longer produce this, but a hand-edited jobs.json can still
	// name it. A job sharing an id with the manual keep-awake window would
	// fight it for the same slot in the daemon's hold map, so one of the two
	// would be silently dropped mid-flight.
	if j.ID == KeepAwakeJobID {
		return fmt.Errorf("job %q uses %q, which is reserved for the manual keep-awake hold", j.Name, KeepAwakeJobID)
	}
	if strings.TrimSpace(j.Schedule) == "" {
		return fmt.Errorf("job %q has no schedule", j.Name)
	}
	if !j.Detection.Valid() {
		return fmt.Errorf("job %q has unknown detection mode %q", j.Name, j.Detection)
	}
	if j.Detection == DetectPattern {
		if strings.TrimSpace(j.Match) == "" {
			return fmt.Errorf("job %q uses pattern detection but has no --match", j.Name)
		}
		// Compile it here rather than trusting it at detection time.
		//
		// An uncompilable pattern is accepted silently otherwise: the job looks
		// correctly registered in `list`, but the daemon's matcher fails to
		// build on every tick and simply returns, so the job is never observed.
		// It would then wake the machine and hold it for the full ceiling on
		// every run, indefinitely, with nothing obviously wrong. Rejecting at
		// the point of entry turns that into an error the user sees while they
		// are still typing.
		if _, err := regexp.Compile(j.Match); err != nil {
			return fmt.Errorf("job %q has an invalid --match pattern %q: %w", j.Name, j.Match, err)
		}
	}
	if j.TZ != "" {
		if _, err := time.LoadLocation(j.TZ); err != nil {
			return fmt.Errorf("job %q has unknown timezone %q", j.Name, j.TZ)
		}
	}
	// Normalise in place rather than only comparing normalised forms, so the
	// name that gets stored is the name every later reader groups by. Every
	// mutation path already runs through here.
	j.Group = NormalizeGroup(j.Group)
	// Counted in runes: the limit exists to keep a heading on one line, and a
	// byte limit would reject a name the user sees as well under it.
	if n := utf8.RuneCountInString(j.Group); n > MaxGroupLen {
		return fmt.Errorf("job %q has a group name of %d characters; the limit is %d",
			j.Name, n, MaxGroupLen)
	}
	if j.MaxRuntime < 0 {
		return fmt.Errorf("job %q has negative max_runtime", j.Name)
	}
	return nil
}

// MaxGroupLen bounds a group name. Groups are printed as headings above the
// job list, so the cap is about what stays readable in a terminal, not about
// storage.
const MaxGroupLen = 64

// NormalizeGroup trims and collapses internal whitespace so "Morning  jobs"
// and " Morning jobs " land in the same group rather than silently creating
// two that look identical in a list.
func NormalizeGroup(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ScheduleAnchor is the origin an interval schedule ("every 6h") counts from,
// for schedule.ParseAt.
//
// It exists so that the rule lives in exactly one place. An interval schedule
// has a cadence and no phase, so the phase comes from here — and the daemon's
// wake scheduler, the job list the GUI renders, and the CLI all have to pick
// the same one. If two of them disagreed, the machine would wake at a time
// that was never displayed, which is a worse failure than the sliding fire
// time this replaced.
//
// CreatedAt is used because it is the only per-job time that never moves. An
// anchor derived from history — the last observed run, say — re-phases the
// schedule every time a run is recorded, and for pattern detection it carries
// the detection lag with it, so the fire time would creep later on every
// cycle: a slower version of the drift being fixed.
//
// For an adopted job, CreatedAt is when WakeGuard first saw it and not when
// the source scheduler's interval clock started, so the phase is an estimate.
// See schedule.ParseAt, which documents what that estimate does and does not
// promise.
func (j *Job) ScheduleAnchor() time.Time { return j.CreatedAt }

// Location resolves the job's timezone, falling back to local time.
func (j *Job) Location() *time.Location {
	if j.TZ == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(j.TZ)
	if err != nil {
		return time.Local
	}
	return loc
}

// KeepAwakeJobID is the reserved id of the manual keep-awake window — the
// "keep this machine up for 30 minutes" hold a user asks for directly.
//
// It is not a job. Nothing with this id is ever written to jobs.json, listed,
// grouped, warned about, or given run history; it exists only so the manual
// hold can be an ordinary entry in the daemon's hold map and inherit the
// cutout, shutdown and helper-sync behaviour every other hold already has.
//
// The leading and trailing underscores are what keep it out of reach: Slug
// trims them, so no name a user can type produces this id and no registered
// job can collide with the manual hold. Validate refuses it outright for the
// one path that bypasses Slug, a hand-written jobs.json.
const KeepAwakeJobID = "__keep_awake__"

// Slug converts a human name into a filesystem- and IPC-safe id.
//
// Deliberately conservative, because the result becomes a filename under the
// history directory. Path separators are already collapsed to dashes, and
// leading or trailing dots are trimmed as well so the result can never be
// "." or ".." — which would be a relative path component rather than a name,
// and would also produce a hidden file. A name that reduces to nothing is
// rejected by the caller with a message naming the problem.
//
// Leading and trailing underscores are trimmed for a different reason: it
// reserves the __name__ shape for WakeGuard's own internal ids, so a user
// name can never collide with one — see KeepAwakeJobID.
func Slug(name string) string {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-._")
}
