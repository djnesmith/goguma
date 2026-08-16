package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/schedule"
)

const addUsage = `goguma add --name <name> --cron <schedule> [options]

Registers a schedule that goguma will wake the machine for. goguma does
not run the job; your existing cron or launchd entry still does that.

Detection (how goguma knows the job is running):

  --detection mark      (default, exact)
        Wrap the job so it reports its own start and exit. Change your
        crontab line to:
            goguma-mark <name> -- <your original command>
        This gives an exact duration from the first run and a real exit code.

  --detection pattern   (no changes to your setup)
        goguma watches the process table for --match. Requires no edits,
        but can't see exit codes and silently observes nothing if the
        pattern is wrong. Test it first with 'goguma test-match'.

  --detection none      (for jobs that can't be observed at all)
        No detection. goguma wakes the machine, holds it awake for a fixed
        window, and lets it sleep again when the window ends, whether or not
        the job finished. It costs the most battery of the three, and it is
        the right answer when the job runs inside a process goguma can't
        pick out on its own.

options:
  --name <name>          required, the job's name
  --cron <expr>          required. Cron syntax ("0 9 * * *", "@daily") or
                         plain English ("every day at 9am", "weekdays at 6pm",
                         "every 30 minutes"). Run 'goguma add' with no flags
                         to be asked instead.
  --tz <zone>            IANA timezone the schedule is evaluated in
  --detection <mode>     mark (default), pattern, or none
  --match <regexp>       process pattern, required with --detection pattern
  --command <cmd>        the command this job runs, recorded for reference
  --group <name>         file the job under a heading in 'goguma list'.
                         Organisation only; it changes nothing about when the
                         job runs. Move it later with 'goguma group'.
  --max-runtime <dur>    override the learned ceiling, e.g. 90s, 5m
  --wake-buffer <dur>    how early to wake before the job fires
  --disabled             register without enabling`

var cmdAdd = &Command{
	Name:    "add",
	Summary: "register a job to wake the machine for",
	Usage:   addUsage,
	Run:     runAdd,
}

func runAdd(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		name       = fs.String("name", "", "job name")
		cron       = fs.String("cron", "", "schedule expression")
		tz         = fs.String("tz", "", "IANA timezone")
		detection  = fs.String("detection", "mark", "mark or pattern")
		match      = fs.String("match", "", "process match pattern")
		command    = fs.String("command", "", "the command this job runs")
		group      = fs.String("group", "", "organise the job under this heading")
		maxRuntime = fs.String("max-runtime", "", "ceiling override")
		wakeBuffer = fs.String("wake-buffer", "", "wake this long before firing")
		source     = fs.String("source", "manual", "the scheduler that runs this job")
		disabled   = fs.Bool("disabled", false, "register without enabling")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Missing essentials means the user does not have the flags to hand, which
	// is the normal state for someone who knows what they want to schedule but
	// not how to write it. Ask, rather than printing usage at them.
	if (*name == "" || *cron == "") && interactivePossible() {
		if err := promptForJob(ctx, name, cron, detection, match, command); err != nil {
			return err
		}
	}
	if *name == "" || *cron == "" {
		return fmt.Errorf("--name and --cron are both required\n\n%s", addUsage)
	}

	job := model.Job{
		Name:      *name,
		ID:        model.Slug(*name),
		Schedule:  *cron,
		TZ:        *tz,
		Detection: model.DetectionMode(*detection),
		Match:     *match,
		Command:   *command,
		Group:     *group,
		// Naming the owning scheduler is what lets the daemon read that
		// scheduler's own run records for this job, the difference between a
		// measured run and a fixed guess. It defaults to "manual" for a job
		// nothing else owns.
		Source:    *source,
		Enabled:   !*disabled,
		CreatedAt: time.Now(),
	}

	if err := applyDurations(&job, *maxRuntime, *wakeBuffer); err != nil {
		return err
	}
	// Validate the schedule here rather than only in the daemon, so a typo is
	// rejected immediately with a useful message instead of becoming a job
	// that silently never fires.
	// Anchored on the CreatedAt just assigned above, so the "next run" printed
	// below is the time the daemon will independently compute for this job
	// rather than a second opinion about it.
	sched, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor())
	if err != nil {
		return err
	}
	if err := job.Validate(); err != nil {
		return err
	}

	// Warn before saving if a pattern matches nothing right now. Not an
	// error: a job that only runs at 3am legitimately has no live process
	// at configuration time.
	if job.Detection == model.DetectPattern {
		reportMatchPreview(ctx, job.Match)
	}

	var saved model.Job
	if err := callDaemon(ctx, ipc.OpJobsAdd, job, &saved); err != nil {
		return err
	}

	r := ctx.Out
	next := sched.Next(time.Now())
	r.Printf("%s registered %s\n", r.Good(r.Sym().OK), r.Bold(saved.Name))

	// Only show the raw expression alongside the human rendering when they
	// actually differ; "* * * * * (* * * * *)" is noise.
	schedLine := sched.Display
	if sched.Display != job.Schedule {
		schedLine += " " + r.Muted("("+job.Schedule+")")
	}
	r.Printf("  %s  %s\n", r.Muted("schedule"), schedLine)
	r.Printf("  %s  %s %s\n", r.Muted("next run"),
		next.Format("Mon 2 Jan 15:04"), r.Muted(model.HumanUntil(next, time.Now())))

	if job.Detection == model.DetectMark {
		r.Blank()
		r.Line(r.Muted("  For this to work, the job must announce itself. Change its"))
		r.Line(r.Muted("  crontab line to:"))
		r.Blank()
		original := job.Command
		if original == "" {
			original = "<your original command>"
		}
		r.Printf("    %s\n", r.Accent(fmt.Sprintf("goguma-mark %s -- %s", saved.ID, original)))
	}
	return nil
}

func applyDurations(job *model.Job, maxRuntime, wakeBuffer string) error {
	if maxRuntime != "" {
		d, err := model.ParseDuration(maxRuntime)
		if err != nil {
			return fmt.Errorf("--max-runtime: %w", err)
		}
		job.MaxRuntime = model.Duration(d)
	}
	if wakeBuffer != "" {
		d, err := model.ParseDuration(wakeBuffer)
		if err != nil {
			return fmt.Errorf("--wake-buffer: %w", err)
		}
		job.WakeBuffer = model.Duration(d)
	}
	return nil
}

// reportMatchPreview tells the user what a pattern currently matches.
func reportMatchPreview(ctx *Context, pattern string) {
	var resp ipc.MatchTestResp
	if err := callDaemon(ctx, ipc.OpTestMatch, ipc.MatchTestReq{Pattern: pattern}, &resp); err != nil {
		return
	}
	r := ctx.Out
	switch {
	case !resp.Valid:
		r.Printf("%s %s\n", r.Warn(r.Sym().Warn), resp.Error)
	case len(resp.Matches) == 0:
		r.Printf("%s %s\n", r.Muted(r.Sym().Bullet), r.Muted(
			"nothing matches that pattern right now, expected if the job isn't running"))
	default:
		r.Printf("%s matches %d process(es) right now, e.g. pid %d\n",
			r.Good(r.Sym().OK), len(resp.Matches), resp.Matches[0].PID)
	}
}

var cmdRemove = &Command{
	Name:    "remove",
	Summary: "unregister a job",
	Usage: `goguma remove <job>

Unregisters a job. Its run history is kept, so re-adding it later starts from
the ceiling it had already learned rather than from a cold start.`,
	Run: func(ctx *Context, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("which job? usage: goguma remove <job>")
		}
		var job model.Job
		if err := callDaemon(ctx, ipc.OpJobsRemove, ipc.JobRef{Ref: args[0]}, &job); err != nil {
			return err
		}
		ctx.Out.Printf("%s removed %s %s\n",
			ctx.Out.Good(ctx.Out.Sym().OK), ctx.Out.Bold(job.Name),
			ctx.Out.Muted("(history kept)"))
		return nil
	},
}

const editUsage = `goguma edit <job> [options]

Changes one or more fields of an existing job. Only the flags you pass are
modified; everything else is left alone.

options:
  --name, --cron, --tz, --detection, --match, --command,
  --max-runtime, --wake-buffer`

var cmdEdit = &Command{
	Name:    "edit",
	Summary: "change a registered job",
	Usage:   editUsage,
	Run: func(ctx *Context, args []string) error {
		ref, rest := splitRef(args)
		if ref == "" {
			return fmt.Errorf("which job? usage: goguma edit <job> [options]")
		}

		fs := flag.NewFlagSet("edit", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		var (
			name       = fs.String("name", "", "")
			cron       = fs.String("cron", "", "")
			tz         = fs.String("tz", "", "")
			detection  = fs.String("detection", "", "")
			match      = fs.String("match", "", "")
			command    = fs.String("command", "", "")
			maxRuntime = fs.String("max-runtime", "", "")
			wakeBuffer = fs.String("wake-buffer", "", "")
		)
		if err := fs.Parse(rest); err != nil {
			return err
		}

		var resp ipc.JobsListResp
		if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
			return errDaemonDown()
		}
		job, ok := findJob(resp.Jobs, ref)
		if !ok {
			return fmt.Errorf("no job named %q", ref)
		}

		// Only overwrite fields the user actually passed, so editing one
		// field cannot silently reset another to a zero value.
		seen := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })

		if seen["name"] {
			job.Name = *name
		}
		if seen["cron"] {
			job.Schedule = *cron
		}
		if seen["tz"] {
			job.TZ = *tz
		}
		if seen["detection"] {
			job.Detection = model.DetectionMode(*detection)
		}
		if seen["match"] {
			job.Match = *match
		}
		if seen["command"] {
			job.Command = *command
		}
		if seen["max-runtime"] {
			if err := applyDurations(&job, *maxRuntime, ""); err != nil {
				return err
			}
		}
		if seen["wake-buffer"] {
			if err := applyDurations(&job, "", *wakeBuffer); err != nil {
				return err
			}
		}
		if len(seen) == 0 {
			return fmt.Errorf("nothing to change · pass at least one option\n\n%s", editUsage)
		}

		if _, err := schedule.ParseAt(job.Schedule, job.Location(), job.ScheduleAnchor()); err != nil {
			return err
		}
		if err := job.Validate(); err != nil {
			return err
		}

		var saved model.Job
		if err := callDaemon(ctx, ipc.OpJobsPut, job, &saved); err != nil {
			return err
		}
		ctx.Out.Printf("%s updated %s (%s)\n",
			ctx.Out.Good(ctx.Out.Sym().OK), ctx.Out.Bold(saved.Name),
			strings.Join(sortedKeys(seen), ", "))
		return nil
	},
}

func findJob(views []ipc.JobView, ref string) (model.Job, bool) {
	slug := model.Slug(ref)
	for _, v := range views {
		if v.Job.ID == ref || v.Job.Name == ref || v.Job.ID == slug {
			return v.Job, true
		}
	}
	return model.Job{}, false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

const groupUsage = `goguma group <job> <group-name>
goguma group <job> --clear

Files a job under a heading in 'goguma list', or removes it from one. A job
belongs to at most one group, and groups don't nest; they exist so a machine
with twenty adopted jobs reads as a handful of headings rather than one wall.

Grouping is organisation only. It changes nothing about when a job runs, what
wakes the machine, or how long a hold lasts.

  --clear   remove the job from its group`

var cmdGroup = &Command{
	Name:    "group",
	Summary: "file a job under a heading in the list",
	Usage:   groupUsage,
	Run: func(ctx *Context, args []string) error {
		ref, rest := splitRef(args)

		fs := flag.NewFlagSet("group", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		clear := fs.Bool("clear", false, "remove the job from its group")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if ref == "" {
			return fmt.Errorf("which job? usage: goguma group <job> <group-name>")
		}

		// Join the remaining words rather than taking one argument, so an
		// unquoted `group nightly Morning jobs` files it under "Morning jobs"
		// instead of failing on a stray argument.
		name := model.NormalizeGroup(strings.Join(fs.Args(), " "))
		switch {
		case *clear && name != "":
			return fmt.Errorf("--clear removes the job from its group, so it takes no group name")
		case !*clear && name == "":
			return fmt.Errorf("which group? usage: goguma group <job> <group-name>\n\n%s", groupUsage)
		}

		var resp ipc.JobsListResp
		if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
			return errDaemonDown()
		}
		job, ok := findJob(resp.Jobs, ref)
		if !ok {
			return fmt.Errorf("no job named %q", ref)
		}
		previous := job.Group
		job.Group = name
		if err := job.Validate(); err != nil {
			return err
		}

		// A group is an ordinary field of a job, so this rides on jobs.put
		// rather than earning its own op: a second way to write a job is a
		// second place for the two writes to disagree.
		var saved model.Job
		if err := callDaemon(ctx, ipc.OpJobsPut, job, &saved); err != nil {
			return err
		}

		r := ctx.Out
		switch {
		case saved.Group == "" && previous == "":
			r.Printf("%s %s wasn't in a group\n",
				r.Muted(r.Sym().Bullet), r.Bold(saved.Name))
		case saved.Group == "":
			r.Printf("%s %s is no longer in %s\n",
				r.Good(r.Sym().OK), r.Bold(saved.Name), r.Bold(previous))
		default:
			r.Printf("%s %s is now in %s\n",
				r.Good(r.Sym().OK), r.Bold(saved.Name), r.Bold(saved.Group))
		}
		return nil
	},
}

var cmdEnable = &Command{
	Name:    "enable",
	Summary: "re-enable a disabled job",
	Usage:   "goguma enable <job>",
	Run:     setEnabled(true),
}

var cmdDisable = &Command{
	Name:    "disable",
	Summary: "stop waking for a job without removing it",
	Usage: `goguma disable <job>

Stops scheduling wakes for this job and releases it if a window is open. The
job and its history are kept, so 'goguma enable' restores it exactly.`,
	Run: setEnabled(false),
}

func setEnabled(enabled bool) func(*Context, []string) error {
	return func(ctx *Context, args []string) error {
		if len(args) == 0 {
			verb := "enable"
			if !enabled {
				verb = "disable"
			}
			return fmt.Errorf("which job? usage: goguma %s <job>", verb)
		}
		var job model.Job
		if err := callDaemon(ctx, ipc.OpJobsEnable,
			ipc.EnableReq{Ref: args[0], Enabled: enabled}, &job); err != nil {
			return err
		}
		state := "enabled"
		if !enabled {
			state = "disabled"
		}
		ctx.Out.Printf("%s %s is now %s\n",
			ctx.Out.Good(ctx.Out.Sym().OK), ctx.Out.Bold(job.Name), state)
		return nil
	}
}

var cmdTestMatch = &Command{
	Name:    "test-match",
	Summary: "check what a process pattern currently matches",
	Usage: `goguma test-match <pattern>

Evaluates a match pattern against the running process table and prints what it
finds. Use this before registering a job with --detection pattern, so a
mistake surfaces now rather than as a job that silently never runs.`,
	Run: func(ctx *Context, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: goguma test-match <pattern>")
		}
		pattern := strings.Join(args, " ")

		var resp ipc.MatchTestResp
		if err := callDaemon(ctx, ipc.OpTestMatch, ipc.MatchTestReq{Pattern: pattern}, &resp); err != nil {
			return errDaemonDown()
		}

		r := ctx.Out
		if !resp.Valid {
			return fmt.Errorf("%s", resp.Error)
		}
		if len(resp.Matches) == 0 {
			r.Printf("%s nothing currently matches %s\n", r.Muted(r.Sym().Bullet), r.Bold(pattern))
			r.Blank()
			r.Line(r.Muted("  That is expected if the job isn't running right now. To be sure the"))
			r.Line(r.Muted("  pattern is right, run this while the job is actually running."))
			return nil
		}
		r.Printf("%s %s matches %d process(es):\n",
			r.Good(r.Sym().OK), r.Bold(pattern), len(resp.Matches))
		r.Blank()
		for _, m := range resp.Matches {
			r.Printf("  %s %s\n", r.Muted(fmt.Sprintf("pid %-7d", m.PID)),
				truncateCommand(m.Command, r.Width()-14))
		}
		if len(resp.Matches) > 1 {
			r.Blank()
			r.Line(r.Muted("  More than one match: goguma will follow the first, and the hold"))
			r.Line(r.Muted("  ends when it exits. Narrow the pattern if that isn't what you want."))
		}
		return nil
	},
}

func truncateCommand(cmd string, width int) string {
	if width < 20 {
		width = 20
	}
	if len(cmd) <= width {
		return cmd
	}
	return cmd[:width-1] + "…"
}
