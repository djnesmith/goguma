package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/render"
	"github.com/junnam586/goguma/internal/schedule"
	"github.com/junnam586/goguma/internal/store"
)

var cmdDoctor = &Command{
	Name:    "doctor",
	Summary: "diagnose why goguma might not be working",
	Usage: `goguma doctor [job]

Checks every link in the chain that has to work for a job to survive sleep,
and reports which one is broken.

The failure this exists to catch is the silent one: a job that is registered,
looks correct in 'goguma list', and quietly never runs.`,
	Run: runDoctor,
}

type check struct {
	name   string
	status checkStatus
	detail string
	fix    string
}

type checkStatus int

const (
	checkOK checkStatus = iota
	checkWarn
	checkFail
	checkSkip
)

func runDoctor(ctx *Context, args []string) error {
	r := ctx.Out
	var checks []check

	// The chain, in the order it has to work: binaries exist, the service runs,
	// helper answers, wake registers, jobs are detected.
	checks = append(checks, checkBinaries(ctx)...)

	st, statusErr := fetchStatus(ctx)
	if statusErr != nil {
		checks = append(checks, check{
			name:   "background service",
			status: checkFail,
			detail: "not running, so nothing is being scheduled",
			fix:    "goguma install",
		})
	} else {
		checks = append(checks, check{
			name: "background service", status: checkOK,
			detail: fmt.Sprintf("running, version %s", st.DaemonVersion),
		})
		checks = append(checks, checkHelper(st))
		checks = append(checks, checkWake(st))
		checks = append(checks, checkThermal(st))
	}

	checks = append(checks, checkPlatform()...)
	checks = append(checks, checkInvalidEntries(ctx)...)

	if statusErr == nil {
		// State-level problems come before per-job ones. A jobs.json that will
		// not parse means there are no jobs to check, and reporting "none
		// registered, run import" would send the user to add jobs on top of a
		// file that cannot be written.
		checks = append(checks, checkStateFiles(st)...)
		if !hasBrokenJobFile(st) {
			checks = append(checks, checkJobs(ctx, args)...)
		}
	}

	// Report.
	var failed, warned int
	for _, c := range checks {
		var mark string
		switch c.status {
		case checkOK:
			mark = r.Good(r.Sym().OK)
		case checkWarn:
			mark = r.Warn(r.Sym().Warn)
			warned++
		case checkFail:
			mark = r.Danger(r.Sym().Danger)
			failed++
		case checkSkip:
			mark = r.Muted(r.Sym().Disabled)
		}
		r.Printf("%s %-22s %s\n", mark, c.name, c.detail)
		if c.fix != "" {
			r.Printf("    %s %s\n", r.Muted("fix:"), r.Accent(c.fix))
		}
	}

	r.Blank()
	switch {
	case failed > 0:
		r.Printf("%s %d check(s) failed.\n", r.Danger(r.Sym().Danger), failed)
		return fmt.Errorf("goguma is not fully working")
	case warned > 0:
		r.Printf("%s working, with %d thing(s) worth attention.\n", r.Warn(r.Sym().Warn), warned)
	default:
		r.Printf("%s everything checks out.\n", r.Good(r.Sym().OK))
	}
	return nil
}

// checkInvalidEntries reports jobs.json entries that parsed but failed
// validation. The store quarantines them rather than erasing them (they are
// the user's own data mid-typo, kept in the file untouched), and this is
// the promised place a human learns which entries are broken and why. Read
// directly from disk so it works whether or not the daemon is up.
func checkInvalidEntries(ctx *Context) []check {
	var out []check
	for _, invalid := range store.New(ctx.Layout).InvalidJobs() {
		out = append(out, check{
			name: "jobs.json", status: checkFail,
			detail: invalid.Error(),
			fix:    "edit " + ctx.Layout.JobsFile(),
		})
	}
	return out
}

// checkStateFiles surfaces problems with the daemon's own files, which the
// daemon reports as warnings because it can still run without them.
func checkStateFiles(st model.Status) []check {
	var out []check
	for _, w := range st.Warnings {
		if w.Kind != model.WarnScheduleParse || w.JobID != "" {
			continue
		}
		out = append(out, check{
			name: "state files", status: checkFail,
			detail: w.Message, fix: w.Fix,
		})
	}
	return out
}

// hasBrokenJobFile reports that the job list is empty because the file could
// not be read, rather than because nothing is registered.
func hasBrokenJobFile(st model.Status) bool {
	for _, w := range st.Warnings {
		if w.Kind == model.WarnScheduleParse && w.JobID == "" &&
			strings.Contains(w.Message, "jobs.json") {
			return true
		}
	}
	return false
}

func checkBinaries(ctx *Context) []check {
	var out []check
	for _, name := range []string{"goguma-daemon", "goguma-mark"} {
		p := filepath.Join(ctx.Layout.BinDir, name)
		if _, err := os.Stat(p); err != nil {
			out = append(out, check{
				name: name, status: checkFail,
				detail: "not installed at " + p,
				fix:    "goguma install",
			})
			continue
		}
		out = append(out, check{name: name, status: checkOK, detail: p})
	}
	return out
}

func checkHelper(st model.Status) check {
	if st.HelperConnected {
		return check{
			name: "privileged helper", status: checkOK,
			detail: fmt.Sprintf("connected, version %s", st.HelperVersion),
		}
	}
	return check{
		name: "privileged helper", status: checkFail,
		detail: "not reachable, lid-closed holds and OS wakes will not work",
		fix:    "goguma install",
	}
}

func checkWake(st model.Status) check {
	switch {
	case st.Paused:
		return check{name: "scheduled wake", status: checkSkip, detail: "paused"}
	case st.WakeSuppressed != "":
		// Working as intended: report it as a skip rather than a warning, so
		// a safeguard doing its job does not look like a fault.
		return check{
			name: "scheduled wake", status: checkSkip,
			detail: "held back · " + st.WakeSuppressed,
		}
	case st.NextWake == nil:
		return check{
			name: "scheduled wake", status: checkWarn,
			detail: "nothing scheduled, no enabled job has an upcoming run",
		}
	case !st.WakeScheduled:
		return check{
			name: "scheduled wake", status: checkFail,
			detail: "the OS did not accept the wake: " + st.WakeError,
			fix:    "check 'pmset -g sched' for a competing entry",
		}
	default:
		return check{
			name: "scheduled wake", status: checkOK,
			detail: fmt.Sprintf("%s for %s",
				st.NextWake.Local().Format("Mon 2 Jan 15:04:05"), st.NextJob),
		}
	}
}

func checkThermal(st model.Status) check {
	if st.Power.CPUTempC == nil {
		return check{
			name: "thermal cutout", status: checkWarn,
			detail: "no temperature sensor is readable, so the cutout cannot arm",
		}
	}
	return check{
		name: "thermal cutout", status: checkOK,
		detail: fmt.Sprintf("armed, currently %.0f°C via %s",
			*st.Power.CPUTempC, st.Power.ThermalSource),
	}
}

func checkPlatform() []check {
	p := power.New()
	ok, reason := p.WakeScheduleSupported()
	if !ok {
		return []check{{
			name: "wake support", status: checkFail,
			detail: "this machine cannot schedule a wake: " + reason,
		}}
	}
	out := []check{{name: "wake support", status: checkOK, detail: "available on " + p.Name()}}

	if !p.SupportsClamshellHold() {
		out = append(out, check{
			name: "lid-closed hold", status: checkWarn,
			detail: "not supported on this platform; jobs will only survive with the lid open",
		})
	}
	return out
}

// checkJobs verifies each job's configuration end to end.
func checkJobs(ctx *Context, args []string) []check {
	var resp ipc.JobsListResp
	if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
		return nil
	}
	if len(resp.Jobs) == 0 {
		return []check{{
			name: "jobs", status: checkWarn,
			detail: "none registered, so nothing will wake this machine",
			fix:    "goguma import",
		}}
	}

	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}

	var out []check
	for _, v := range resp.Jobs {
		if filter != "" && v.Job.ID != filter && v.Job.Name != filter {
			continue
		}
		out = append(out, checkOneJob(ctx, v))
	}
	return out
}

func checkOneJob(ctx *Context, v ipc.JobView) check {
	name := "job: " + v.Job.Name

	if v.ScheduleError != "" {
		return check{
			name: name, status: checkFail,
			detail: "its schedule will not parse: " + v.ScheduleError,
			fix:    fmt.Sprintf("goguma edit %s --cron '<expression>'", v.Job.ID),
		}
	}
	if !v.Job.Enabled {
		return check{name: name, status: checkSkip, detail: "disabled"}
	}

	// The high-value check: a job whose recent windows never saw the job.
	if v.Stats.NeverSeen > 0 && v.Stats.Runs > 0 {
		detail := fmt.Sprintf("%d of %d windows never detected it · the machine woke for nothing",
			v.Stats.NeverSeen, v.Stats.Runs)
		fix := fmt.Sprintf("goguma doctor %s", v.Job.ID)
		if v.Job.Detection == model.DetectPattern {
			fix = fmt.Sprintf("goguma test-match %q", v.Job.Match)
		} else {
			fix = fmt.Sprintf("make sure the crontab line starts with: goguma-mark %s --", v.Job.ID)
		}
		return check{name: name, status: checkFail, detail: detail, fix: fix}
	}

	if v.Job.Detection == model.DetectPattern {
		var resp ipc.MatchTestResp
		if err := callDaemon(ctx, ipc.OpTestMatch,
			ipc.MatchTestReq{Pattern: v.Job.Match}, &resp); err == nil && !resp.Valid {
			return check{
				name: name, status: checkFail,
				detail: "its match pattern is not a valid regular expression: " + resp.Error,
				fix:    fmt.Sprintf("goguma edit %s --match '<pattern>'", v.Job.ID),
			}
		}
	}

	if v.Stats.CeilingHits > 0 {
		return check{
			name: name, status: checkWarn,
			detail: fmt.Sprintf("hit its ceiling %d time(s), so it was cut off mid-run",
				v.Stats.CeilingHits),
			fix: fmt.Sprintf("goguma edit %s --max-runtime <duration>", v.Job.ID),
		}
	}

	detail := "ok"
	if v.NextFire != nil {
		detail = "next run " + model.HumanUntil(*v.NextFire, time.Now())
	}
	if v.Stats.ColdStart {
		detail += ", still learning its duration"
	}

	// Say which part of that time is a guess.
	//
	// An interval schedule names a cadence and no time of day, so its phase
	// has to come from somewhere else: goguma counts from when the job was
	// added. For a job adopted from another scheduler that is when goguma
	// first saw it, not when that scheduler's own interval clock started, so
	// the predicted time can be up to one full interval away from the real
	// one. Nothing is broken and there is no command that fixes it, which is
	// why this qualifies the line instead of becoming a warning, but doctor
	// is where someone comes to ask whether a job is really covered, and the
	// honest answer here is "on this cadence, at an estimated time".
	if s, err := schedule.ParseAt(v.Job.Schedule, v.Job.Location(), v.Job.ScheduleAnchor()); err == nil {
		if d, anchored := s.AnchoredInterval(); anchored {
			detail += fmt.Sprintf(", timed from when the job was added so it may be up to %s out of phase",
				model.HumanDuration(d))
		}
	}
	return check{name: name, status: checkOK, detail: detail}
}

// unusedContext keeps the import list honest if checks are added later.
var _ = context.Background
var _ = render.Truncate
