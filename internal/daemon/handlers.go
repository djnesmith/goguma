package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/junnam586/goguma/internal/advisory"
	"github.com/junnam586/goguma/internal/detect"
	"github.com/junnam586/goguma/internal/estimate"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/schedule"
	"github.com/junnam586/goguma/internal/store"
)

// handle dispatches an IPC request from the CLI, the GUI, or goguma-mark.
func (d *Daemon) handle(ctx context.Context, op ipc.Op, payload json.RawMessage) (any, error) {
	switch op {
	case ipc.OpPing:
		return map[string]string{"version": d.version}, nil

	case ipc.OpStatus:
		return d.Status(), nil

	case ipc.OpJobsList:
		return d.jobsList(), nil

	case ipc.OpJobsAdd:
		var job model.Job
		if err := json.Unmarshal(payload, &job); err != nil {
			return nil, err
		}
		if job.ID == "" {
			job.ID = model.Slug(job.Name)
		}
		// Parse the schedule before storing, not just Validate it: a stored
		// unparseable schedule is a poison pill every tick trips over, and
		// clients cannot be trusted to have checked (the CLI once stored one
		// and then crashed before its own check ran).
		if _, perr := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); perr != nil {
			return nil, perr
		}
		if err := d.store.Add(&job); err != nil {
			return nil, err
		}
		d.afterJobChange()
		return job, nil

	case ipc.OpJobsPut:
		var job model.Job
		if err := json.Unmarshal(payload, &job); err != nil {
			return nil, err
		}
		// A client edit that changes what sync mirrors (schedule or command)
		// is a takeover: clear Managed so the next sync does not silently
		// revert an explicit human decision back to the source's values.
		// Sync's own refreshes call store.Put directly, not this op, so
		// untouched adopted jobs keep tracking their source.
		if existing, ok := d.store.Job(job.ID); ok && existing.Managed &&
			(job.Schedule != existing.Schedule || job.Command != existing.Command) {
			job.Managed = false
		}
		// Same parse-before-store guard as jobs.add.
		if _, perr := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); perr != nil {
			return nil, perr
		}
		if err := d.store.Put(&job); err != nil {
			return nil, err
		}
		d.afterJobChange()
		return job, nil

	case ipc.OpJobsRemove:
		var req ipc.JobRef
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		job, err := d.store.Remove(req.Ref)
		if err != nil {
			return nil, err
		}
		// Close any window this job currently holds, so removing a job does
		// not leave the machine pinned awake for something that no longer
		// exists.
		d.releaseJob(job.ID)
		d.afterJobChange()
		return job, nil

	case ipc.OpJobsEnable:
		var req ipc.EnableReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		job, err := d.store.SetEnabled(req.Ref, req.Enabled)
		if err != nil {
			return nil, err
		}
		if !req.Enabled {
			d.releaseJob(job.ID)
		}
		d.afterJobChange()
		return job, nil

	case ipc.OpHistory:
		var req ipc.HistoryReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.history(req)

	case ipc.OpConfigGet:
		d.mu.RLock()
		defer d.mu.RUnlock()
		return ipc.ConfigResp{
			Config:   d.cfg,
			Warnings: d.cfgWarn,
			// -1 is "never measured on battery", which is the baseline every
			// job starts at and the only floor a client can state without
			// knowing which job it is talking about.
			WakeFloorBasePct: wakeFloor(d.cfg, -1),
			// A build with no signing key refuses every advisory, genuine
			// ones included, so a client must not offer a switch for it.
			AdvisoriesAvailable: advisory.Enabled(),
		}, nil

	case ipc.OpConfigSet:
		var req ipc.ConfigSetReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.setConfig(req)

	case ipc.OpSkipNext:
		var req ipc.SkipNextReq
		_ = json.Unmarshal(payload, &req)
		fire, err := d.SkipNext(req.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"skipped_fire": fire}, nil

	// Sent by a surface that has shown the notices and is going away, so the
	// user is not told the same thing on every launch for three days.
	//
	// Acknowledged on close rather than on open. Acking as the popover
	// appears would clear the notice the same second it was rendered, and the
	// next poll a second later would erase it while the user was reading it.
	case ipc.OpAckNotices:
		d.ackNotices()
		return nil, nil

	case ipc.OpSync:
		adopted, updated, retired := d.SyncNow(ctx)
		return map[string]int{"added": adopted, "updated": updated, "retired": retired}, nil

	case ipc.OpSleepNow:
		return map[string]int{"released": d.SleepNow()}, nil

	case ipc.OpKeepAwake:
		var req ipc.KeepAwakeReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.KeepAwake(req.For.D(), time.Now())

	case ipc.OpPause:
		d.setPaused(true)
		return nil, nil

	case ipc.OpResume:
		d.setPaused(false)
		return nil, nil

	case ipc.OpTestMatch:
		var req ipc.MatchTestReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.testMatch(ctx, req.Pattern), nil

	case ipc.OpMarkStart:
		var req ipc.MarkStartReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.markStart(req), nil

	case ipc.OpMarkEnd:
		var req ipc.MarkEndReq
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return d.markEnd(req), nil

	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}

// Status assembles the full daemon snapshot.
func (d *Daemon) Status() model.Status {
	d.mu.RLock()
	defer d.mu.RUnlock()

	st := model.Status{
		SchemaVersion:   1,
		DaemonRunning:   true,
		DaemonVersion:   d.version,
		Holding:         len(d.holds) > 0,
		NextWake:        d.nextWake,
		NextJob:         d.nextJob,
		NextFire:        d.nextFire,
		WakeScheduled:   d.wakeOK,
		WakeError:       d.wakeErr,
		WakeSuppressed:  d.wakeSuppressed,
		NoWakeReason:    d.noWakeReason,
		HelperConnected: d.helper.Connected(),
		HelperVersion:   d.helper.Version(),
		Paused:          d.paused,
		Starting:        d.lastTick.IsZero(),
		LastRun:         d.lastRun,
		Cutout:          d.lastCut,
		Warnings:        d.warnings,
		Power: model.PowerState{
			LidClosed:     d.lastState.LidClosed,
			OnAC:          d.lastState.OnAC,
			BatteryPct:    d.lastState.BatteryPct,
			CPUTempC:      d.lastState.TempC,
			ThermalSource: d.lastState.TempSource,
		},
	}
	st.SleepBlocked = st.Holding && st.HelperConnected
	st.KeepAwakeUntil = d.keepAwakeUntilLocked()
	for _, h := range d.holds {
		st.Holds = append(st.Holds, h.view())
	}
	return st
}

// unattendedStretch is the window the nightly battery projection covers.
//
// Eight hours: a night's sleep, which is the stretch nobody is watching and
// the one where a job firing every thirty minutes turns into a flat battery
// by morning. Any figure here is arbitrary; this one is arbitrary and
// recognisable, which is what makes the number mean something to a reader.
const unattendedStretch = 8 * time.Hour

func (d *Daemon) jobsList() ipc.JobsListResp {
	d.mu.RLock()
	cfg := d.cfg
	held := make(map[string]bool, len(d.holds))
	for id := range d.holds {
		held[id] = true
	}
	d.mu.RUnlock()

	now := time.Now()
	var out []ipc.JobView
	for _, job := range d.store.Jobs() {
		v := ipc.JobView{Job: *job, Holding: held[job.ID]}

		if sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); err != nil {
			v.ScheduleError = err.Error()
		} else {
			v.ScheduleDisplay = sched.Display
			v.ScheduleShort = sched.DisplayShort
			// Same source of truth as the wake itself.
			//
			// This computed the fire time independently, so after the wake
			// path started trusting the scheduler's own answer the list still
			// showed the re-derived one, the app would have displayed a time
			// the daemon had no intention of waking for, which is worse than
			// either being wrong on its own.
			fire := d.schedulerFireTime(job, now)
			if fire.IsZero() {
				fire = sched.Next(now)
			}
			v.NextFire = &fire
			buffer := cfg.WakeBuffer.D()
			if job.WakeBuffer > 0 {
				buffer = job.WakeBuffer.D()
			}
			wake := fire.Add(-buffer)
			v.NextWake = &wake

			// How often it fires across an unattended night. `TypicalInterval`
			// is the smallest gap the schedule actually produces, which is the
			// one that matters: a job at 09:00 and 09:05 costs two wakes, not
			// one spaced a day apart.
			if gap := sched.TypicalInterval(now); gap > 0 {
				v.FiresPerNight = int(unattendedStretch / gap)
			}
		}

		runs, _ := d.store.Runs(job.ID)
		v.Stats = estimate.Summarize(job, runs, cfg)
		v.CeilingReason = estimate.Compute(job, runs, cfg).Reason
		// Only where there is a measurement. A job that has never run on
		// battery has BatteryPerRun zero, and multiplying that out would
		// report "0.0%" for something whose cost is simply unknown.
		if v.Stats.BatteryPerRun > 0 {
			v.NightlyBatteryPct = float64(v.FiresPerNight) * v.Stats.BatteryPerRun
		}
		out = append(out, v)
	}
	return ipc.JobsListResp{Jobs: out, Groups: d.store.Groups()}
}

func (d *Daemon) history(req ipc.HistoryReq) (ipc.HistoryResp, error) {
	job, ok := d.store.Job(req.Ref)
	if !ok {
		return ipc.HistoryResp{}, fmt.Errorf("no job named %q", req.Ref)
	}
	runs, err := d.store.Runs(job.ID)
	if err != nil {
		return ipc.HistoryResp{}, err
	}
	d.mu.RLock()
	cfg := d.cfg
	d.mu.RUnlock()

	stats := estimate.Summarize(job, runs, cfg)
	if req.Limit > 0 && len(runs) > req.Limit {
		runs = runs[len(runs)-req.Limit:]
	}
	resp := ipc.HistoryResp{Job: *job, Runs: runs, Stats: stats}
	// A schedule that no longer parses still has history worth reading, so a
	// parse failure leaves the display empty and the GUI shows the raw
	// expression rather than this becoming an error.
	if sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); err == nil {
		resp.ScheduleDisplay = sched.Display
		resp.ScheduleShort = sched.DisplayShort
	}
	return resp, nil
}

// testMatch evaluates a pattern against the live process table.
//
// This is what lets `add` and the GUI say "that matches nothing" while the
// user is still configuring, instead of the mistake surfacing as a job that
// silently never runs.
func (d *Daemon) testMatch(ctx context.Context, pattern string) ipc.MatchTestResp {
	m, err := detect.Compile(pattern)
	if err != nil {
		return ipc.MatchTestResp{Valid: false, Error: err.Error()}
	}
	procs, err := detect.Snapshot(ctx)
	if err != nil {
		return ipc.MatchTestResp{Valid: true, Error: "couldn't read the process table: " + err.Error()}
	}
	resp := ipc.MatchTestResp{Valid: true}
	for _, p := range m.Find(procs) {
		resp.Matches = append(resp.Matches, ipc.ProcessInfo{PID: p.PID, Command: p.Command})
	}
	return resp
}

// markStart handles a wrapped job announcing that it has begun.
func (d *Daemon) markStart(req ipc.MarkStartReq) ipc.MarkResp {
	job, ok := d.store.Job(req.Job)
	if !ok {
		d.log.Warn("mark from an unregistered job", "job", req.Job, "pid", req.PID)
		return ipc.MarkResp{Known: false}
	}
	now := time.Now()

	d.mu.Lock()
	h, open := d.holds[job.ID]
	if !open {
		// The job started outside any window goguma opened, a manual run,
		// or a schedule that has drifted from what is registered. Open a hold
		// anyway: something real is running and the machine should stay awake
		// for it. This is a genuine advantage of the wrapper over pattern
		// matching, which can only observe jobs inside an expected window.
		d.mu.Unlock()
		d.openAdHocWindow(job, now)
		d.mu.Lock()
		h, open = d.holds[job.ID]
		if !open {
			d.mu.Unlock()
			return ipc.MarkResp{Known: true}
		}
	}
	h.detected = true
	h.startedAt = now
	h.pid = req.PID
	d.eventLocked(store.Event{
		Kind: store.EventJobStarted, JobID: job.ID, JobName: job.Name,
		Message: fmt.Sprintf("wrapper reported start (pid %d)", req.PID),
	})
	d.mu.Unlock()

	d.log.Info("job started", "job", job.ID, "pid", req.PID, "detection", "mark")
	d.syncSleepBlock()
	return ipc.MarkResp{Known: true}
}

// markEnd handles a wrapped job reporting its exit.
func (d *Daemon) markEnd(req ipc.MarkEndReq) ipc.MarkResp {
	job, ok := d.store.Job(req.Job)
	if !ok {
		return ipc.MarkResp{Known: false}
	}
	now := time.Now()

	d.mu.Lock()
	h, open := d.holds[job.ID]
	if !open {
		d.mu.Unlock()
		return ipc.MarkResp{Known: true}
	}
	h.markEnded = true
	code := req.ExitCode
	h.exitCode = &code

	outcome := model.OutcomeOK
	if req.ExitCode != 0 {
		// The job ran to completion but failed. The duration is still a real
		// measurement, so it trains the estimator; the failure is recorded
		// because "ran but failed" is information pattern detection can never
		// provide.
		outcome = model.OutcomeFailed
	}
	d.finishHoldLocked(h, now, outcome)
	d.mu.Unlock()

	d.log.Info("job finished", "job", job.ID, "exit_code", req.ExitCode, "detection", "mark")
	d.syncSleepBlock()
	return ipc.MarkResp{Known: true}
}

// openAdHocWindow opens a hold for a job that reported a start with no window
// open.
func (d *Daemon) openAdHocWindow(job *model.Job, now time.Time) {
	d.mu.RLock()
	cfg := d.cfg
	d.mu.RUnlock()

	runs, _ := d.store.Runs(job.ID)
	est := estimate.Compute(job, runs, cfg)

	h := &hold{
		batteryStart:   batteryLevel(d.lastState),
		job:            job,
		fireAt:         now,
		openedAt:       now,
		ceiling:        est.Ceiling,
		detectDeadline: detectDeadlineFor(now, est.Ceiling),
	}
	if a, err := d.plat.HoldIdleSleep("goguma: " + job.Name); err == nil {
		h.assertion = a
	}
	d.mu.Lock()
	if _, exists := d.holds[job.ID]; exists {
		// Lost a race with the scheduled path, or with a second concurrent
		// mark for the same job. See openWindow: overwriting orphans an
		// assertion that nothing can ever release.
		d.mu.Unlock()
		if h.assertion != nil {
			_ = h.assertion.Release()
		}
		return
	}
	d.holds[job.ID] = h
	d.mu.Unlock()
	d.log.Info("opened an ad-hoc window for a job that started outside its schedule", "job", job.ID)
}

func (d *Daemon) releaseJob(jobID string) {
	now := time.Now()
	d.mu.Lock()
	if h, ok := d.holds[jobID]; ok {
		d.finishHoldLocked(h, now, model.OutcomeCutout)
	}
	d.mu.Unlock()
	d.syncSleepBlock()
}

func (d *Daemon) setPaused(p bool) {
	d.mu.Lock()
	d.paused = p
	d.mu.Unlock()
	if p {
		d.SleepNow()
		_ = d.helper.CancelWake()
		// Clear the wake bookkeeping to match the cancel. Leaving it in
		// place made resume a no-op: scheduleNextWake saw the same target
		// still marked registered and short-circuited, so no OS wake existed,
		// while status kept printing the stale time with no warning. The
		// machine then slept through the very next job.
		d.mu.Lock()
		d.nextWake, d.nextJob, d.nextFire = nil, "", nil
		d.wakeOK, d.wakeErr = false, ""
		d.mu.Unlock()
		d.log.Info("paused: no wakes will be scheduled and all holds are released")
	} else {
		d.log.Info("resumed")
		d.mu.RLock()
		cfg := d.cfg
		d.mu.RUnlock()
		d.scheduleNextWake(time.Now(), cfg)
	}
}

func (d *Daemon) afterJobChange() {
	d.mu.RLock()
	cfg := d.cfg
	d.mu.RUnlock()
	d.scheduleNextWake(time.Now(), cfg)
}
