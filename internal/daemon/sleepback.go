package daemon

import (
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// Putting the machine back to sleep after goguma has woken it.
//
// goguma cannot ask for a quiet wake. `pmset schedule` produces a wake macOS
// classifies as user-initiated: the log shows powerd taking a `UserIsActive`
// assertion the instant it lands, and the whole desktop stack comes up with
// it. Its own maintenance wakes are a different, constrained class, and on the
// machine this was measured on all eight of them returned to sleep after 44 to
// 46 seconds, without exception.
//
// goguma's wake did not. Seven seconds in, and eighty-four seconds before the
// job even ran, cloudd began taking `SystemIsActive` assertions; it took 160
// of them over the following six hours and the Mac never slept again. goguma
// had released its own hold after 2m 1s and behaved correctly throughout.
//
// The wake buffer is not the cause and shortening it would fix nothing, since
// cloudd was already running before the job started. The cause is the class of
// wake, and no third party can request the other one. That leaves goguma as
// the only thing in a position to undo its own wake, because it is the only
// thing that knows the machine came up for a 31-second job rather than for a
// person.

// sleepBackGrace is how long to wait after the last hold closes before sleeping.
//
// Long enough that a job which spawns a follow-up, or a scheduler about to fire
// a second job in the same minute, is not cut off at the knees. Short enough
// that the window handed to opportunistic background work stays small: cloudd
// took its first assertion seven seconds after the wake, so anything much
// longer than this is simply donating time to the problem being fixed.
const sleepBackGrace = 45 * time.Second

// sleepBackUserIdle is how long the keyboard and pointer must have been quiet.
//
// The machine woke on its own at an hour nobody asked about, so any input at
// all means somebody is present and sleeping the machine under them would be
// hostile. Two minutes rather than seconds because a person reading the screen
// is still a person.
const sleepBackUserIdle = 2 * time.Minute

// sleepBackWakeMargin is how close the next wake may be before sleeping is not
// worth it. Sleeping four seconds before the alarm is churn, not thrift.
const sleepBackWakeMargin = 90 * time.Second

// armSleepBack records that goguma finished with a wake it caused itself.
//
// Called with d.mu held, from the hold's close path. It only notes the
// intention; every condition is re-checked when it fires, because the useful
// ones (is anyone there, is another job due) can all change during the grace
// period.
func (d *Daemon) armSleepBack(h *hold, now time.Time, outcome model.Outcome) {
	if h.manual() {
		return
	}

	// Only when goguma watched the job finish.
	//
	// Forcing sleep is not the same as declining to hold. A window can close
	// for three reasons that are not "the job is done": it hit its ceiling
	// while the job was demonstrably still running, it never saw the job at
	// all, or it was a wake-only window that held a fixed stretch and observed
	// nothing either way. Sleeping the machine on any of those puts a laptop
	// to sleep on top of work that may still be going.
	//
	// Wake-only is the one worth being explicit about, because it is the
	// commonest kind of adopted job. Its window is a guess with a default of
	// three minutes, and a backup that takes four would be suspended by the
	// very tool that woke the machine to run it. Before this feature the
	// machine merely stopped being held and often stayed up anyway; now the
	// sleep would be deliberate, which is a worse thing to be wrong about.
	//
	// So an unwatched job never triggers this, and wrapping it in `goguma-mark`
	// is what earns it: with a real start and exit, "the job is done" stops
	// being an assumption.
	if !h.detected {
		return
	}
	if outcome != model.OutcomeOK && outcome != model.OutcomeFailed {
		return
	}
	// Either signal will do, and they fail in different ways. wokeMachine is
	// inferred from a gap in the daemon's ticks and misses a wake out of a
	// light sleep. followsWake is the kernel's own last-wake time and misses
	// nothing on macOS, but is unavailable on Linux. Neither can invent a wake
	// that did not happen, and the user-presence check below is what actually
	// guards against sleeping a machine somebody is using.
	if !h.wokeMachine && !h.followsWake {
		return
	}
	d.sleepBackAt = now.Add(sleepBackGrace)
	d.sleepBackJob = h.job.Name
}

// disarmSleepBack cancels a pending sleep. Called with d.mu held whenever
// something happens that makes sleeping wrong: a new hold, a resume, a pause.
func (d *Daemon) disarmSleepBack() {
	d.sleepBackAt = time.Time{}
	d.sleepBackJob = ""
}

// maybeSleepBack sleeps the machine if goguma woke it and is done with it.
//
// Every check here is a reason not to sleep, and the order is deliberate:
// cheap and local first, the two that shell out last.
func (d *Daemon) maybeSleepBack(now time.Time, cfg config.Config) {
	d.mu.Lock()
	due := d.sleepBackAt
	job := d.sleepBackJob
	holds := len(d.holds)
	paused := d.paused
	d.mu.Unlock()

	if due.IsZero() || now.Before(due) {
		return
	}
	if !cfg.SleepAfterWake {
		d.mu.Lock()
		d.disarmSleepBack()
		d.mu.Unlock()
		return
	}

	// Something opened a hold during the grace period, so the machine is
	// wanted awake and this is no longer goguma's to decide.
	if holds > 0 || paused {
		d.mu.Lock()
		d.disarmSleepBack()
		d.mu.Unlock()
		return
	}

	// Sleeping into an alarm that is about to fire achieves nothing and costs a
	// full wake cycle.
	d.mu.RLock()
	nextWake := d.nextWake
	d.mu.RUnlock()
	if nextWake != nil && nextWake.Sub(now) < sleepBackWakeMargin {
		d.mu.Lock()
		d.disarmSleepBack()
		d.mu.Unlock()
		d.log.Debug("not sleeping back, the next wake is imminent", "next", *nextWake)
		return
	}

	// Is anybody there?
	//
	// An error is "cannot tell", never "nobody". On a platform with no idle
	// source this declines every time, which is the right way to be wrong: the
	// cost of not sleeping is some battery, and the cost of sleeping under
	// somebody's hands is their machine going dark mid-sentence.
	idle, err := d.plat.UserIdle()
	if err != nil {
		d.mu.Lock()
		d.disarmSleepBack()
		d.mu.Unlock()
		d.log.Debug("not sleeping back, cannot read user idle time", "err", err)
		return
	}
	if idle < sleepBackUserIdle {
		d.mu.Lock()
		d.disarmSleepBack()
		d.mu.Unlock()
		d.log.Info("not sleeping back, someone is using the machine", "idle", idle)
		return
	}

	d.mu.Lock()
	d.disarmSleepBack()
	d.mu.Unlock()

	if err := d.plat.SleepNow(); err != nil {
		// Not fatal and not retried. The machine stays awake, which is exactly
		// where it was before this existed.
		d.log.Warn("couldn't put the machine back to sleep", "err", err)
		return
	}
	d.log.Info("slept the machine back down after a wake goguma caused",
		"job", job, "idle", idle)
}

// wakeIsRecent is how close the kernel's last-wake time must be to a window
// opening for the window to count as following that wake.
//
// The wake buffer is 90s by default and the window opens on it, so this has to
// clear that with room for a slow wake. Two minutes does, and is still far
// short of any plausible "the user opened the lid earlier and wandered off".
const wakeIsRecent = 3 * time.Minute

// followsRecentWake reports whether the machine woke just before now,
// according to the kernel rather than to goguma's own tick history.
//
// An error, or no wake since boot, is false: unable to tell means do not act.
func (d *Daemon) followsRecentWake(now time.Time) bool {
	woke, err := d.plat.LastWakeAt()
	if err != nil || woke.IsZero() {
		return false
	}
	since := now.Sub(woke)
	return since >= 0 && since < wakeIsRecent
}
