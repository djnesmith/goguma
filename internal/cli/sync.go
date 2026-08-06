package cli

import (
	"fmt"
	"strings"

	"github.com/junnam/wakeguard/internal/daemon"
	"github.com/junnam/wakeguard/internal/ipc"
)

var cmdSync = &Command{
	Name:    "sync",
	Summary: "re-read watched schedulers and adopt any new jobs",
	Usage: `wakeguard sync

Re-reads every scheduler listed in auto_adopt and registers jobs that have
appeared since the last check, retiring ones that have gone.

This normally happens on its own every couple of minutes. Run it by hand when
you have just created a job and do not want to wait.

To start watching a scheduler:

    wakeguard config set auto_adopt hermes

Only schedulers that run their own jobs can be watched. Crontab and launchd
entries are excluded: covering those properly means editing a command line,
which is a decision only you can make — use 'wakeguard import' for those.`,
	Run: func(ctx *Context, args []string) error {
		var resp struct {
			Added int `json:"added"`
		}
		if err := callDaemon(ctx, ipc.OpSync, nil, &resp); err != nil {
			return err
		}

		r := ctx.Out
		var cfg ipc.ConfigResp
		_ = callDaemon(ctx, ipc.OpConfigGet, nil, &cfg)

		// Report what is actually being watched, not what is literally in the
		// file. An unconfigured install watches everything adoptable, so
		// reading the raw field would claim nothing is watched while jobs are
		// visibly being adopted.
		watching := daemon.EffectiveAutoAdopt(cfg.Config.AutoAdopt)
		if len(watching) == 0 {
			r.Printf("%s automatic adoption is turned off.\n", r.Muted(r.Sym().Bullet))
			r.Blank()
			if avail := daemon.AdoptableSources(); len(avail) > 0 {
				r.Printf("  available to watch: %s\n", r.Bold(strings.Join(avail, ", ")))
				r.Printf("  %s\n", r.Accent("wakeguard config set auto_adopt "+strings.Join(avail, ",")))
			} else {
				r.Line(r.Muted("  no scheduler on this machine supports automatic adoption"))
			}
			return nil
		}

		switch {
		case resp.Added > 0:
			r.Printf("%s adopted %d new job(s) from %s\n", r.Good(r.Sym().OK),
				resp.Added, strings.Join(watching, ", "))
			r.Printf("  %s\n", r.Muted("see them with: wakeguard list"))
		case resp.Added < 0:
			r.Printf("%s retired %d job(s) that no longer exist in their source\n",
				r.Good(r.Sym().OK), -resp.Added)
		default:
			r.Printf("%s up to date with %s\n", r.Good(r.Sym().OK),
				strings.Join(watching, ", "))
		}
		return nil
	},
}

var _ = fmt.Sprintf
