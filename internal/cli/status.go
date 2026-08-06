package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/junnam/wakeguard/internal/ipc"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/render"
)

func fetchStatus(ctx *Context) (model.Status, error) {
	var st model.Status
	err := callDaemon(ctx, ipc.OpStatus, nil, &st)
	return st, err
}

// errDaemonDown is returned with actionable guidance rather than a bare
// connection error, because "connection refused" tells a user nothing about
// what to do next.
func errDaemonDown() error {
	return errors.New("WakeGuard's background service isn't running, start it with 'wakeguard install', " +
		"or check 'wakeguard doctor' if you have already installed it")
}

var cmdStatus = &Command{
	Name:    "status",
	Summary: "show what wakeguard is doing right now",
	Usage: `wakeguard status [--json]

Shows whether sleep is currently being held, when the machine will next wake,
and any problems that need attention.

  --json   emit machine-readable JSON with a stable shape, suitable for
           polling from another tool`,
	Run: func(ctx *Context, args []string) error {
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args); err != nil {
			return err
		}

		st, err := fetchStatus(ctx)
		if err != nil {
			if *asJSON {
				// Still emit valid JSON when the daemon is down: a polling
				// dashboard should get a parseable "not running" rather than
				// a parse error it has to special-case.
				out, _ := json.MarshalIndent(model.Status{SchemaVersion: 1}, "", "  ")
				fmt.Fprintln(os.Stdout, string(out))
				return nil
			}
			return errDaemonDown()
		}

		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(st)
		}
		printStatus(ctx.Out, st)
		return nil
	},
}

func printStatus(r *render.Renderer, st model.Status) {
	now := time.Now()
	sym := r.Sym()

	// Headline: the one line that answers "what is happening".
	switch {
	case st.Paused:
		r.Printf("%s %s\n", r.Warn(sym.Idle), r.Bold("paused")+r.Muted(" · no wakes are scheduled"))
	case st.Holding:
		r.Printf("%s %s\n", r.Good(sym.Holding), r.Bold("holding sleep off"))
	default:
		r.Printf("%s %s\n", r.Muted(sym.Idle), r.Bold("idle")+r.Muted(" · the machine can sleep normally"))
	}

	if len(st.Holds) > 0 {
		r.Blank()

		// The manual window is rendered on its own terms. It is a hold like any
		// other to the daemon, but it has no job behind it, so the job shape
		// below would describe it as something that is "waiting for the job to
		// start" and never will.
		if st.KeepAwakeUntil != nil {
			r.Printf("  %s %s · %s\n", r.Good(sym.Bullet),
				r.Accent("keep awake"), r.Muted("held manually"))
			r.Printf("      %s\n", r.Muted(fmt.Sprintf("%s left, until %s",
				model.HumanDuration(st.KeepAwakeUntil.Sub(now)),
				st.KeepAwakeUntil.Local().Format("15:04"))))
		}

		for _, h := range st.Holds {
			if h.JobID == model.KeepAwakeJobID {
				continue
			}
			elapsed := model.Clock(h.Elapsed(now))
			remain := h.Remaining(now)

			// A wake-only job is never observed, so "waiting" is a state it
			// can never leave — it reads as a job that is late when the hold
			// is simply running its fixed window. Same reasoning the app
			// already applies to manual holds.
			// Ordered by what is actually known, not by how the job is
			// configured: a scheduler that reports its own runs makes a
			// DetectNone job observed in practice, and calling that "not
			// watched" while we are watching it is simply false.
			detail := r.Muted("waiting for the job to start")
			if !h.Detected && h.Detection == model.DetectNone {
				detail = r.Muted("not watched, holding the full window")
			} else if h.Detected {
				detail = fmt.Sprintf("running for %s", r.Bold(elapsed))
				if h.PID > 0 {
					detail += r.Muted(fmt.Sprintf(" (pid %d)", h.PID))
				}
			}
			r.Printf("  %s %s · %s\n", r.Good(sym.Bullet), r.Accent(h.JobName), detail)

			ceilNote := fmt.Sprintf("ceiling %s, %s left",
				h.Ceiling, model.HumanDuration(remain))
			if remain < 0 {
				ceilNote = r.Warn("past its ceiling; releasing")
			}
			r.Printf("      %s\n", r.Muted(ceilNote))
		}
	}

	r.Blank()
	var pairs [][2]string

	if st.NextWake != nil {
		when := st.NextWake.Local().Format("Mon 15:04:05")
		rel := model.HumanUntil(*st.NextWake, now)
		v := fmt.Sprintf("%s  %s", when, r.Muted(rel))
		if !st.WakeScheduled {
			v = r.Danger(when + "  not registered with the OS")
		}
		pairs = append(pairs, [2]string{"next wake", v})
		if st.NextJob != "" && st.NextFire != nil {
			pairs = append(pairs, [2]string{"for", fmt.Sprintf("%s at %s",
				r.Accent(st.NextJob), st.NextFire.Local().Format("15:04:05"))})
		}
	} else if st.WakeSuppressed != "" {
		// A deliberate decision, not a failure — worded so it does not read
		// like something went wrong.
		pairs = append(pairs, [2]string{"next wake",
			r.Warn("held back") + r.Muted("  "+st.WakeSuppressed)})
	} else if st.Starting {
		// An absence, not a finding. "nothing scheduled" on a machine with
		// nine jobs reads as a fault; the daemon has simply not finished its
		// first tick yet, which takes about a second.
		pairs = append(pairs, [2]string{"next wake", r.Muted("starting up\u2026")})
	} else if !st.Paused {
		pairs = append(pairs, [2]string{"next wake", r.Muted("nothing scheduled")})
	}

	// Be specific about which sleep is actually blocked. Claiming a generic
	// success would hide that lid-closed holds are unavailable without the
	// helper, which is the difference between a job running and not.
	helper := r.Good("connected") + r.Muted("  lid-closed sleep can be held")
	if !st.HelperConnected {
		helper = r.Warn("not connected") + r.Muted("  only lid-open holds will work")
	}
	pairs = append(pairs, [2]string{"helper", helper})

	p := st.Power
	lid := "open"
	if p.LidClosed {
		lid = "closed"
	}
	power := lid
	if st.Starting {
		power += r.Muted("  ·  reading the machine\u2026")
	}
	if p.BatteryPct >= 0 {
		src := "on battery"
		if p.OnAC {
			src = "on AC"
		}
		power += fmt.Sprintf("  ·  %d%% %s", p.BatteryPct, src)
	}
	if p.CPUTempC != nil {
		power += fmt.Sprintf("  ·  %.0f°C", *p.CPUTempC)
	}
	pairs = append(pairs, [2]string{"machine", power})

	r.KeyValue(pairs)

	if st.Cutout != nil {
		r.Blank()
		r.Printf("  %s %s\n", r.Danger(sym.Danger),
			r.Bold("safety cutout: ")+st.Cutout.Detail)
		r.Printf("      %s\n", r.Muted(fmt.Sprintf(
			"released %d hold(s) at %s; holds stay suspended until conditions recover",
			st.Cutout.Released, st.Cutout.FiredAt.Local().Format("15:04:05"))))
	}

	if st.LastRun != nil && !st.Holding {
		lr := st.LastRun
		r.Blank()
		r.Printf("  %s %s\n", r.Muted("last run"), describeRun(r, lr))
	}

	if len(st.Warnings) > 0 {
		r.Blank()
		r.Section("  needs attention")
		for _, w := range st.Warnings {
			r.Problem(w.Message, w.Fix)
		}
	}
}

func describeRun(r *render.Renderer, run *model.Run) string {
	sym := r.Sym()
	var mark, note string
	switch run.Outcome {
	case model.OutcomeOK:
		mark = r.Good(sym.OK)
		note = fmt.Sprintf("ran %s", run.Duration)
	case model.OutcomeFailed:
		mark = r.Danger(sym.Danger)
		code := 0
		if run.ExitCode != nil {
			code = *run.ExitCode
		}
		note = fmt.Sprintf("ran %s then exited %d", run.Duration, code)
	case model.OutcomeCeiling:
		mark = r.Warn(sym.Warn)
		note = fmt.Sprintf("hit its %s ceiling and was cut off", run.Ceiling)
	case model.OutcomeNeverDetected:
		mark = r.Warn(sym.Warn)
		note = "was never detected · the machine woke for nothing"
	case model.OutcomeCutout:
		mark = r.Warn(sym.Warn)
		note = "was released early by a safety cutout"
	default:
		mark, note = sym.Bullet, string(run.Outcome)
	}

	when := run.WindowOpened.Local().Format("Mon 15:04")
	line := fmt.Sprintf("%s %s  %s", mark, r.Muted(when), note)
	if run.WokeMachine {
		line += r.Muted("  (machine was asleep; wakeguard woke it)")
	}
	return line
}
