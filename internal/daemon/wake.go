package daemon

import (
	"time"

	"github.com/junnam/wakeguard/internal/config"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/schedule"
	"github.com/junnam/wakeguard/internal/store"
)

// scheduleNextWake registers an OS wake for the earliest upcoming job.
//
// Only one wake is registered at a time — the nearest one. Registering every
// job's wake up front would fill the system schedule with entries that go
// stale as soon as a job is edited, and macOS in particular is happier with a
// single owned entry that is continually re-asserted.
func (d *Daemon) scheduleNextWake(now time.Time, cfg config.Config) {
	// Refuse to wake a machine that should be left asleep. Checked before
	// anything is registered, because a cutout can release a hold but cannot
	// un-wake a machine — see ShouldScheduleWake.
	d.mu.RLock()
	st := d.lastState
	d.mu.RUnlock()

	if safe, reason := ShouldScheduleWake(st, cfg); !safe {
		d.mu.Lock()
		had := d.nextWake != nil
		d.nextWake, d.nextJob, d.nextFire = nil, "", nil
		d.wakeOK, d.wakeErr = false, ""
		d.wakeSuppressed = reason
		d.mu.Unlock()

		if had {
			d.log.Warn("cancelling the scheduled wake", "reason", reason)
			_ = d.helper.CancelWake()
		}
		return
	}
	d.mu.Lock()
	d.wakeSuppressed = ""
	d.mu.Unlock()

	job, fire, wakeAt, ok := d.nextWakeTarget(now, cfg)
	if !ok {
		d.mu.Lock()
		had := d.nextWake != nil
		d.nextWake, d.nextJob, d.nextFire = nil, "", nil
		d.wakeOK, d.wakeErr = false, ""
		d.mu.Unlock()
		if had {
			_ = d.helper.CancelWake()
		}
		return
	}

	d.mu.RLock()
	current := d.nextWake
	registered := d.wakeOK
	d.mu.RUnlock()

	// Re-register when the target moves, OR when the last attempt failed.
	//
	// Checking only whether the target changed treats a recorded target as
	// proof the OS accepted it, which it is not. A single failure — the helper
	// restarting, which routinely happens for the first minute after boot —
	// would then be permanent: every later tick short-circuits here, nothing
	// retries, and the machine sleeps through the job. The failure is only
	// escaped once the wake time has already passed, by which point the run is
	// lost. Requiring `registered` makes a failed attempt retry on the next
	// tick instead.
	if current != nil && current.Equal(wakeAt) && registered {
		d.mu.Lock()
		d.nextJob, d.nextFire = job.Name, &fire
		d.mu.Unlock()
		return
	}

	err := d.helper.ScheduleWake(wakeAt, cfg.UseWakeOrPowerOn)

	d.mu.Lock()
	d.nextWake, d.nextJob, d.nextFire = &wakeAt, job.Name, &fire
	if err != nil {
		d.wakeOK, d.wakeErr = false, err.Error()
	} else {
		d.wakeOK, d.wakeErr = true, ""
	}
	d.mu.Unlock()

	if err != nil {
		d.log.Error("could not schedule the OS wake — jobs may be missed",
			"at", wakeAt.Format(time.RFC3339), "err", err)
		d.event(store.Event{
			Kind: store.EventWakeFailed, JobID: job.ID, JobName: job.Name,
			Message: err.Error(),
		})
		d.hooks.send(cfg.WebhookURL, webhookPayload{
			Event:   string(store.EventWakeFailed),
			Job:     job.Name,
			Message: "could not register a wake with the OS: " + err.Error(),
		})
		return
	}

	d.log.Info("scheduled OS wake",
		"at", wakeAt.Format(time.RFC3339), "job", job.ID, "fire", fire.Format(time.RFC3339))
	d.event(store.Event{
		Kind: store.EventWakeScheduled, JobID: job.ID, JobName: job.Name,
		Message: "wake at " + wakeAt.Format(time.RFC3339),
	})
}

// nextWakeTarget finds the soonest job that needs the machine awake.
func (d *Daemon) nextWakeTarget(now time.Time, cfg config.Config) (*model.Job, time.Time, time.Time, bool) {
	var (
		bestJob  *model.Job
		bestFire time.Time
		bestWake time.Time
		found    bool
	)
	for _, job := range d.store.Jobs() {
		if !job.Enabled {
			continue
		}
		sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor())
		if err != nil {
			continue
		}
		buffer := cfg.WakeBuffer.D()
		if job.WakeBuffer > 0 {
			buffer = job.WakeBuffer.D()
		}

		// Walk forward past any fire times the user chose to skip.
		fire := sched.Next(now)
		for range 10 {
			if !d.isSkipped(job.ID, fire) {
				break
			}
			fire = sched.Next(fire)
		}
		wakeAt := fire.Add(-buffer)
		if !wakeAt.After(now) {
			// The window is already open (or opening this tick); nothing to
			// schedule for it.
			continue
		}
		if !found || wakeAt.Before(bestWake) {
			bestJob, bestFire, bestWake, found = job, fire, wakeAt, true
		}
	}
	return bestJob, bestFire, bestWake, found
}

// isSkipped reports whether a specific upcoming fire was skipped by the user.
func (d *Daemon) isSkipped(jobID string, fire time.Time) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.skipNextGlobal.IsZero() && !fire.After(d.skipNextGlobal) {
		return true
	}
	until, ok := d.skipUntil[jobID]
	return ok && !fire.After(until)
}

// SkipNext suppresses the next wake, either for one job or globally.
func (d *Daemon) SkipNext(ref string) (time.Time, error) {
	now := time.Now()
	d.mu.RLock()
	cfg := d.cfg
	d.mu.RUnlock()

	if ref == "" {
		_, fire, _, ok := d.nextWakeTarget(now, cfg)
		if !ok {
			return time.Time{}, errNoSuchJob
		}
		d.mu.Lock()
		d.skipNextGlobal = fire
		d.mu.Unlock()
		d.log.Info("skipping the next wake", "fire", fire.Format(time.RFC3339))
		d.scheduleNextWake(now, cfg)
		return fire, nil
	}

	job, ok := d.store.Job(ref)
	if !ok {
		return time.Time{}, errNoSuchJob
	}
	sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor())
	if err != nil {
		return time.Time{}, err
	}
	fire := sched.Next(now)
	d.mu.Lock()
	d.skipUntil[job.ID] = fire
	d.mu.Unlock()
	d.log.Info("skipping next wake for job", "job", job.ID, "fire", fire.Format(time.RFC3339))
	d.scheduleNextWake(now, cfg)
	return fire, nil
}

// SleepNow releases every hold immediately.
func (d *Daemon) SleepNow() int {
	now := time.Now()
	d.mu.Lock()
	n := len(d.holds)
	for _, h := range d.holds {
		d.finishHoldLocked(h, now, model.OutcomeCutout)
	}
	d.mu.Unlock()
	d.syncSleepBlock()
	d.log.Info("released all holds on request", "count", n)
	return n
}

// detectSleepGap infers that the machine slept between two ticks.
//
// The daemon is frozen while the machine is asleep, so a tick arriving much
// later than the interval means time passed without us — which is exactly a
// sleep period. Recording it feeds the miss-risk model with first-party data
// and, on platforms with no queryable system sleep log, is the only source.
//
// The threshold is generous relative to the tick interval so that ordinary
// scheduler jitter or a busy machine is not mistaken for sleep.
func (d *Daemon) detectSleepGap(now time.Time) {
	d.mu.Lock()
	last := d.lastTick
	tick := d.cfg.TickInterval.D()
	d.mu.Unlock()

	if last.IsZero() {
		return
	}
	// Compare wall clocks, not monotonic ones.
	//
	// time.Now() carries a monotonic reading and Sub uses it exclusively when
	// both operands have one. The monotonic clock does not advance while the
	// machine is asleep on either platform — darwin's is mach_absolute_time,
	// Linux's is CLOCK_MONOTONIC — so the gap after a six-hour sleep measured
	// one tick interval and this function never fired at all. Round(0) strips
	// the monotonic reading so the comparison sees the wall-clock jump that
	// actually happened.
	gap := now.Round(0).Sub(last.Round(0))
	threshold := max(4*tick, time.Minute)
	if gap < threshold {
		return
	}

	iv := schedule.SleepInterval{Sleep: last, Wake: now}
	d.log.Info("machine appears to have slept",
		"from", last.Format(time.RFC3339), "to", now.Format(time.RFC3339),
		"duration", gap.Round(time.Second))
	if err := d.store.RecordSleepInterval(iv); err != nil {
		d.log.Debug("could not record sleep interval", "err", err)
	}
	d.event(store.Event{
		Kind:    store.EventWoke,
		Message: "woke after " + model.HumanDuration(gap),
	})

	// A wake this close to a scheduled one is almost certainly ours, which
	// means the upcoming run only happens because WakeGuard asked for it.
	d.mu.Lock()
	d.lastWokeAt = now
	d.mu.Unlock()

	// The helper's block state can be reset by the kernel across sleep/wake,
	// so re-assert immediately rather than waiting for the next timer.
	d.helper.Repush()
}

// wokeForThis reports whether this window follows a WakeGuard-initiated wake,
// meaning the run would otherwise have been silently skipped.
func (d *Daemon) wokeForThis(now time.Time) bool {
	d.mu.RLock()
	woke := d.lastWokeAt
	target := d.nextWake
	d.mu.RUnlock()

	if woke.IsZero() || target == nil {
		return false
	}
	// The wake must be recent, and must line up with the wake we requested.
	if now.Sub(woke) > 5*time.Minute {
		return false
	}
	d2 := woke.Sub(*target)
	return d2 > -2*time.Minute && d2 < 5*time.Minute
}
