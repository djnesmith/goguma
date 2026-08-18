// Package daemon is the unprivileged brain of goguma. It owns all policy:
// schedule evaluation, wake-time computation, duration history, job detection,
// hold lifecycle, and the safety cutouts. It asks the privileged helper only
// for the two things it cannot do itself: block sleep, and register an OS
// wake.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/junnam586/goguma/internal/advisory"
	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/detect"
	"github.com/junnam586/goguma/internal/estimate"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/scan"
	"github.com/junnam586/goguma/internal/schedule"
	"github.com/junnam586/goguma/internal/store"
)

// Daemon is the long-running service.
type Daemon struct {
	version string
	log     *slog.Logger
	store   *store.Store
	plat    power.Platform
	helper  *helperLink
	hooks   *webhookSender

	mu       sync.RWMutex
	cfg      config.Config
	cfgWarn  []string
	holds    map[string]*hold
	latch    CutoutLatch
	paused   bool
	lastRun  *model.Run
	lastCut  *model.Cutout
	warnings []model.Warning

	// nextWake / nextJob describe the wake currently registered with the OS.
	nextWake  *time.Time
	nextJob   string
	nextFire  *time.Time
	wakeOK    bool
	wakeErr   string
	skipUntil map[string]time.Time // job id -> suppress wakes for fires before this
	served    map[string]time.Time // job id -> last fire time a window completed for
	// lateWatch holds recently closed unobservable windows whose scheduler
	// may still report a completion; see pollSchedulerState.
	lateWatch      map[string]lateWindow
	skipNextGlobal time.Time

	// wakeSuppressed explains why no wake is registered when the machine is
	// deliberately being left asleep. Distinct from wakeErr, which means a
	// wake was wanted and the OS refused it: one is a decision, the other is
	// a failure, and conflating them would report a working safeguard as a
	// broken scheduler.
	wakeSuppressed string
	// noWakeReason says why there is no next wake, when the reason is not the
	// battery. Empty when a wake is scheduled, or when the absence is the
	// ordinary transient one (every window already open).
	noWakeReason string

	// lastTick and lastState support sleep detection: a gap between ticks far
	// larger than the tick interval means the machine was asleep in between.
	lastTick time.Time
	// busy is how long the control loop has spent inside its own work since
	// lastTick. Sleep is inferred from a wall-clock gap between ticks, and
	// the loop still makes blocking helper calls on the wake path, so without
	// this a daemon stuck in a 45-second socket read is indistinguishable
	// from a machine that slept for 45 seconds. Time spent running cannot be
	// time spent asleep, so it is subtracted before the gap is judged.
	busy time.Duration

	// sleepBackAt is when to put the machine back to sleep after goguma woke
	// it; zero means nothing pending. See sleepback.go.
	sleepBackAt  time.Time
	sleepBackJob string
	lastState    power.State

	// runSeq numbers `goguma run` holds so two concurrent wrapped commands get
	// separate entries in the hold map rather than one releasing the other.
	// Guarded by mu. Not persisted: no hold survives a restart, so a counter
	// that restarts with the daemon can never collide with a live one.
	runSeq uint64

	// advisory is the last verified notice feed, or nil when none has been
	// fetched, this build has no key compiled in, or the user turned it off.
	advisory *advisory.Feed

	// uncovered are scheduled jobs found on this machine that adoption
	// declined to take, kept so they can be reported rather than forgotten.
	uncovered []scan.Candidate

	// retired names jobs dropped because they vanished from their source,
	// with when it happened, so the next person to open the app is told
	// rather than left to notice a shorter list.
	//
	// Held in memory and time-boxed rather than acknowledged. There is no
	// "seen" signal to hang it on: the app polls constantly for the menu bar
	// glyph, so opening the popover is indistinguishable from not opening it.
	// The permanent record is the event log; this is the notice.
	retired []retiredJob

	// observers are the schedulers on this machine that keep their own run
	// records, keyed by source name. Resolved once at startup: which providers
	// exist does not change while the daemon runs.
	observers map[string]scan.RunObserver

	// lastWokeAt is when a sleep gap was last observed, used to attribute a
	// run to a goguma-initiated wake.
	lastWokeAt time.Time

	// sleepHist caches the reconstructed sleep history.
	//
	// Reading it costs ~4 seconds on macOS because `pmset -g log` dumps weeks
	// of power-management events. Doing that on every sync put a plain
	// `goguma sync` right at the CLI's 5-second timeout. The data covers a
	// fortnight and changes only when the machine sleeps, so re-reading it
	// more than occasionally buys nothing.
	sleepHist   *schedule.SleepHistory
	sleepHistAt time.Time

	// bg tracks background persistence so shutdown can flush it. Run records
	// and event-log lines are written off the control path to keep a slow
	// disk from delaying a sleep-hold release, but they must still land
	// before the process exits or the final run of a session is lost.
	bg sync.WaitGroup

	srv *ipc.Server
}

// New constructs a daemon.
func New(version string, log *slog.Logger, st *store.Store, plat power.Platform) *Daemon {
	return &Daemon{
		version:   version,
		log:       log,
		store:     st,
		plat:      plat,
		helper:    newHelperLink(log),
		hooks:     newWebhookSender(log),
		holds:     map[string]*hold{},
		skipUntil: map[string]time.Time{},
		served:    map[string]time.Time{},
		lateWatch: map[string]lateWindow{},
		cfg:       config.Default(),
		observers: resolveObservers(),
		// Unknown until the first tick reads the machine. The zero value of
		// power.State is "0%, not on AC", which is a perfectly plausible
		// reading of a nearly-flat battery, so before this, a daemon that had
		// simply not looked yet was indistinguishable from one looking at a
		// laptop about to die, both to the safety guards and to the user.
		lastState: power.State{BatteryPct: -1},
	}
}

// Run starts the IPC server and the main loop, returning when ctx is done.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.store.Layout().EnsureDirs(); err != nil {
		return fmt.Errorf("preparing state directory: %w", err)
	}
	if err := d.reload(); err != nil {
		return err
	}

	// Coding agents, brought into line with the setting on every start.
	//
	// Off the critical path: this reads and possibly rewrites files belonging
	// to other programs, and none of that should be able to delay the daemon
	// answering its socket, still less stop it starting.
	d.mu.RLock()
	startCfg := d.cfg
	d.mu.RUnlock()
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		d.reconcileAgentHooks(startCfg)
	}()

	srv, err := ipc.Listen(
		d.store.Layout().DaemonSocket(), 0o600, d.handle,
		ipc.WithAuthz(ipc.AllowSelf()), ipc.WithLogger(d.log),
	)
	if err != nil {
		return err
	}
	d.srv = srv

	// Apps that run their own schedules, described by manifest rather than by
	// a hand-written reader. Loaded before the first scan so a machine whose
	// real automation lives inside an application is covered from the first
	// tick rather than from the next restart.
	if loaded, problems := scan.LoadManifests(d.store.Layout().SchedulersDir()); len(loaded) > 0 || len(problems) > 0 {
		if len(loaded) > 0 {
			d.log.Info("loaded scheduler manifests", "sources", strings.Join(loaded, ", "))
		}
		for _, err := range problems {
			d.log.Warn("a scheduler manifest couldn't be used", "err", err)
		}
	}

	d.event(store.Event{Kind: store.EventDaemonStart, Message: "goguma " + d.version})
	d.log.Info("daemon started",
		"version", d.version,
		"socket", d.store.Layout().DaemonSocket(),
		"jobs", len(d.store.Jobs()))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(ctx); err != nil {
			d.log.Error("ipc server stopped", "err", err)
		}
	}()
	// Every push to the helper goes through here, off the control loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.helper.pump(ctx)
	}()

	d.loop(ctx)

	// Stop serving BEFORE releasing holds. The old order left the listener
	// up while shutdown cleared the sleep block, so a mark or keep-awake
	// request landing in that window (cron firing at logout) re-blocked
	// sleep on a daemon about to exit, stranding the global block until the
	// helper's dead-man switch noticed. Closing first also severs keep-alive
	// clients, whose open connections otherwise pinned wg.Wait until launchd
	// killed the process.
	srv.Close()

	// Release everything before exiting. A stranded hold would leave the
	// machine unable to sleep, so this runs even on an abrupt shutdown path.
	d.shutdown()
	wg.Wait()
	return nil
}

func (d *Daemon) loop(ctx context.Context) {
	d.mu.RLock()
	tick := d.cfg.TickInterval.D()
	poll := d.cfg.PollInterval.D()
	repush := d.cfg.WakeReassertInterval.D()
	d.mu.RUnlock()

	tickT := time.NewTicker(tick)
	defer tickT.Stop()
	pollT := time.NewTicker(poll)
	defer pollT.Stop()
	repushT := time.NewTicker(repush)
	defer repushT.Stop()

	d.mu.RLock()
	syncEvery := d.cfg.AutoAdoptInterval.D()
	d.mu.RUnlock()
	syncT := time.NewTicker(syncEvery)
	defer syncT.Stop()

	advisoryT := time.NewTicker(advisoryInterval)
	defer advisoryT.Stop()
	// Not at startup: a daemon that fetches the moment it launches makes the
	// first thing a new install does a network request, before the user has
	// seen the app at all. A few minutes in is soon enough for something
	// checked once a day.
	firstAdvisory := time.NewTimer(5 * time.Minute)
	defer firstAdvisory.Stop()

	// Sync before the first tick so a daemon restart immediately reflects any
	// jobs created while it was down.
	d.syncProviders(ctx)
	d.tick(ctx, time.Now())
	// Every branch is timed, because every branch can block: the wake path
	// calls the helper synchronously, and syncProviders shells out to each
	// scheduler on the machine.
	busy := func(f func()) {
		start := time.Now()
		f()
		d.mu.Lock()
		d.busy += time.Since(start)
		d.mu.Unlock()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tickT.C:
			busy(func() { d.tick(ctx, now) })
		case <-pollT.C:
			// Only meaningful while a window is open; a cheap no-op otherwise.
			now := time.Now()
			d.mu.RLock()
			cfg := d.cfg
			d.mu.RUnlock()
			busy(func() {
				d.pollDetection(ctx, now)
				d.pollSchedulerState(ctx, now)
				// Ceilings are enforced on the fast timer as well as the main
				// tick. Checking only on the tick means a short ceiling overshoots
				// by up to a full tick interval (a 5s ceiling fired at 10.2s in
				// testing), which matters because the ceiling exists to bound how
				// long a hung job can pin the machine awake.
				d.enforceCeilings(now, cfg)
				d.syncSleepBlock()
			})
		case <-repushT.C:
			d.helper.Repush()
		case <-syncT.C:
			busy(func() { d.syncProviders(ctx) })
		case <-firstAdvisory.C:
			go d.checkAdvisories(ctx)
		case <-advisoryT.C:
			// Off the loop goroutine: this one talks to the internet, and
			// the control loop must never wait on a network round trip.
			go d.checkAdvisories(ctx)
		}
	}
}

// tick is one pass of the control loop.
func (d *Daemon) tick(ctx context.Context, now time.Time) {
	d.detectSleepGap(now)

	st, err := d.plat.ReadState()
	if err != nil {
		d.log.Warn("couldn't read power state", "err", err)
		// Unknown, not flat.
		//
		// A failed read leaves `st` as the zero value, which is `OnAC: false,
		// BatteryPct: 0`, indistinguishable from a machine about to die. Both
		// battery guards then act on it: `ShouldScheduleWake` refuses to
		// register any wake ("staying asleep to preserve charge"), and the
		// low-battery cutout fires and force-releases every hold. So a
		// transient failure to read the battery silently stops the Mac being
		// woken for anything, and reports a reason that is not true.
		//
		// -1 is the sentinel both guards already check for and is what the
		// platform layer returns for a machine with no battery at all.
		st = power.State{BatteryPct: -1}
	}
	d.mu.Lock()
	d.lastState = st
	d.lastTick = now
	paused := d.paused
	cfg := d.cfg
	d.mu.Unlock()

	// Safety first: evaluate cutouts before opening anything new.
	d.evaluateCutouts(st, cfg, now)

	if !paused {
		d.openDueWindows(ctx, now, cfg)
	}
	d.pollDetection(ctx, now)
	d.pollSchedulerState(ctx, now)
	d.enforceCeilings(now, cfg)
	d.syncSleepBlock()
	d.maybeSleepBack(now, cfg)
	if !paused {
		d.scheduleNextWake(now, cfg)
	}
	d.refreshWarnings(st, cfg)
}

// openDueWindows opens a hold for any job whose wake window has arrived.
func (d *Daemon) openDueWindows(ctx context.Context, now time.Time, cfg config.Config) {
	if d.latch.Blocking() {
		return // a cutout is latched; do not re-acquire into a live hazard
	}
	for _, job := range d.store.Jobs() {
		if !job.Enabled {
			continue
		}
		d.mu.RLock()
		_, already := d.holds[job.ID]
		d.mu.RUnlock()
		if already {
			continue
		}

		// Anchored via the job so that the window this opens is for the same
		// fire time the wake was registered for; see model.Job.ScheduleAnchor.
		sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor())
		if err != nil {
			continue // surfaced as a warning by refreshWarnings
		}
		// Same source of truth as the wake itself. The wake is registered for
		// the owning scheduler's own next-run time when it has one (see
		// nextWakeTarget); opening the window from the parsed recurrence
		// instead means the machine can wake for a fire this loop never
		// holds for, and hold for a fire the scheduler will never use.
		fire := d.schedulerFireTime(job, now)
		if fire.IsZero() {
			fire = sched.Next(now.Add(-cfg.WakeBuffer.D()))
		}
		// The buffer look-back above deliberately catches a fire that is
		// already inside its window, but that same look-back re-finds a fire
		// whose window already opened, served the job, and closed, for as
		// long as `now` stays inside the buffer. Re-opening it holds the
		// machine awake for a job that just finished and then records a
		// phantom never_detected run every night. A fire already served is
		// done.
		d.mu.RLock()
		srv, wasServed := d.served[job.ID]
		d.mu.RUnlock()
		if wasServed && !fire.After(srv) {
			continue
		}
		buffer := cfg.WakeBuffer.D()
		if job.WakeBuffer > 0 {
			buffer = job.WakeBuffer.D()
		}
		windowOpens := fire.Add(-buffer)
		if now.Before(windowOpens) {
			continue
		}
		// Do not open a window for a fire time that has already passed by
		// more than the detection grace; that run is gone, and holding for
		// it would waste battery for nothing.
		if now.After(detectDeadlineFor(fire, 0)) {
			continue
		}
		if d.isSkipped(job.ID, fire) {
			continue
		}
		d.openWindow(ctx, job, fire, now, cfg)
	}
}

func (d *Daemon) openWindow(ctx context.Context, job *model.Job, fire, now time.Time, cfg config.Config) {
	runs, err := d.store.Runs(job.ID)
	if err != nil {
		d.log.Warn("couldn't read history", "job", job.ID, "err", err)
	}
	est := estimate.Compute(job, runs, cfg)

	h := &hold{
		batteryStart:   batteryLevel(d.lastState),
		job:            job,
		fireAt:         fire,
		openedAt:       now,
		ceiling:        est.Ceiling,
		detectDeadline: detectDeadlineFor(fire, est.Ceiling),
		wokeMachine:    d.wokeForThis(now),
		followsWake:    d.followsRecentWake(now),
	}

	// The unprivileged idle assertion is taken directly; the clamshell block
	// goes through the helper in syncSleepBlock.
	if a, err := d.plat.HoldIdleSleep("goguma: " + job.Name); err != nil {
		d.log.Error("couldn't hold idle sleep", "job", job.ID, "err", err)
	} else {
		h.assertion = a
	}

	d.mu.Lock()
	if _, exists := d.holds[job.ID]; exists {
		// Another path opened a window for this job while we were doing the
		// slow work above, reading history from disk, and taking the idle
		// assertion, which on Linux deliberately waits 300ms. Overwriting the
		// map entry would strand the other hold's assertion with no owner and
		// nothing to release it, leaving the machine unable to idle-sleep
		// until the daemon restarts. Release ours and let theirs stand.
		d.mu.Unlock()
		if h.assertion != nil {
			if err := h.assertion.Release(); err != nil {
				d.log.Warn("releasing a raced assertion failed", "job", job.ID, "err", err)
			}
		}
		return
	}
	d.holds[job.ID] = h
	d.mu.Unlock()

	d.log.Info("wake window opened",
		"job", job.ID, "fire", fire.Format(time.RFC3339),
		"ceiling", est.Ceiling, "detection", job.Detection)
	d.event(store.Event{
		Kind: store.EventWindowOpened, JobID: job.ID, JobName: job.Name,
		Message: fmt.Sprintf("window open, fires at %s, ceiling %s (%s)",
			fire.Format("15:04:05"), model.HumanDuration(est.Ceiling), est.Reason),
	})
}

// pollDetection looks for job start and exit.
func (d *Daemon) pollDetection(ctx context.Context, now time.Time) {
	d.mu.RLock()
	needScan := false
	var patternHolds []*hold
	for _, h := range d.holds {
		if h.job.Detection == model.DetectPattern {
			patternHolds = append(patternHolds, h)
			needScan = true
		} else if h.detected && !h.markEnded && h.pid > 0 {
			// A marked job whose wrapper was killed without reporting: fall
			// back to watching the pid so the hold still ends.
			needScan = true
		}
	}
	d.mu.RUnlock()

	if len(d.holdsSnapshot()) == 0 {
		return
	}

	var procs []detect.Process
	if needScan {
		var err error
		procs, err = detect.Snapshot(ctx)
		if err != nil {
			d.log.Warn("couldn't read the process table", "err", err)
			return
		}
	}

	for _, h := range patternHolds {
		d.updatePatternHold(h, procs, now)
	}
	d.checkMarkedPIDs(now)
}

func (d *Daemon) updatePatternHold(h *hold, procs []detect.Process, now time.Time) {
	m, err := detect.Compile(h.job.Match)
	if err != nil {
		return
	}
	matches := m.Find(procs)

	d.mu.Lock()
	defer d.mu.Unlock()

	switch {
	case !h.detected && len(matches) > 0:
		h.detected = true
		h.startedAt = now
		h.pid = matches[0].PID
		d.log.Info("job detected", "job", h.job.ID, "pid", h.pid)
		d.eventLocked(store.Event{
			Kind: store.EventJobStarted, JobID: h.job.ID, JobName: h.job.Name,
			Message: fmt.Sprintf("matched pid %d", h.pid),
		})

	case h.detected && len(matches) == 0:
		// The process is gone. Release immediately rather than waiting out
		// the remaining window; this is what keeps hold time close to real
		// runtime.
		d.finishHoldLocked(h, now, model.OutcomeOK)
	}
}

// checkMarkedPIDs ends a marked hold whose process died without the wrapper
// reporting (wrapper SIGKILLed, machine forced down mid-job).
func (d *Daemon) checkMarkedPIDs(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range d.holds {
		if h.job.Detection != model.DetectMark || !h.detected || h.markEnded || h.pid <= 0 {
			continue
		}
		if !detect.Alive(h.pid) {
			d.log.Info("marked job's process exited without a mark", "job", h.job.ID, "pid", h.pid)
			d.finishHoldLocked(h, now, model.OutcomeOK)
		}
	}
}

// enforceCeilings force-releases holds that have run past their cap.
// enforceCeilings closes windows whose time is up.
//
// A job that is *still running* when its ceiling arrives does not get cut off.
// The ceiling is an estimate of how long the job takes, and a job outlasting it
// is proof the estimate is low, not evidence of a problem. Releasing there did
// two bad things at once: the machine became free to sleep out from under a
// healthy job, and the run was discarded by the estimator (a truncated duration
// cannot be trained on), so the next run inherited the same wrong ceiling. A
// job that was reliably slower than its estimate was cut off every single run,
// forever, and the only exit was the user noticing a warning and setting
// --max-runtime by hand.
//
// So while the process is alive the hold is extended, and the true duration is
// recorded when it finally exits, which is what teaches the estimator the real
// number. MaxCeiling remains a hard backstop for a job that never exits at all,
// and the thermal and battery cutouts are unaffected: they fire on hazard, not
// on elapsed time, and they are what actually protects the machine.
func (d *Daemon) enforceCeilings(now time.Time, cfg config.Config) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range d.holds {
		if !h.expired(now) {
			continue
		}
		switch {
		// `&& !h.detected` matters now that a job's detectability is not fixed
		// by its declared mode. A hermes job is registered DetectNone because
		// no process can be watched for it, but its scheduler reports its own
		// runs, so the hold may well know it is running. Without this guard
		// the first case fired first and closed the window at the ceiling on a
		// job we could see was still going, discarding the very measurement
		// the observer exists to provide.
		case !h.job.Detection.Observable() && !h.detected:
			// A wake-only job was never going to be seen; reaching the end of
			// its window is how it is supposed to work, not a failure. Calling
			// it never-detected would generate a permanent warning about a
			// configuration that is correct.
			//
			// But when the job's scheduler keeps run records, the story may
			// not be over: a run that outgrew its learned window finishes
			// *after* this release, and its completion record is the only
			// evidence the ceiling is now too small. Watch for it, or the
			// estimator can never learn the job got slower and the machine
			// sleeps out from under it identically forever.
			if _, hasObs := d.observers[h.job.Source]; hasObs {
				d.lateWatch[h.job.ID] = lateWindow{
					opened: h.openedAt, fired: h.fireAt, closed: now,
				}
			}
			d.log.Info("wake-only window complete", "job", h.job.ID)
			d.finishHoldLocked(h, now, model.OutcomeOK)
		case h.detected && h.job.MaxRuntime > 0:
			// An explicit --max-runtime is the one promise the daemon must
			// keep. The still-running extension below exists for learned
			// guesses, where outlasting the estimate is proof the estimate
			// was low; a cap a human typed is not an estimate, and the
			// ceiling-hit warning explicitly tells users this flag is the
			// fix. Ignoring it here made that advice a no-op.
			d.log.Warn("job reached its explicit --max-runtime and is still running; releasing",
				"job", h.job.ID, "max", h.job.MaxRuntime.D())
			d.finishHoldLocked(h, now, model.OutcomeCeiling)
		case h.detected && now.Before(h.hardDeadline(cfg)):
			// Still running, and inside the backstop. Keep holding, and say so
			// once per run rather than every tick.
			if !h.extended {
				h.extended = true
				d.log.Info("job is outlasting its ceiling but still running; holding on",
					"job", h.job.ID, "ceiling", h.ceiling,
					"backstop", cfg.MaxCeiling.D())
			}
		case h.detected:
			// The backstop. This one really is a cut-off, and the only case
			// that should ever be recorded as such.
			d.log.Warn("job passed the maximum hold and is still running; releasing",
				"job", h.job.ID, "max", cfg.MaxCeiling.D())
			d.finishHoldLocked(h, now, model.OutcomeCeiling)
		default:
			d.log.Warn("job was never detected during its window", "job", h.job.ID)
			d.finishHoldLocked(h, now, model.OutcomeNeverDetected)
		}
	}
}

// lateWindow is a closed unobservable window whose scheduler may still
// report a completion; see pollSchedulerState.
type lateWindow struct {
	opened, fired, closed time.Time
}

// finishHoldLocked closes a hold and records it. Caller holds d.mu.
func (d *Daemon) finishHoldLocked(h *hold, now time.Time, outcome model.Outcome) {
	if h.assertion != nil {
		if err := h.assertion.Release(); err != nil {
			d.log.Warn("releasing idle assertion failed", "job", h.job.ID, "err", err)
		}
	}
	delete(d.holds, h.job.ID)
	if !h.manual() {
		d.served[h.job.ID] = h.fireAt
	}
	// Only once nothing else is holding: two jobs in the same window should
	// sleep the machine when the second finishes, not when the first does.
	if len(d.holds) == 0 {
		d.armSleepBack(h, now, outcome)
	}

	// A manual keep-awake window is not a job execution, and stops here.
	//
	// Everything downstream of AppendRun reads a run as evidence about a job:
	// the ceiling estimator learns durations from it, per-job statistics count
	// it, and the never-detected warning counts consecutive misses. A window
	// whose length is a number the user typed is evidence about nothing; it
	// would drag every ceiling towards "however long someone once wanted a
	// coffee break", and it would be filed under a job id that exists in no
	// jobs.json. lastRun is left alone for the same reason: `status` prints it
	// as "last run", and nothing ran.
	//
	// The event log still gets a line, because that is an audit trail of what
	// held the machine awake rather than a record of job behaviour, and a
	// manual hold is exactly the sort of thing you want to find in it later.
	if h.manual() {
		held := now.Sub(h.openedAt)
		d.eventLocked(store.Event{
			Kind: store.EventHoldReleased, JobID: h.job.ID, JobName: h.job.Name,
			Outcome: outcome, Duration: model.Duration(held),
			Message: fmt.Sprintf("manual keep-awake released after %s", model.HumanDuration(held)),
		})
		d.log.Info("keep-awake window closed", "outcome", outcome, "held", held)
		return
	}

	// The level right now is the other end of the measurement. -1 on AC, so a
	// plugged-in run records no cost rather than a fabricated zero drain.
	run := h.finish(now, outcome, batteryLevel(d.lastState))
	d.lastRun = &run

	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		if err := d.store.AppendRun(run); err != nil {
			d.log.Warn("couldn't record run", "job", run.JobID, "err", err)
		}
	}()

	kind := store.EventHoldReleased
	switch outcome {
	case model.OutcomeCeiling:
		kind = store.EventCeilingHit
	case model.OutcomeNeverDetected:
		kind = store.EventNeverDetected
	case model.OutcomeOK, model.OutcomeFailed:
		kind = store.EventJobFinished
	}
	d.eventLocked(store.Event{
		Kind: kind, JobID: h.job.ID, JobName: h.job.Name,
		Outcome:  outcome,
		Duration: run.Duration,
		Message: fmt.Sprintf("ran %s, held %s",
			model.HumanDuration(run.Duration.D()), model.HumanDuration(run.HoldDuration.D())),
	})

	d.log.Info("wake window closed",
		"job", h.job.ID, "outcome", outcome,
		"duration", run.Duration.D(), "held", run.HoldDuration.D())

	// Problems get pushed out; routine success does not.
	//
	// The URL is read directly from d.cfg rather than through webhookURL(),
	// which takes a read lock: this function runs with d.mu already held for
	// writing, and Go's RWMutex is not reentrant, so calling the accessor here
	// deadlocks the daemon. That is not hypothetical; it wedged the process
	// precisely when the ceiling safety valve fired, which is the moment it
	// most needs to keep running.
	switch outcome {
	case model.OutcomeCeiling, model.OutcomeNeverDetected:
		d.hooks.send(d.cfg.WebhookURL, webhookPayload{
			Event: string(kind), Job: h.job.Name, Outcome: string(outcome),
			Message:  fmt.Sprintf("job %q ended as %s", h.job.Name, outcome),
			Duration: run.Duration.String(),
		})
	}
}

// syncSleepBlock pushes the aggregate blocked state to the helper.
//
// The helper is a dumb switch: it is told "blocked" or "not blocked" and
// knows nothing about which jobs are responsible. Aggregating here keeps all
// reference counting in the unprivileged tier.
func (d *Daemon) syncSleepBlock() {
	d.mu.RLock()
	// Any hold blocks sleep, paused or not. Pause stops job wakes and
	// windows from opening, and released every hold it found; the only holds
	// that can exist during a pause are a manual keep-awake or a wrapped job
	// that reported in, both explicit statements that the machine must stay
	// up. Gating on paused here made those holds report "blocked" in status
	// while affirmatively telling the helper "not blocked": the lid closes,
	// the machine sleeps, and the upload the user asked to protect dies.
	want := len(d.holds) > 0
	var names []string
	for _, h := range d.holds {
		names = append(names, h.job.Name)
	}
	d.mu.RUnlock()

	sort.Strings(names)
	reason := "idle"
	if len(names) > 0 {
		reason = "jobs: " + joinNames(names)
	}
	// Queued, not sent. This runs on the two-second poll, and sending from
	// here is what let a helper that had stopped answering freeze the loop.
	d.helper.Push(want, reason)
}

func (d *Daemon) evaluateCutouts(st power.State, cfg config.Config, now time.Time) {
	d.mu.Lock()
	holding := len(d.holds) > 0
	d.mu.Unlock()

	if d.latch.Update(st, cfg) {
		d.log.Info("safety cutout cleared; holds may resume")
		d.mu.Lock()
		d.lastCut = nil
		d.mu.Unlock()
	}
	dec := EvaluateCutout(st, cfg, holding)
	if !dec.Fire || d.latch.Blocking() {
		return
	}

	d.mu.Lock()
	released := len(d.holds)
	for _, h := range d.holds {
		d.finishHoldLocked(h, now, model.OutcomeCutout)
	}
	c := d.latch.Engage(dec, released, now)
	d.lastCut = c
	d.mu.Unlock()

	d.log.Error("safety cutout fired", "kind", dec.Kind, "detail", dec.Detail, "released", released)
	d.event(store.Event{
		Kind: store.EventCutout, Message: dec.Detail,
		LidClosed: boolPtr(st.LidClosed), TempC: st.TempC, BatteryPct: intPtr(st.BatteryPct),
	})
	d.hooks.send(d.webhookURL(), webhookPayload{
		Event: "cutout", Outcome: string(dec.Kind), Message: dec.Detail,
	})
	d.syncSleepBlock()
}

func (d *Daemon) shutdown() {
	now := time.Now()
	d.mu.Lock()
	for _, h := range d.holds {
		d.finishHoldLocked(h, now, model.OutcomeCutout)
	}
	d.mu.Unlock()

	// Clear the privileged block explicitly. disablesleep is global and is
	// NOT cleared when this process exits, so skipping this would leave the
	// machine unable to sleep until something else fixed it.
	//
	// After the pump has stopped, or this can be overtaken by a push that was
	// already in flight and the machine is left unable to sleep by the very
	// call meant to free it.
	d.helper.Settle(2 * time.Second)
	if err := d.helper.SetBlocked(false, "daemon shutting down"); err != nil {
		d.log.Warn("couldn't clear sleep block on shutdown", "err", err)
	}
	// Wait for pending run and event writes so the last window of a session
	// is durable, then record the stop.
	d.bg.Wait()
	d.event(store.Event{Kind: store.EventDaemonStop})
	power.Close()
	d.log.Info("daemon stopped")
}

// reload re-reads config and jobs from disk.
func (d *Daemon) reload() error {
	cfg, warn, err := config.Load(d.store.Layout().ConfigFile())
	if err != nil {
		return err
	}
	if err := d.store.Load(); err != nil {
		return err
	}
	d.mu.Lock()
	d.cfg, d.cfgWarn = cfg, warn
	d.mu.Unlock()
	for _, w := range warn {
		d.log.Warn("config adjusted on load", "detail", w)
	}
	return nil
}

func (d *Daemon) holdsSnapshot() []*hold {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]*hold, 0, len(d.holds))
	for _, h := range d.holds {
		out = append(out, h)
	}
	return out
}

func (d *Daemon) webhookURL() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg.WebhookURL
}

func (d *Daemon) event(e store.Event) {
	d.mu.RLock()
	maxBytes := d.cfg.EventLogMaxBytes
	d.mu.RUnlock()
	if err := d.store.LogEvent(e, maxBytes); err != nil {
		d.log.Debug("event log write failed", "err", err)
	}
}

// eventLocked logs without re-taking d.mu, for callers that already hold it.
func (d *Daemon) eventLocked(e store.Event) {
	maxBytes := d.cfg.EventLogMaxBytes
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		if err := d.store.LogEvent(e, maxBytes); err != nil {
			d.log.Debug("event log write failed", "err", err)
		}
	}()
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func joinNames(n []string) string {
	switch len(n) {
	case 0:
		return ""
	case 1:
		return n[0]
	default:
		out := n[0]
		for _, s := range n[1:] {
			out += ", " + s
		}
		return out
	}
}

var errNoSuchJob = errors.New("no such job")

// resolveObservers indexes the run-state observers available on this machine.
func resolveObservers() map[string]scan.RunObserver {
	out := map[string]scan.RunObserver{}
	for _, o := range scan.Observers() {
		out[o.Name()] = o
	}
	return out
}

// pollSchedulerState reads run state from schedulers that keep their own.
//
// This is what makes an in-process scheduler measurable. Hermes runs its cron
// jobs inside the gateway, so no process ever appears or exits for one and
// process watching has nothing to look at, but it writes a claim when a run
// is dispatched and a completion timestamp when it ends. Reading that turns a
// blind fixed window into a real start, a real end, and a real duration, for
// jobs the user never had to touch.
//
// Jobs stay registered as DetectNone: nothing is migrated, and if the observer
// stops working the behaviour falls straight back to the fixed window it had
// before. That is the whole degradation story: an observer that cannot make
// sense of what it reads returns false, and this does nothing.
func (d *Daemon) pollSchedulerState(ctx context.Context, now time.Time) {
	if len(d.observers) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, h := range d.holds {
		if h.manual() || h.job == nil {
			continue
		}
		obs, ok := d.observers[h.job.Source]
		if !ok {
			continue
		}
		rec, ok := obs.ObserveRun(ctx, h.job.ID)
		if !ok {
			continue // unreadable: leave the fixed window alone
		}

		// Started. The claim carries its own timestamp; fall back to now if
		// the scheduler does not record one, which is still far better than
		// treating the whole window as runtime.
		if rec.Running && !h.detected {
			h.detected = true
			h.startedAt = rec.StartedAt
			if h.startedAt.IsZero() || h.startedAt.After(now) {
				h.startedAt = now
			}
			d.log.Info("scheduler reports the job started",
				"job", h.job.ID, "source", h.job.Source)
			d.eventLocked(store.Event{
				Kind: store.EventJobStarted, JobID: h.job.ID, JobName: h.job.Name,
				Message: "reported by " + h.job.Source,
			})
			continue
		}

		// Finished. The completion must be *newer than this window*, or a
		// record left over from the previous run would close the hold the
		// instant it opened.
		if !rec.Running && rec.CompletedAt.After(h.openedAt) {
			if !h.detected {
				// No start was ever observed, so it has to be inferred.
				//
				// This is the normal case, not an edge one: hermes only stamps
				// `fire_claim` for externally-dispatched runs, so a job fired
				// by its own in-process ticker goes from idle to complete with
				// nothing in between. Verified live: a job that had just run
				// still read `fire_claim: None`.
				//
				// The floor is the *fire time*, not the window opening. The
				// window opens a wake buffer early (2 minutes by default) and
				// the job cannot have started before it was due, so measuring
				// from the opening charges every run the whole buffer: a real
				// 17-second run was recorded as 2m25s, which would then teach
				// the estimator that the job takes as long as the window.
				h.detected = true
				h.startedAt = h.fireAt
				if h.startedAt.Before(h.openedAt) || h.startedAt.After(rec.CompletedAt) {
					h.startedAt = h.openedAt
				}
				if !rec.StartedAt.IsZero() && rec.StartedAt.After(h.startedAt) {
					h.startedAt = rec.StartedAt
				}
			}
			h.endedAt = rec.CompletedAt
			outcome := model.OutcomeOK
			if rec.Status == "error" {
				outcome = model.OutcomeFailed
			}
			d.log.Info("scheduler reports the job finished",
				"job", h.job.ID, "source", h.job.Source,
				"ran", rec.CompletedAt.Sub(h.startedAt), "status", rec.Status)
			d.finishHoldLocked(h, now, outcome)
		}
	}

	// Second pass: windows that already closed at their ceiling with the job
	// unseen. A run that outgrew its learned window finishes after the
	// release; its completion record is the only evidence the ceiling is too
	// small, and skipping it left the estimator stuck at the old number with
	// the machine sleeping out from under the job identically every night.
	// The extra run recorded here is what teaches the estimator the truth.
	for id, w := range d.lateWatch {
		if now.Sub(w.closed) > time.Hour {
			delete(d.lateWatch, id)
			continue
		}
		job, ok := d.store.Job(id)
		if !ok {
			delete(d.lateWatch, id)
			continue
		}
		obs, ok := d.observers[job.Source]
		if !ok {
			delete(d.lateWatch, id)
			continue
		}
		rec, ok := obs.ObserveRun(ctx, id)
		if !ok || rec.Running || !rec.CompletedAt.After(w.opened) {
			continue // nothing new yet; keep watching
		}
		delete(d.lateWatch, id)

		started := rec.StartedAt
		if started.IsZero() || started.Before(w.opened) || started.After(rec.CompletedAt) {
			// Same floor as the in-window path: the job cannot have started
			// before it was due.
			started = w.fired
		}
		outcome := model.OutcomeOK
		if rec.Status == "error" {
			outcome = model.OutcomeFailed
		}
		run := model.Run{
			JobID:        id,
			WindowOpened: w.opened,
			Started:      started,
			Ended:        rec.CompletedAt,
			Duration:     model.Duration(rec.CompletedAt.Sub(started)),
			HoldDuration: model.Duration(w.closed.Sub(w.opened)),
			BatteryStart: -1, BatteryEnd: -1,
			Outcome:   outcome,
			Detection: job.Detection,
		}
		d.log.Info("job finished after its window closed; learning the real duration",
			"job", id, "ran", run.Duration.D(), "window", run.HoldDuration.D())
		d.eventLocked(store.Event{
			Kind: store.EventJobFinished, JobID: id, JobName: job.Name,
			Outcome: outcome, Duration: run.Duration,
			Message: "completed after the window closed; the next window will be sized to it",
		})
		d.bg.Add(1)
		go func() {
			defer d.bg.Done()
			if err := d.store.AppendRun(run); err != nil {
				d.log.Warn("couldn't record late completion", "job", run.JobID, "err", err)
			}
		}()
	}
}

// batteryLevel is the charge to record against a hold, or -1 when there is no
// cost to measure.
//
// On AC the battery is not paying for the hold, so recording a percentage
// there would attribute a cost to the wrong thing: a charging machine can even
// gain charge across a window, which as a "drain" is nonsense.
func batteryLevel(st power.State) int {
	if st.OnAC || st.BatteryPct < 0 {
		return -1
	}
	return st.BatteryPct
}
