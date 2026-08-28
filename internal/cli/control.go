package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/daemon"
	"github.com/junnam586/goguma/internal/ipc"
)

var cmdSkipNext = &Command{
	Name:    "skip-next",
	Summary: "skip the next scheduled wake",
	Usage: `goguma skip-next [job]

Skips the next wake, either for a specific job, or the very next one if no
job is named. Everything after it is unaffected, so this is the right tool for
"not tonight" rather than disabling a job.`,
	Run: func(ctx *Context, args []string) error {
		ref := ""
		if len(args) > 0 {
			ref = args[0]
		}
		var resp struct {
			SkippedFire time.Time `json:"skipped_fire"`
		}
		if err := callDaemon(ctx, ipc.OpSkipNext, ipc.SkipNextReq{Ref: ref}, &resp); err != nil {
			return err
		}
		r := ctx.Out
		r.Printf("%s skipping the wake for %s\n", r.Good(r.Sym().OK),
			r.Bold(resp.SkippedFire.Local().Format("Mon 2 Jan 15:04")))
		r.Line(r.Muted("  Later runs are unaffected."))
		return nil
	},
}

var cmdSleepNow = &Command{
	Name:    "sleep-now",
	Summary: "release every hold so the machine can sleep",
	Usage: `goguma sleep-now

Releases every active hold immediately. Doesn't put the machine to sleep; it
removes goguma's reason not to. Any job currently running keeps running.`,
	Run: func(ctx *Context, args []string) error {
		var resp struct {
			Released int `json:"released"`
		}
		if err := callDaemon(ctx, ipc.OpSleepNow, nil, &resp); err != nil {
			return err
		}
		r := ctx.Out
		if resp.Released == 0 {
			r.Printf("%s nothing was being held; the machine could already sleep\n", r.Muted(r.Sym().Bullet))
			return nil
		}
		r.Printf("%s released %d hold(s); the machine can sleep normally now\n",
			r.Good(r.Sym().OK), resp.Released)
		return nil
	},
}

var cmdPause = &Command{
	Name:    "pause",
	Summary: "stop scheduling wakes until resumed",
	Usage: `goguma pause

Releases all holds and stops scheduling wakes. Jobs stay registered and their
history is preserved; nothing wakes the machine until 'goguma resume'.`,
	Run: func(ctx *Context, args []string) error {
		if err := callDaemon(ctx, ipc.OpPause, nil, nil); err != nil {
			return err
		}
		r := ctx.Out
		r.Printf("%s paused · no wakes will be scheduled\n", r.Warn(r.Sym().Idle))
		r.Printf("  %s\n", r.Muted("resume with: goguma resume"))
		return nil
	},
}

var cmdResume = &Command{
	Name:    "resume",
	Summary: "resume scheduling wakes",
	Usage:   "goguma resume",
	Run: func(ctx *Context, args []string) error {
		if err := callDaemon(ctx, ipc.OpResume, nil, nil); err != nil {
			return err
		}
		st, err := fetchStatus(ctx)
		r := ctx.Out
		r.Printf("%s resumed\n", r.Good(r.Sym().OK))
		if err == nil && st.NextWake != nil {
			r.Printf("  %s next wake %s\n", r.Muted("→"),
				st.NextWake.Local().Format("Mon 2 Jan 15:04:05"))
		}
		return nil
	},
}

const configUsage = `goguma config get [--json]
goguma config set <key> <value>

Settings:
  wake_buffer              how early to wake before a job fires (default 90s)
  wake_only_hold           window for a job that can't be watched (default 3m).
                           Most adopted jobs use this, so it is the setting that
                           decides what they cost.
  default_ceiling          ceiling for a job with no history yet (default 5m)
  min_ceiling              floor for the learned ceiling (default 30s)
  max_ceiling              cap for the learned ceiling (default 2h)
  ceiling_multiplier       headroom on the slow-run figure (default 1.2, so +20%)
  history_window           how many recent runs the estimator considers
  min_runs_for_estimate    runs needed before the learned ceiling is used
  thermal_cutout_c         force-release above this temperature, 70-95
  low_battery_cutout_pct   release holds below this charge, 5-50 (default 10).
                           goguma also refuses to wake the machine at all
                           until it is this plus the rearm margin, so a wake
                           can't land straight into a release.
  cutout_rearm_margin_pct  headroom above the cutout, 1-50 (default 5)
  webhook_url              POST target for problem events
  notify_on_missed_job     notify when a job is never detected
  use_wake_or_power_on     also power on a machine that is shut down. Does
                           nothing on a FileVault Mac, which stops at the
                           unlock screen with nothing running
  min_import_interval      shortest schedule worth a wake. 0 (the default)
                           covers everything, however often it fires
  auto_adopt               schedulers watched for new jobs. 'all' restores the
                           default (everything adoptable), 'off' disables it,
                           or name sources comma-separated.
  sleep_after_wake         put the machine back to sleep after a job goguma
                           woke it for, when nobody is at the keyboard
                           (default on)
  agent_hooks              hold sleep off while a coding agent is working. On by
                           default. Off does NOT stop agents reporting: they are
                           still shown in the menu bar while they work, the Mac
                           just sleeps normally unless you hold it awake
  advisory_checks          check getgoguma.com once a day for word of a bug or
                           a fix. Off for anyone who installed before it
                           existed. Does nothing in a build with no signing key
                           compiled in, which is every release so far.`

var cmdConfig = &Command{
	Name:    "config",
	Summary: "view or change settings",
	Usage:   configUsage,
	Run: func(ctx *Context, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: goguma config get|set ...\n\n%s", configUsage)
		}
		switch args[0] {
		case "get", "show", "list":
			return configGet(ctx, args[1:])
		case "set":
			return configSet(ctx, args[1:])
		default:
			return fmt.Errorf("unknown config subcommand %q; use get or set", args[0])
		}
	},
}

func configGet(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var resp ipc.ConfigResp
	if err := callDaemon(ctx, ipc.OpConfigGet, nil, &resp); err != nil {
		// Fall back to reading the file directly, so `config get` still works
		// for inspection when the daemon is not running.
		cfg, warnings, ferr := config.Load(ctx.Layout.ConfigFile())
		if ferr != nil {
			return errDaemonDown()
		}
		resp = ipc.ConfigResp{Config: cfg, Warnings: warnings}
		ctx.Out.Line(ctx.Out.Muted("(background service not running · showing the config file)"))
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp.Config)
	}

	c := resp.Config
	r := ctx.Out
	r.KeyValue([][2]string{
		{"wake_buffer", c.WakeBuffer.String()},
		{"default_ceiling", c.DefaultCeiling.String()},
		{"min_ceiling", c.MinCeiling.String()},
		{"max_ceiling", c.MaxCeiling.String()},
		{"ceiling_multiplier", fmt.Sprintf("%.2f", c.CeilingMultiplier)},
		{"history_window", fmt.Sprintf("%d runs", c.HistoryWindow)},
		{"min_runs_for_estimate", fmt.Sprintf("%d runs", c.MinRunsForEstimate)},
		{"thermal_cutout_c", fmt.Sprintf("%.0f°C", c.ThermalCutoutC)},
		{"low_battery_cutout_pct", fmt.Sprintf("%d%%", c.LowBatteryCutoutPct) +
			// No longer states a single wake floor, because there is not one:
			// the floor is this plus whatever the job has been measured to
			// cost, so it differs per job.
			r.Muted("  holds released below this; wakes need this plus the job's own drain")},
		{"cutout_rearm_margin_pct", fmt.Sprintf("%d%%", c.CutoutRearmMarginPct) +
			r.Muted("  headroom above the cutout before a released hold resumes")},
		{"cutout_rearm_margin_c", fmt.Sprintf("%.0f°C", c.CutoutRearmMarginC) +
			r.Muted("  how far it must cool before holds resume")},
		{"webhook_url", orNone(r, c.WebhookURL)},
		{"notify_on_missed_job", fmt.Sprintf("%t", c.NotifyOnMissedJob)},
		{"use_wake_or_power_on", fmt.Sprintf("%t", c.UseWakeOrPowerOn)},
		{"sleep_after_wake", fmt.Sprintf("%t", c.SleepAfterWake)},
		{"advisory_checks", fmt.Sprintf("%t", c.AdvisoryChecks)},
		{"agent_hooks", fmt.Sprintf("%t", c.AgentHooks)},
		{"min_import_interval", c.MinImportInterval.String()},
	})

	if len(resp.Warnings) > 0 {
		r.Blank()
		r.Section("  adjusted on load")
		for _, w := range resp.Warnings {
			r.Problem(w, "")
		}
	}
	return nil
}

func orNone(r interface{ Muted(string) string }, s string) string {
	if s == "" {
		return r.Muted("not set")
	}
	return s
}

func configSet(ctx *Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: goguma config set <key> <value>\n\nvalid keys:\n  %s",
			strings.Join(daemon.ConfigKeys(), "\n  "))
	}
	key, value := args[0], strings.Join(args[1:], " ")

	var resp ipc.ConfigResp
	if err := callDaemon(ctx, ipc.OpConfigSet,
		ipc.ConfigSetReq{Key: key, Value: value}, &resp); err != nil {
		return err
	}

	r := ctx.Out
	r.Printf("%s %s = %s\n", r.Good(r.Sym().OK), r.Bold(key), value)
	for _, w := range resp.Warnings {
		r.Problem(w, "")
	}
	return nil
}
