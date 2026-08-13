package daemon

import (
	"fmt"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/store"
)

// Bounds on a manual keep-awake window.
//
// The floor exists because a request measured in seconds is almost always a
// mistyped unit, and a window shorter than a tick would be released before the
// user saw it take effect.
//
// The ceiling is the real safeguard. There is deliberately no "stay awake
// until I say otherwise": a hold nobody remembers is exactly the failure the
// safety chapter exists to prevent (a laptop held awake in a bag overnight)
// and the difference between that and a bounded window is that the bounded one
// ends by itself. Twelve hours is longer than any deliberate use of this and
// still cannot run past a working day.
const (
	MinKeepAwake = time.Minute
	MaxKeepAwake = 12 * time.Hour
)

// keepAwakeJob is the synthetic job the manual hold hangs off.
//
// Built as an ordinary hold rather than a second sleep-blocking mechanism on
// purpose. A parallel path would have to re-derive cutout release, shutdown
// release, and helper syncing, and would drift away from the job path the
// first time any of the three changed. As a hold it gets all of them for free.
//
// DetectNone is exactly the right mode: hold.deadline gives a wake-only hold a
// fixed window running from fireAt for its ceiling, with nothing to observe and
// no reason to extend, which is the whole of what "keep this machine awake for
// 30 minutes" means.
//
// It is never handed to the store, so it reaches neither jobs.json, the job
// list, Groups(), the warning sweep, nor the miss-risk model; all of those read
// registered jobs, not open holds.
func keepAwakeJob() *model.Job {
	return &model.Job{
		ID:        model.KeepAwakeJobID,
		Name:      "manual keep-awake",
		Detection: model.DetectNone,
	}
}

// KeepAwake opens, replaces, or releases the manual keep-awake window.
//
// A non-positive duration cancels, so one entry point covers both directions
// and a client cannot get into the state of having asked for a window it has
// no matching call to close.
func (d *Daemon) KeepAwake(want time.Duration, now time.Time) (ipc.KeepAwakeResp, error) {
	if want <= 0 {
		d.releaseKeepAwake(now)
		return ipc.KeepAwakeResp{}, nil
	}

	// Refuse while a cutout is latched, exactly as openDueWindows does.
	//
	// This is not merely tidy. evaluateCutouts stops firing once the latch is
	// engaged, so a hold opened during a latched cutout is not released by the
	// next pass; it would sit out its full window on a machine already judged
	// too hot or too flat to be held awake. Refusing is the only point at which
	// that can be caught.
	d.mu.RLock()
	cut := d.latch.Active()
	d.mu.RUnlock()
	if cut != nil {
		return ipc.KeepAwakeResp{}, fmt.Errorf(
			"a safety cutout is active (%s); holds stay suspended until conditions recover", cut.Detail)
	}

	ceiling, clamped := clampKeepAwake(want)
	job := keepAwakeJob()

	// Take the assertion before touching the map: HoldIdleSleep is the slow
	// part (on Linux it deliberately waits 300ms) and holding d.mu across it
	// would stall every tick and every status call for its duration.
	h := &hold{job: job, fireAt: now, openedAt: now, ceiling: ceiling,
		batteryStart: batteryLevel(d.lastState)}
	if a, err := d.plat.HoldIdleSleep("goguma: " + job.Name); err != nil {
		d.log.Error("could not hold idle sleep for a manual keep-awake", "err", err)
	} else {
		h.assertion = a
	}

	d.mu.Lock()
	// Replace rather than stack. The user asked to be awake for a new period
	// starting now, not for the sum of this request and the last one. The old
	// hold is finished properly instead of being overwritten, because dropping
	// the map entry would strand its idle assertion with nothing left able to
	// release it; the machine would then refuse to idle-sleep until the daemon
	// restarted.
	if prev, ok := d.holds[model.KeepAwakeJobID]; ok {
		d.finishHoldLocked(prev, now, model.OutcomeOK)
	}
	d.holds[job.ID] = h
	until := h.deadline()
	d.mu.Unlock()

	d.syncSleepBlock()
	d.log.Info("keep-awake window opened",
		"for", ceiling, "until", until.Format(time.RFC3339), "clamped", clamped)
	d.event(store.Event{
		Kind: store.EventWindowOpened, JobID: job.ID, JobName: job.Name,
		Message: fmt.Sprintf("manual keep-awake for %s", model.HumanDuration(ceiling)),
	})
	return ipc.KeepAwakeResp{Active: true, Until: &until, Clamped: clamped}, nil
}

// releaseKeepAwake closes the manual window if one is open, reporting whether
// there was one to close.
func (d *Daemon) releaseKeepAwake(now time.Time) bool {
	d.mu.Lock()
	h, ok := d.holds[model.KeepAwakeJobID]
	if ok {
		d.finishHoldLocked(h, now, model.OutcomeOK)
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	d.syncSleepBlock()
	return true
}

// keepAwakeUntilLocked reports the end of the manual window. Caller holds d.mu.
func (d *Daemon) keepAwakeUntilLocked() *time.Time {
	h, ok := d.holds[model.KeepAwakeJobID]
	if !ok {
		return nil
	}
	until := h.deadline()
	return &until
}

// clampKeepAwake bounds a requested window, reporting whether it had to move.
// The caller passes that back to the user: silently substituting a different
// duration for the one they typed reads as the command having been ignored.
func clampKeepAwake(want time.Duration) (time.Duration, bool) {
	switch {
	case want < MinKeepAwake:
		return MinKeepAwake, true
	case want > MaxKeepAwake:
		return MaxKeepAwake, true
	default:
		return want, false
	}
}
