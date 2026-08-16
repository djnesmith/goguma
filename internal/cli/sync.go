package cli

import (
	"strings"

	"github.com/junnam586/goguma/internal/daemon"
	"github.com/junnam586/goguma/internal/ipc"
)

var cmdSync = &Command{
	Name:    "sync",
	Summary: "re-read watched schedulers and adopt any new jobs",
	Usage: `goguma sync

Re-reads every scheduler listed in auto_adopt and registers jobs that have
appeared since the last check, retiring ones that have gone.

This normally happens on its own every couple of minutes. Run it by hand when
you have just created a job and don't want to wait.

To start watching a scheduler:

    goguma config set auto_adopt hermes

Only schedulers that run their own jobs can be watched. Crontab and launchd
entries are excluded: covering those properly means editing a command line,
which is a decision only you can make; use 'goguma import' for those.`,
	Run: func(ctx *Context, args []string) error {
		var resp struct {
			Added   int `json:"added"`
			Updated int `json:"updated"`
			Retired int `json:"retired"`
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
				r.Printf("  %s\n", r.Accent("goguma config set auto_adopt "+strings.Join(avail, ",")))
			} else {
				r.Line(r.Muted("  no scheduler on this machine supports automatic adoption"))
			}
			return nil
		}

		// Each kind of change is reported on its own. A single net delta hid
		// a rename entirely: one adoption plus one retirement summed to zero
		// and printed "up to date" while the job had lost its history and
		// learned ceiling.
		changed := false
		if resp.Added > 0 {
			changed = true
			r.Printf("%s adopted %d new job(s) from %s\n", r.Good(r.Sym().OK),
				resp.Added, strings.Join(watching, ", "))
		}
		if resp.Updated > 0 {
			changed = true
			r.Printf("%s updated %d job(s) that changed in their source\n",
				r.Good(r.Sym().OK), resp.Updated)
		}
		if resp.Retired > 0 {
			changed = true
			r.Printf("%s retired %d job(s) that no longer exist in their source\n",
				r.Good(r.Sym().OK), resp.Retired)
		}
		if changed {
			r.Printf("  %s\n", r.Muted("see the result with: goguma list"))
		} else {
			r.Printf("%s up to date with %s\n", r.Good(r.Sym().OK),
				strings.Join(watching, ", "))
		}
		return nil
	},
}
