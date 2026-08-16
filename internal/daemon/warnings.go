package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/schedule"
)

func osHostname() (string, error) { return os.Hostname() }

// refreshWarnings recomputes the actionable-problem list surfaced by `status`
// and the menu bar app.
//
// Every warning carries a Fix string that is literally the command to run.
// The failure mode this guards against is a job that is registered, looks
// fine in `list`, and silently never does anything, which without this is
// indistinguishable from working correctly.
func (d *Daemon) refreshWarnings(st power.State, cfg config.Config) {
	var out []model.Warning

	// A wake that could not be registered means every job is at risk, so it
	// leads the list.
	d.mu.RLock()
	wakeErr, nextJob := d.wakeErr, d.nextJob
	holding := len(d.holds) > 0
	cfgWarn := d.cfgWarn
	d.mu.RUnlock()

	// One cause, not its symptoms.
	//
	// Scheduling an OS wake goes through the helper, so a helper that is not
	// reachable makes every wake registration fail, and reporting both reads
	// as two independent problems when there is one, with one fix. Listing the
	// symptom alongside the cause also puts the wrong fix first: `doctor`
	// diagnoses, where `install` is what actually resolves it.
	//
	// The wake failure is still reported whenever the helper is fine, because
	// then it is a genuinely separate fault: pmset refusing the entry, or a
	// competing wake owned by something else.
	helperDown := !d.helper.Connected()

	if wakeErr != "" && !helperDown {
		out = append(out, model.Warning{
			Kind: model.WarnWakeFailed,
			Message: fmt.Sprintf("could not register an OS wake for %q, it will be missed if the machine is asleep: %s",
				nextJob, wakeErr),
			Fix: "goguma doctor",
		})
	}

	if helperDown {
		// Says what is lost, plainly, without the raw error. The failures it
		// causes, wakes that cannot be registered, holds that cannot survive a
		// closed lid, are consequences of this one line and are not repeated
		// as separate warnings.
		msg := "the privileged helper isn't running, so the Mac can't be woken for jobs and " +
			"sleep can't be held with the lid shut. Run the fix in Terminal; it asks for your " +
			"Mac login password"
		// "Not listening" is already what the sentence above says. Appending
		// the raw dial error would read as "the helper is not reachable
		// (goguma daemon is not running)", which names the wrong process
		// and sends the user looking in the wrong place.
		if err := d.helper.LastError(); err != nil && !errors.Is(err, ipc.ErrNotRunning) {
			msg += " (" + err.Error() + ")"
		}
		out = append(out, model.Warning{
			Kind:    model.WarnHelperDown,
			Message: msg,
			Fix:     "goguma install",
		})
	}

	// A thermal cutout that cannot read a temperature is not protecting
	// anything. Say so rather than letting the user assume it is armed.
	if holding && st.LidClosed && st.TempC == nil {
		out = append(out, model.Warning{
			Kind:    model.WarnCeilingHits,
			Message: "no CPU temperature sensor is readable, so the thermal cutout cannot arm while the lid is closed",
		})
	}

	// A job file that would not parse is the loudest possible problem: nothing
	// is scheduled and nothing can be saved until it is fixed.
	if err := d.store.LoadError(); err != nil {
		out = append(out, model.Warning{
			Kind: model.WarnScheduleParse,
			Message: "jobs.json could not be read, so no jobs are registered and " +
				"changes cannot be saved: " + err.Error(),
			Fix: "fix the file or move it aside, then restart the daemon",
		})
	}

	for _, w := range cfgWarn {
		out = append(out, model.Warning{Kind: model.WarnScheduleParse, Message: "config: " + w})
	}

	for _, job := range d.store.Jobs() {
		// Only the error matters here, but the anchor is passed anyway: every
		// site that parses a job's schedule uses the same call, so none of them
		// can drift apart later.
		if _, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); err != nil {
			out = append(out, model.Warning{
				Kind:    model.WarnScheduleParse,
				JobID:   job.ID,
				Message: fmt.Sprintf("job %q has an unparseable schedule and will never run: %v", job.Name, err),
				Fix:     fmt.Sprintf("goguma edit %s --cron '<expression>'", job.ID),
			})
			continue
		}
		if !job.Enabled {
			continue
		}

		runs, err := d.store.Runs(job.ID)
		if err != nil || len(runs) == 0 {
			continue
		}

		// Consecutive never-detected runs are the signature of a match
		// pattern that does not match anything. One can be a fluke; several
		// in a row is a broken configuration.
		missed := trailingNeverDetected(runs)
		if missed >= 2 {
			w := model.Warning{
				Kind:  model.WarnNeverDetected,
				JobID: job.ID,
				Message: fmt.Sprintf(
					"job %q has not been detected in its last %d windows, the machine was woken and held awake for nothing",
					job.Name, missed),
			}
			if job.Detection == model.DetectPattern {
				w.Message += fmt.Sprintf("; the pattern %q may not match the running process", job.Match)
				w.Fix = fmt.Sprintf("goguma test-match %q   # then: goguma edit %s --match '<pattern>'", job.Match, job.ID)
			} else {
				w.Message += "; the wrapper may no longer be in the crontab line"
				w.Fix = fmt.Sprintf("goguma doctor %s", job.ID)
			}
			out = append(out, w)
		}

		if hits := trailingCeilingHits(runs); hits >= 2 {
			out = append(out, model.Warning{
				Kind:  model.WarnCeilingHits,
				JobID: job.ID,
				// This now means the *backstop*, not the estimate. A job that
				// merely runs longer than expected is held on to and learned
				// from; reaching this point means it was still going after the
				// maximum hold, which is a job that hangs rather than a job
				// that is slow.
				Message: fmt.Sprintf(
					"job %q was still running at the maximum hold in the last %d runs, "+
						"so it was released before finishing",
					job.Name, hits),
				Fix: fmt.Sprintf("goguma edit %s --max-runtime <duration>", job.ID),
			})
		}
	}

	// Jobs that vanished from the scheduler that created them.
	//
	// Said out loud rather than left to be noticed. Adoption is silent by
	// design, which is right while jobs are appearing; the same silence when
	// one disappears means the list quietly gets shorter and the machine
	// quietly stops waking for something, and the user finds out by counting
	// rows. There is no fix to offer: the job is gone from its source, which
	// is usually deliberate. This is a notice, not a problem.
	if gone := d.recentRetirements(time.Now()); len(gone) > 0 {
		names := make([]string, 0, 3)
		for _, r := range gone {
			if len(names) == 3 {
				break
			}
			names = append(names, r.Name)
		}
		list := strings.Join(names, ", ")
		if len(gone) > len(names) {
			list += ", …"
		}
		out = append(out, model.Warning{
			Kind: model.WarnRetired,
			Message: fmt.Sprintf(
				"%s %s gone from the scheduler that created %s, so goguma stopped "+
					"waking for %s (%s)",
				pluralJobs(len(gone)), verbIs(len(gone)), objectThem(len(gone)),
				objectThem(len(gone)), list),
		})
	}

	// Jobs being woken for, but on a fixed window rather than their real
	// runtime.
	//
	// This used to describe jobs goguma had found and deliberately not adopted,
	// which made a half-covered install indistinguishable from a complete one.
	// They are adopted now, so this is no longer about coverage: it is the
	// difference between holding a bounded window and holding exactly as long
	// as the job runs. Still worth saying, because the bounded window is what
	// costs battery, but it is an upgrade rather than a gap.
	d.mu.RLock()
	uncovered := d.uncovered
	d.mu.RUnlock()
	if n := len(uncovered); n > 0 {
		names := make([]string, 0, 3)
		for _, c := range uncovered {
			if len(names) == 3 {
				break
			}
			names = append(names, c.Name)
		}
		list := strings.Join(names, ", ")
		if n > len(names) {
			list += ", …"
		}
		out = append(out, model.Warning{
			Kind: model.WarnUncovered,
			Message: fmt.Sprintf(
				"%s %s woken for on a fixed window because the command cannot "+
					"be watched; wrapping %s gives exact timing (%s)",
				pluralJobs(n), verbIs(n), objectThem(n), list),
			Fix: "goguma import",
		})
	}

	// Notices from goguma itself, last.
	//
	// Everything above is a fault on this machine and comes first; a released
	// fix or a known bug is worth saying but must never push a broken helper
	// down the list.
	for _, n := range d.advisoryWarnings() {
		out = append(out, model.Warning{
			Kind:    model.WarnAdvisory,
			Message: n.Message,
			Fix:     n.Fix,
		})
	}

	d.mu.Lock()
	d.warnings = out
	d.mu.Unlock()
}

func trailingNeverDetected(runs []model.Run) int {
	n := 0
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Outcome != model.OutcomeNeverDetected {
			break
		}
		n++
	}
	return n
}

func trailingCeilingHits(runs []model.Run) int {
	n := 0
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Outcome != model.OutcomeCeiling {
			break
		}
		n++
	}
	return n
}

func pluralJobs(n int) string {
	if n == 1 {
		return "1 scheduled job"
	}
	return fmt.Sprintf("%d scheduled jobs", n)
}

func verbIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func verbThey(n int) string {
	if n == 1 {
		return "it is"
	}
	return "they are"
}

// objectThem is the object pronoun, for sentences that do something *to* the
// jobs rather than saying something is happening to them. `verbThey` carries
// its own verb and reads as "wrapping it is gives exact timing" here.
func objectThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
