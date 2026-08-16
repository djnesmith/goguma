package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/daemon"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
)

var cmdAwake = &Command{
	Name:    "awake",
	Summary: "keep this machine awake for a while",
	Usage: `goguma awake <duration>
goguma awake off

Holds sleep off for a fixed period whether or not a job is running, the
manual escape hatch, for when you are the reason the machine has to stay up.

  goguma awake 30m
  goguma awake 1h30m
  goguma awake 2h
  goguma awake off      release it early

Asking again replaces the window rather than adding to it, so 'awake 10m'
followed by 'awake 1h' leaves you awake for an hour from now, not seventy
minutes. The window is clamped to between 1 minute and 12 hours.

The safety cutouts still apply: a lid-closed machine that overheats or runs
low on battery releases this hold exactly as it releases a job's. It is also
forgotten if the background service restarts, and it is never recorded as a job run.`,
	Run: func(ctx *Context, args []string) error {
		_, positional := hoistFlags(args)
		if len(positional) == 0 {
			return fmt.Errorf("how long? try 'goguma awake 30m', or 'goguma awake off' to release")
		}

		// Joined rather than taking args[0], so `awake 1h 30m` works; that is
		// exactly how HumanDuration prints a duration back to the user, and a
		// form the CLI prints is a form it should accept.
		arg := strings.Join(positional, " ")

		var want time.Duration
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "off", "stop", "cancel", "none":
		default:
			parsed, err := model.ParseDuration(arg)
			if err != nil {
				return err
			}
			// A zero or negative duration cancels rather than erroring: it is
			// the same intent as "off", expressed arithmetically.
			want = max(parsed, 0)
		}

		var resp ipc.KeepAwakeResp
		if err := callDaemon(ctx, ipc.OpKeepAwake,
			ipc.KeepAwakeReq{For: model.Duration(want)}, &resp); err != nil {
			return err
		}

		r := ctx.Out
		if !resp.Active || resp.Until == nil {
			r.Printf("%s keep-awake released · the machine can sleep normally\n", r.Muted(r.Sym().Idle))
			return nil
		}

		// Rounded to the second before rendering: the round trip costs a few
		// milliseconds, and reporting "29m 59s" for a window the user asked to
		// be 30m long looks like something quietly went wrong.
		until := resp.Until.Local()
		r.Printf("%s staying awake for %s %s\n", r.Good(r.Sym().Holding),
			r.Bold(model.HumanDuration(time.Until(until).Round(time.Second))),
			r.Muted("· until "+until.Format("15:04")))
		if resp.Clamped {
			r.Printf("  %s\n", r.Muted(fmt.Sprintf("adjusted to the %s-%s range",
				model.HumanDuration(daemon.MinKeepAwake), model.HumanDuration(daemon.MaxKeepAwake))))
		}
		r.Printf("  %s\n", r.Muted("safety cutouts still apply · release early with: goguma awake off"))
		return nil
	},
}
