package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/render"
)

var cmdList = &Command{
	Name:    "list",
	Summary: "list registered jobs and their learned durations",
	Usage: `goguma list [--json] [--explain]

Shows every registered job with its schedule, next run, typical duration, and
the ceiling that will be enforced. Jobs that have been filed into a group are
listed under that group's heading, ungrouped ones last; see 'goguma group'.

  --json      emit machine-readable JSON
  --explain   show how each ceiling was derived`,
	Run: func(ctx *Context, args []string) error {
		fs := flag.NewFlagSet("list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		asJSON := fs.Bool("json", false, "emit JSON")
		explain := fs.Bool("explain", false, "explain each ceiling")
		if err := fs.Parse(args); err != nil {
			return err
		}

		var resp ipc.JobsListResp
		if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
			return errDaemonDown()
		}

		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}
		printJobs(ctx.Out, resp, *explain)
		return nil
	},
}

func printJobs(r *render.Renderer, resp ipc.JobsListResp, explain bool) {
	jobs := resp.Jobs
	if len(jobs) == 0 {
		r.Line(r.Muted("No jobs registered yet."))
		r.Blank()
		r.Printf("  %s  scan this machine for jobs worth waking for\n", r.Accent("goguma import"))
		r.Printf("  %s  register one by hand\n", r.Accent("goguma add --help"))
		return
	}

	now := time.Now()
	sym := r.Sym()

	t := render.NewTable(r,
		render.Column{Title: ""},
		render.Column{Title: "NAME", Flex: true, Max: 28},
		render.Column{Title: "SCHEDULE", Max: 22},
		render.Column{Title: "NEXT RUN", Max: 20},
		render.Column{Title: "TYPICAL", Align: render.AlignRight},
		render.Column{Title: "CEILING", Align: render.AlignRight},
	)

	// Sections are only introduced once something is actually grouped, so a
	// list where nobody has used groups reads exactly as it always has.
	sections := listSections(resp)
	labelled := len(sections) > 1

	for _, section := range sections {
		rows := jobsInGroup(jobs, section)
		if section == "" && len(rows) == 0 {
			continue
		}
		if labelled {
			if section == "" {
				// Quiet, and named rather than left blank: under a run of real
				// headings, an unlabelled block reads as belonging to the last
				// one.
				t.Label(r.Muted("ungrouped"), "ungrouped")
			} else {
				t.Label(r.Heading(section), section)
			}
		}
		for _, v := range rows {
			jobRow(r, t, v, now)
		}
	}
	t.Render()

	// Legend, only for the markers actually present.
	var notes []string
	if anyColdStart(jobs) {
		notes = append(notes, r.Muted("* ceiling is a conservative default until enough runs accumulate"))
	}
	if anyProblem(jobs) {
		notes = append(notes, r.Muted(sym.Warn+" needs attention · run 'goguma doctor'"))
	}
	if len(notes) > 0 {
		r.Blank()
		for _, n := range notes {
			r.Printf("  %s\n", n)
		}
	}

	if explain {
		r.Blank()
		r.Section("  how each ceiling was set")
		// Pad on the unstyled name: a styled string carries invisible escape
		// bytes, so %-24s would count those and misalign every coloured row.
		const nameCol = 26
		for _, v := range jobs {
			name := render.Truncate(v.Job.Name, nameCol)
			pad := strings.Repeat(" ", max(nameCol-len([]rune(name)), 0))
			r.Printf("    %s%s  %s\n", r.Accent(name), pad, r.Muted(v.CeilingReason))
		}
	}

	// What one round of the whole schedule costs, in the unit the user pays
	// it in. Summed per-run costs rather than a grand total: the question is
	// "what do these jobs cost me each time", not "how much have they ever
	// used", which only ever climbs.
	var perRound float64
	var woken, slept int
	for _, v := range jobs {
		perRound += v.Stats.BatteryPerRun
		woken += v.Stats.Woken
		slept += v.Stats.Slept
	}
	if perRound > 0 {
		r.Blank()
		r.Printf("  %s\n", r.Muted(fmt.Sprintf(
			"about %s of battery each time these jobs run",
			model.Percent(perRound))))
	}

	// What goguma has actually done, measured rather than modelled.
	//
	// A count of runs it woke the machine for is the only honest claim the
	// tool can make about its own worth: every one of them is a run that was
	// going to be skipped. Printed alongside the misses, because a scoreboard
	// that only shows the wins is an advertisement.
	if woken > 0 || slept > 0 {
		if perRound <= 0 {
			r.Blank()
		}
		parts := []string{}
		if woken > 0 {
			parts = append(parts, fmt.Sprintf("woken for %s that would have been missed",
				pluralRuns(woken)))
		}
		if slept > 0 {
			parts = append(parts, fmt.Sprintf("%s missed while asleep", pluralRuns(slept)))
		}
		r.Printf("  %s\n", r.Muted("goguma has "+strings.Join(parts, ", ")))
	}
}

func pluralRuns(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// listSections orders the blocks of the list: every group name in use, sorted,
// with the ungrouped remainder last.
//
// The daemon's group list is taken as authoritative rather than derived from
// the rows, so a group it knows about still gets a heading even when nothing
// currently sits in it. Groups found only on the rows are folded in as well,
// which keeps the list grouped when talking to a daemon too old to send them.
func listSections(resp ipc.JobsListResp) []string {
	seen := map[string]bool{}
	var out []string
	add := func(g string) {
		if g == "" || seen[g] {
			return
		}
		seen[g] = true
		out = append(out, g)
	}
	for _, g := range resp.Groups {
		add(g)
	}
	for _, v := range resp.Jobs {
		add(v.Job.Group)
	}
	sort.Strings(out)
	return append(out, "")
}

// jobsInGroup keeps the caller's existing order within a group.
func jobsInGroup(jobs []ipc.JobView, group string) []ipc.JobView {
	var out []ipc.JobView
	for _, v := range jobs {
		if v.Job.Group == group {
			out = append(out, v)
		}
	}
	return out
}

func jobRow(r *render.Renderer, t *render.Table, v ipc.JobView, now time.Time) {
	mark, plain := jobMark(r, v)

	schedText := v.ScheduleDisplay
	if schedText == "" {
		schedText = v.Job.Schedule
	}
	if v.ScheduleError != "" {
		schedText = "invalid"
	}

	next := r.Muted("-")
	nextPlain := "-"
	if v.NextFire != nil {
		nextPlain = model.HumanUntil(*v.NextFire, now)
		next = nextPlain
	}
	if !v.Job.Enabled {
		next, nextPlain = r.Muted("disabled"), "disabled"
	}

	typical := "-"
	if v.Stats.Typical > 0 {
		typical = v.Stats.Typical.String()
	}

	ceiling := v.Stats.Ceiling.String()
	ceilCell := render.C(ceiling)
	if v.Stats.ColdStart {
		ceilCell = render.SC(ceiling+r.Muted("*"), ceiling+"*")
	}

	t.Row(
		render.SC(mark, plain),
		render.C(v.Job.Name),
		render.C(schedText),
		render.SC(next, nextPlain),
		render.C(typical),
		ceilCell,
	)
}

// jobMark returns the status glyph for a job row, styled and plain.
func jobMark(r *render.Renderer, v ipc.JobView) (styled, plain string) {
	sym := r.Sym()
	switch {
	case v.ScheduleError != "" || v.Stats.NeverSeen > 0:
		return r.Warn(sym.Warn), sym.Warn
	case v.Holding:
		return r.Good(sym.Holding), sym.Holding
	case !v.Job.Enabled:
		return r.Muted(sym.Disabled), sym.Disabled
	default:
		return r.Muted(sym.Idle), sym.Idle
	}
}

func anyColdStart(jobs []ipc.JobView) bool {
	for _, v := range jobs {
		if v.Stats.ColdStart {
			return true
		}
	}
	return false
}

func anyProblem(jobs []ipc.JobView) bool {
	for _, v := range jobs {
		if v.ScheduleError != "" || v.Stats.NeverSeen > 0 {
			return true
		}
	}
	return false
}
