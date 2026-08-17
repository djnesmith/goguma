package daemon

import (
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
)

// hold is one open wake window.
//
// A window is opened before the job's fire time and stays open until the job
// is observed to exit, not until a timer expires. That distinction is the
// whole battery argument: hold duration converges on the job's real runtime
// plus wake overhead, rather than on a padded guess.
type hold struct {
	job      *model.Job
	fireAt   time.Time
	openedAt time.Time

	// ceiling is the force-release cap for this run.
	ceiling time.Duration
	// detectDeadline is when an undetected job gives up and closes the window
	// as never-detected.
	detectDeadline time.Time

	detected  bool
	startedAt time.Time
	pid       int

	// markEnded is set when goguma-mark reported the exit, which also
	// carries a real exit code, something pattern detection cannot observe.
	markEnded bool
	exitCode  *int

	// extended records that this run has already outlasted its ceiling, so
	// the fact is logged once rather than on every tick until it finishes.
	extended bool

	// batteryStart is the charge level when this window opened, in percent,
	// or -1 on AC or when unread. Half of the only honest measure of what a
	// job costs.
	batteryStart int

	// endedAt is when the job actually finished, when something authoritative
	// says so, currently a scheduler's own completion record. Distinct from
	// when the hold was released: the daemon notices on its next poll, up to a
	// poll interval later, and charging that lag to the job would inflate
	// every learned duration by the sampling rate.
	endedAt time.Time

	// wokeMachine records that the machine was actually asleep before this
	// window opened, meaning the run would have been skipped entirely without
	// goguma. This is the tool's value metric.
	wokeMachine bool

	// followsWake is true when the kernel says the machine woke moments before
	// this window opened.
	//
	// Separate from wokeMachine, which is inferred from a gap in the daemon's
	// own ticks and feeds the run history and the miss-risk figures. That
	// inference misses a wake out of a light sleep, where the daemon keeps
	// ticking and sees no gap: measured on a real run it was wrong for two
	// wakes out of three. Changing it would have moved the meaning of "woken"
	// everywhere it is already reported, so this is a second, narrower fact
	// used for one decision.
	followsWake bool

	assertion power.IdleAssertion
}

// deadline is when this hold must be released regardless of what the job is
// doing. Before detection it is bounded by the detect deadline; afterwards by
// the ceiling measured from the observed start.
//
// A wake-only hold has no detection to wait for, so its window runs from the
// job's fire time for exactly the ceiling; there is nothing to observe and
// no reason to extend past it.
func (h *hold) deadline() time.Time {
	if h.job.Detection == model.DetectNone {
		return h.fireAt.Add(h.ceiling)
	}
	if h.detected {
		return h.startedAt.Add(h.ceiling)
	}
	return h.detectDeadline
}

// expired reports whether the safety valve should fire.
func (h *hold) expired(now time.Time) bool { return now.After(h.deadline()) }

// hardDeadline is the point past which a running job is released regardless.
//
// Measured from when the job was seen to start, so it bounds the *run* rather
// than the window it was waiting in. A job still going at this point is not
// slow, it is stuck.
func (h *hold) hardDeadline(cfg config.Config) time.Time {
	start := h.startedAt
	if start.IsZero() {
		start = h.openedAt
	}
	return start.Add(cfg.MaxCeiling.D())
}

// manual reports whether this is the user's own keep-awake window rather than
// a job's wake window.
//
// It is the one hold with no registered job behind it, so every path that
// treats a closed window as evidence about a job (run history, the ceiling
// estimator, job statistics) has to skip it.
func (h *hold) manual() bool { return h.job.ID == model.KeepAwakeJobID }

// view converts to the wire representation.
func (h *hold) view() model.Hold {
	v := model.Hold{
		JobID:     h.job.ID,
		JobName:   h.job.Name,
		OpenedAt:  h.openedAt,
		Deadline:  h.deadline(),
		Ceiling:   model.Duration(h.ceiling),
		Detected:  h.detected,
		Detection: h.job.Detection,
		PID:       h.pid,
	}
	if h.detected {
		v.StartedAt = h.startedAt
	}
	return v
}

// finish builds the history record for a completed or force-released window.
func (h *hold) finish(now time.Time, outcome model.Outcome, batteryEnd int) model.Run {
	r := model.Run{
		JobID:        h.job.ID,
		WindowOpened: h.openedAt,
		Outcome:      outcome,
		Detection:    h.job.Detection,
		Ceiling:      model.Duration(h.ceiling),
		WokeMachine:  h.wokeMachine,
		PID:          h.pid,
		HoldDuration: model.Duration(now.Sub(h.openedAt)),
		ExitCode:     h.exitCode,
		BatteryStart: h.batteryStart,
		BatteryEnd:   batteryEnd,
	}
	if h.detected {
		end := now
		if !h.endedAt.IsZero() && h.endedAt.After(h.startedAt) {
			end = h.endedAt
		}
		r.Started = h.startedAt
		r.Ended = end
		r.Duration = model.Duration(end.Sub(h.startedAt))
	}
	return r
}

// detectionGrace is how long past a job's fire time the daemon keeps looking
// before declaring it never-detected.
//
// Generous on purpose. The machine may have just woken from deep sleep, where
// filesystem remount and service startup can delay cron by tens of seconds,
// and a job wrongly declared missing produces a loud warning about a match
// pattern that is actually fine. Two minutes is well past any realistic
// post-wake delay while still bounding the wasted hold.
const detectionGrace = 2 * time.Minute

func detectDeadlineFor(fireAt time.Time, ceiling time.Duration) time.Time {
	grace := ceiling
	if grace < detectionGrace {
		grace = detectionGrace
	}
	return fireAt.Add(grace)
}
