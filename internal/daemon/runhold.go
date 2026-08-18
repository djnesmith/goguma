package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/store"
)

// Holds for commands the CLI is wrapping: `goguma run -- <command>`.
//
// This is the answer to a job whose length nobody can predict and no schedule
// describes. A coding agent runs for as long as it runs; a build takes what it
// takes. Neither has a fire time to wake for, and neither can be watched from
// outside, because an agent waiting on a model is a process blocked on a
// socket and looks exactly like one doing nothing. Measured on one machine, an
// idle agent used more CPU over ten seconds than the one actively working.
//
// So the command announces itself, the way goguma-mark already does for a
// scheduled job, and the hold lasts exactly as long as the process does.

// runLease is how long a run hold survives with no renewal.
//
// The lease is the whole safety design. `goguma run` sends run.end when the
// command exits, but a wrapper that is SIGKILLed, or whose machine loses power
// mid-run, never gets to. A hold with no one left able to close it is the
// failure the safety chapter exists to prevent, so instead of trusting the
// wrapper to clean up, the hold expires by default and survives only while
// something keeps saying the command is alive.
//
// Ninety seconds is a compromise between a stranded hold lingering and a
// renewal being missed because the machine was briefly busy. The wrapper
// renews at a third of it, so two consecutive renewals must fail before a live
// command loses its hold.
const runLease = 90 * time.Second

// maxRun bounds the total life of a run hold however often it is renewed.
//
// The lease alone covers a dead wrapper, not a live one attached to something
// that will never finish. This is the same reasoning as MaxKeepAwake, and the
// same number: a wrapped command still going after twelve hours is not slow,
// and whatever it is doing should not silently be the reason a laptop never
// slept.
const maxRun = 12 * time.Hour

// agentLease is the default for a keyed hold, and the reason keyed holds take a
// lease at all rather than inheriting runLease.
//
// `goguma run` renews on a timer it controls, so ninety seconds is generous. An
// agent harness renews on events it does not control: a hook fires when a
// prompt is submitted and when a tool call finishes, and between those a model
// can think for minutes with nothing happening locally at all. A ninety-second
// lease would drop the hold in the middle of exactly the work this exists for.
//
// Fifteen minutes is longer than any inference gap seen in practice and is also
// the worst case for a hold outliving its agent, which only happens when the
// harness dies without firing its stop hook. The ordinary path does not rely on
// it: the stop hook releases immediately.
const agentLease = 15 * time.Minute

// maxLease bounds what a caller may ask for, since the request carries it.
const maxLease = 30 * time.Minute

// keyedRunID is the hold id for a named hold. The `k:` segment keeps the keyed
// namespace clear of the counter that names anonymous ones, so a caller passing
// the key "7" cannot collide with the seventh `goguma run`.
func keyedRunID(key string) string { return model.RunHoldPrefix + "k:" + key }

// runHoldJob is the synthetic job a wrapped command's hold hangs off.
//
// DetectNone for the same reason keepAwakeJob uses it: hold.deadline gives a
// wake-only hold a fixed window from fireAt, which is exactly a lease. Renewing
// moves fireAt forward. Nothing observes the process, and nothing needs to,
// because the wrapper is the observer.
//
// Never handed to the store, so it reaches neither jobs.json, the job list, nor
// the miss-risk model, all of which read registered jobs rather than holds.
func runHoldJob(id, label string) *model.Job {
	name := strings.TrimSpace(label)
	if name == "" {
		name = "wrapped command"
	}
	return &model.Job{ID: id, Name: name, Detection: model.DetectNone}
}

// StartRun opens a hold for a command the CLI is about to run, or renews the
// keyed hold that command already has.
func (d *Daemon) StartRun(req ipc.RunStartReq, now time.Time) (ipc.RunStartResp, error) {
	label, key := req.Label, strings.TrimSpace(req.Key)

	lease := runLease
	if key != "" {
		lease = agentLease
	}
	if want := req.Lease.D(); want > 0 {
		lease = min(want, maxLease)
	}

	// A keyed hold that is already open is renewed rather than replaced. This
	// is the whole point of the key: an agent reports the same session on every
	// prompt and every tool call, and each of those must land on the hold that
	// session already has. Done before the cutout check on purpose, because
	// refusing to renew a hold that is already open would drop an agent
	// mid-run, and the cutout has its own machinery for releasing what is
	// already held.
	if key != "" {
		id := keyedRunID(key)
		d.mu.Lock()
		if h, ok := d.holds[id]; ok {
			limit := h.openedAt.Add(maxRun)
			next := now
			if next.After(limit) {
				next = limit
			}
			h.fireAt, h.ceiling = next, lease
			d.mu.Unlock()
			return ipc.RunStartResp{ID: id, Lease: model.Duration(lease)}, nil
		}
		d.mu.Unlock()
	}
	return d.openRunHold(label, key, lease, now)
}

// openRunHold creates a new run hold.
func (d *Daemon) openRunHold(label, key string, lease time.Duration, now time.Time) (ipc.RunStartResp, error) {
	// Refuse while a cutout is latched, exactly as KeepAwake does: once the
	// latch is engaged evaluateCutouts stops firing, so a hold opened now
	// would sit out its lease on a machine already judged too hot or too flat.
	d.mu.RLock()
	cut := d.latch.Active()
	d.mu.RUnlock()
	if cut != nil {
		return ipc.RunStartResp{}, fmt.Errorf(
			"a safety cutout is active (%s); holds stay suspended until conditions recover", cut.Detail)
	}

	var id string
	if key != "" {
		id = keyedRunID(key)
	} else {
		d.mu.Lock()
		d.runSeq++
		id = fmt.Sprintf("%s%d", model.RunHoldPrefix, d.runSeq)
		d.mu.Unlock()
	}

	job := runHoldJob(id, label)

	// Assertion first, outside the lock: HoldIdleSleep is the slow part and
	// holding d.mu across it stalls every tick and status call.
	h := &hold{job: job, fireAt: now, openedAt: now, ceiling: lease,
		batteryStart: batteryLevel(d.lastState)}
	if a, err := d.plat.HoldIdleSleep("goguma: " + job.Name); err != nil {
		d.log.Error("couldn't hold idle sleep for a wrapped command", "err", err)
	} else {
		h.assertion = a
	}

	d.mu.Lock()
	// Lost a race with a concurrent open for the same key: keep the one that
	// is already there, and release this assertion rather than orphaning it.
	if prev, exists := d.holds[id]; exists {
		d.mu.Unlock()
		if h.assertion != nil {
			_ = h.assertion.Release()
		}
		return ipc.RunStartResp{ID: id, Lease: model.Duration(prev.ceiling)}, nil
	}
	d.holds[id] = h
	d.mu.Unlock()

	d.syncSleepBlock()
	d.log.Info("run hold opened", "id", id, "label", job.Name, "lease", lease)
	d.event(store.Event{
		Kind: store.EventWindowOpened, JobID: id, JobName: job.Name,
		Message: "holding sleep off for a wrapped command",
	})
	return ipc.RunStartResp{ID: id, Lease: model.Duration(lease)}, nil
}

// RenewRun extends a run hold's lease from now, reporting whether there was
// still a hold to extend.
//
// Returning false rather than reopening is deliberate. A lapsed hold means the
// wrapper stopped being able to reach the daemon for longer than the lease, and
// silently resurrecting it would hide that from a caller who should say so.
func (d *Daemon) RenewRun(id string, now time.Time) ipc.RunRenewResp {
	d.mu.Lock()
	h, ok := d.holds[id]
	if !ok {
		d.mu.Unlock()
		return ipc.RunRenewResp{Held: false}
	}
	// Never past the absolute bound, however many renewals arrive. Clamping
	// rather than refusing keeps the hold alive to its true end, at which point
	// enforceCeilings closes it on the ordinary path.
	limit := h.openedAt.Add(maxRun)
	next := now
	if next.After(limit) {
		next = limit
	}
	h.fireAt = next
	d.mu.Unlock()
	return ipc.RunRenewResp{Held: true}
}

// EndRun closes a run hold, reporting whether one was open.
func (d *Daemon) EndRun(id string, exitCode *int, now time.Time) bool {
	d.mu.Lock()
	h, ok := d.holds[id]
	if ok {
		d.finishHoldLocked(h, now, model.OutcomeOK)
	}
	d.mu.Unlock()
	if !ok {
		return false
	}
	d.syncSleepBlock()

	msg := "wrapped command finished"
	if exitCode != nil {
		msg = fmt.Sprintf("wrapped command finished (exit %d)", *exitCode)
	}
	d.log.Info("run hold released", "id", id, "held", now.Sub(h.openedAt).Round(time.Second))
	d.event(store.Event{
		Kind: store.EventHoldReleased, JobID: id, JobName: h.job.Name, Message: msg,
	})
	return true
}
