package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/render"
)

var cmdHistory = &Command{
	Name:    "history",
	Summary: "show a job's duration trend",
	Usage: `goguma history <job> [--limit N] [--json]

Shows how long a job has actually been taking, and how much sleep-hold time
was spent beyond that. A large gap between the two is wasted battery, usually
caused by a match pattern that fails to see the job exit.

  --limit N   show the last N runs (default 20)
  --json      emit machine-readable JSON`,
	Run: func(ctx *Context, args []string) error {
		// The job name comes first in practice, so flags are hoisted ahead of
		// it before parsing; otherwise `history <job> --json` silently drops
		// the flag and prints a human table to a caller expecting JSON.
		ref, rest := splitRef(args)

		fs := flag.NewFlagSet("history", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		limit := fs.Int("limit", 20, "number of runs to show")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if ref == "" {
			return fmt.Errorf("which job? usage: goguma history <job>")
		}

		var resp ipc.HistoryResp
		if err := callDaemon(ctx, ipc.OpHistory,
			ipc.HistoryReq{Ref: ref, Limit: *limit}, &resp); err != nil {
			return err
		}

		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}
		printHistory(ctx.Out, resp)
		return nil
	},
}

func printHistory(r *render.Renderer, resp ipc.HistoryResp) {
	st := resp.Stats
	r.Line(r.Bold(resp.Job.Name) + r.Muted("  "+resp.Job.Schedule))
	r.Blank()

	if len(resp.Runs) == 0 {
		r.Line(r.Muted("  No runs recorded yet."))
		r.Blank()
		r.Printf("  %s\n", r.Muted(
			"The ceiling stays at the cold-start default until this job has run a few times."))
		return
	}

	// The trend, which is the whole point of this view: it should be visible
	// at a glance whether the ceiling has converged on the real runtime.
	var durations []float64
	for _, run := range resp.Runs {
		durations = append(durations, run.Duration.D().Seconds())
	}
	spark := r.Sparkline(durations)

	r.KeyValue([][2]string{
		{"runs", fmt.Sprintf("%d recorded", st.Runs)},
		{"typical", st.Typical.String() + r.Muted("  (median)")},
		{"slow run", st.P95.String() + r.Muted("  (all but the slowest)")},
		{"ceiling", ceilingNote(r, st)},
		{"trend", r.Accent(spark)},
	})

	if st.BatteryPerRun > 0 {
		r.Blank()
		r.Printf("  %s\n", r.Muted(fmt.Sprintf(
			"about %s of battery per run, holding the Mac awake for this job",
			model.Percent(st.BatteryPerRun))))
	}

	r.Blank()
	t := render.NewTable(r,
		render.Column{Title: ""},
		render.Column{Title: "STARTED", Max: 20},
		render.Column{Title: "RAN FOR", Align: render.AlignRight},
		render.Column{Title: "HELD FOR", Align: render.AlignRight},
		render.Column{Title: "OUTCOME", Flex: true},
	)

	sym := r.Sym()
	for _, run := range resp.Runs {
		mark, plain := outcomeMark(r, run.Outcome)

		when := run.WindowOpened.Local().Format("Jan 02 15:04")
		if !run.Started.IsZero() {
			when = run.Started.Local().Format("Jan 02 15:04")
		}

		ran := run.Duration.String()
		if run.Duration == 0 {
			ran = "-"
		}

		// Flag runs where the hold vastly exceeded the work, which is the
		// signal that detection is not working for this job.
		held := run.HoldDuration.String()
		heldPlain := held
		if run.Duration > 0 && run.HoldDuration.D() > 3*run.Duration.D()+30*time.Second {
			held = r.Warn(held)
		}

		note := outcomeNote(run)
		if run.WokeMachine {
			note += r.Muted(" · woken")
		}

		t.Row(
			render.SC(mark, plain),
			render.C(when),
			render.C(ran),
			render.SC(held, heldPlain),
			render.C(note),
		)
	}
	t.Render()

	if st.NeverSeen > 0 {
		r.Blank()
		r.Problem(
			fmt.Sprintf("%d of these windows never detected the job at all", st.NeverSeen),
			fmt.Sprintf("goguma doctor %s", resp.Job.ID))
	}
	_ = sym
}

func ceilingNote(r *render.Renderer, st model.Stats) string {
	s := st.Ceiling.String()
	if st.ColdStart {
		return s + r.Muted("  (cold start · not yet learned)")
	}
	return s
}

func outcomeMark(r *render.Renderer, o model.Outcome) (styled, plain string) {
	sym := r.Sym()
	switch o {
	case model.OutcomeOK:
		return r.Good(sym.OK), sym.OK
	case model.OutcomeFailed:
		return r.Danger(sym.Danger), sym.Danger
	default:
		return r.Warn(sym.Warn), sym.Warn
	}
}

func outcomeNote(run model.Run) string {
	switch run.Outcome {
	case model.OutcomeOK:
		return "ok"
	case model.OutcomeFailed:
		if run.ExitCode != nil {
			return fmt.Sprintf("exited %d", *run.ExitCode)
		}
		return "failed"
	case model.OutcomeCeiling:
		return "cut off at the ceiling"
	case model.OutcomeNeverDetected:
		return "never detected"
	case model.OutcomeCutout:
		return "released by a safety cutout"
	case model.OutcomeSlept:
		return "missed · the Mac was asleep and not woken"
	default:
		return string(run.Outcome)
	}
}
