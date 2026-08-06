package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junnam/wakeguard/internal/install"
	"github.com/junnam/wakeguard/internal/ipc"
	"github.com/junnam/wakeguard/internal/model"
	"github.com/junnam/wakeguard/internal/paths"
	"github.com/junnam/wakeguard/internal/render"
	"github.com/junnam/wakeguard/internal/scan"
	"github.com/junnam/wakeguard/internal/schedule"
)

var cmdInstall = &Command{
	Name:    "install",
	Summary: "install and start the background services",
	Usage: `wakeguard install [--no-helper] [--dry-run]

Installs the binaries, registers the background daemon so it starts at login,
and installs the privileged helper.

The helper is a separate root service that does exactly two things: block
sleep, and register a wake with the OS. All scheduling and policy stays in the
unprivileged daemon. Installing it requires your password once.

  --no-helper   skip the privileged helper. WakeGuard will still hold sleep
                off with the lid OPEN, but cannot hold a lid-closed machine
                awake, and cannot schedule OS wakes — which is most of what
                it is for.
  --dry-run     print the steps without doing anything`,
	Run: runInstall,
}

func runInstall(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noHelper := fs.Bool("no-helper", false, "skip the privileged helper")
	dryRun := fs.Bool("dry-run", false, "print steps only")
	if err := fs.Parse(args); err != nil {
		return err
	}

	r := ctx.Out
	l := ctx.Layout
	if err := l.EnsureDirs(); err != nil {
		return fmt.Errorf("preparing %s: %w", l.StateDir, err)
	}

	plan, err := install.BuildPlan(l, !*noHelper)
	if err != nil {
		return err
	}

	r.Line(r.Bold("WakeGuard will:"))
	r.Blank()
	privileged := 0
	for i, s := range plan.Steps {
		tag := ""
		if s.Privileged {
			tag = r.Warn("  (asks for your Mac password)")
			privileged++
		}
		r.Printf("  %d. %s%s\n", i+1, s.Description, tag)
	}
	r.Blank()

	if privileged > 0 {
		// Said before the first step runs, not discovered at step 7.
		//
		// The prompt appears partway through a list of things that are already
		// succeeding, and `sudo` gives it no context of its own — so without
		// this it reads as an unexplained password box from a tool that was
		// working a second ago. It also fails outright when this is run
		// anywhere without a terminal, which is worth knowing up front.
		r.Blank()
		r.Line(r.Bold("You will be asked for your Mac login password."))
		r.Printf("%s\n", r.Muted(
			"That is macOS asking, not WakeGuard — it is the standard `sudo` prompt, and it is"))
		r.Printf("%s\n", r.Muted(
			"needed to install the piece that can wake a sleeping Mac. Nothing is typed to us and"))
		r.Printf("%s\n", r.Muted(
			"nothing is stored. Run this in Terminal: a password cannot be entered without one."))
		r.Blank()
	}

	if *dryRun {
		r.Line(r.Muted("Dry run, nothing was changed."))
		return nil
	}
	if privileged > 0 {
		r.Printf("%s\n", r.Muted(fmt.Sprintf(
			"%d step(s) run as root via sudo. The helper's entire job is to block sleep and", privileged)))
		r.Printf("%s\n", r.Muted("schedule wakes; it holds no schedules or policy of its own."))
		r.Blank()
	}

	for _, s := range plan.Steps {
		r.Printf("  %s %s… ", r.Muted("→"), s.Description)
		if err := s.Run(); err != nil {
			r.Printf("%s\n", r.Danger("failed"))
			return err
		}
		r.Printf("%s\n", r.Good("done"))
	}

	r.Blank()
	verifyInstall(ctx, !*noHelper)

	if !onPath(l.BinDir) {
		r.Blank()
		r.Problem(
			fmt.Sprintf("%s is not on your PATH, so the wakeguard command will not be found", l.BinDir),
			fmt.Sprintf(`echo 'export PATH="%s:$PATH"' >> ~/.zshrc`, l.BinDir))
	}

	reportAdopted(ctx)

	r.Blank()
	r.Line(r.Bold("Next:"))
	r.Printf("  %s   look for anything else worth waking for\n", r.Accent("wakeguard import"))
	r.Printf("  %s   see what it is doing\n", r.Accent("wakeguard status"))
	return nil
}

// reportAdopted tells the user what WakeGuard started covering by itself.
//
// Adoption happens without being asked, which is the right default — installing
// the tool is the statement that jobs should survive sleep, and a second opt-in
// mostly produces installs that quietly do nothing. But acting unprompted is
// only acceptable if it is not also invisible. The cost is stated in wakes per
// day, because a tool whose purpose is saving battery owes the user an account
// of the battery it intends to spend.
func reportAdopted(ctx *Context) {
	r := ctx.Out

	var resp ipc.JobsListResp
	if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
		return
	}
	var managed []ipc.JobView
	for _, v := range resp.Jobs {
		if v.Job.Managed {
			managed = append(managed, v)
		}
	}
	if len(managed) == 0 {
		return
	}

	r.Blank()
	r.Printf("%s now waking for %s found on this machine:\n",
		r.Good(r.Sym().OK), r.Bold(fmt.Sprintf("%d job(s)", len(managed))))
	for _, v := range managed {
		sched := v.ScheduleDisplay
		if sched == "" {
			sched = v.Job.Schedule
		}
		r.Printf("    %s %-32s %s\n", r.Muted(r.Sym().Bullet),
			render.Truncate(v.Job.Name, 32), r.Muted(sched))
	}

	if cost, ok := estimateWakeCost(ctx, managed); ok {
		r.Blank()
		line := "  this costs " + cost.Summary()
		if cost.Heavy() {
			r.Printf("  %s %s\n", r.Warn(r.Sym().Warn), r.Warn(cost.Summary()))
			if cost.Busiest != "" {
				r.Printf("    %s\n", r.Muted(fmt.Sprintf(
					"most of it is %q at %.0f wakes a day, consider 'wakeguard disable %s'",
					cost.Busiest, cost.BusiestWakes, model.Slug(cost.Busiest))))
			}
		} else {
			r.Printf("%s\n", r.Muted(line))
		}
	}
	r.Printf("  %s\n", r.Muted("turn any of it off with 'wakeguard disable <name>', "+
		"or all of it with 'wakeguard config set auto_adopt off'"))
}

// estimateWakeCost prices the adopted set in wakes per day.
func estimateWakeCost(ctx *Context, views []ipc.JobView) (scan.Cost, bool) {
	var cfg ipc.ConfigResp
	if err := callDaemon(ctx, ipc.OpConfigGet, nil, &cfg); err != nil {
		return scan.Cost{}, false
	}
	perWake := cfg.Config.WakeBuffer.D() + cfg.Config.WakeOnlyHold.D()

	now := time.Now()
	cands := make([]scan.Candidate, 0, len(views))
	for _, v := range views {
		s, err := schedule.ParseAt(v.Job.Schedule, time.Local, v.Job.ScheduleAnchor())
		if err != nil {
			continue
		}
		cands = append(cands, scan.Candidate{
			Entry:  scan.Entry{Name: v.Job.Name, Schedule: v.Job.Schedule},
			Parsed: s,
		})
	}
	if len(cands) == 0 {
		return scan.Cost{}, false
	}
	return scan.EstimateCost(cands, now, perWake), true
}

// verifyInstall confirms the services actually came up.
//
// A successful `launchctl bootstrap` does not mean the daemon is running and
// answering — reporting success without checking is how an install appears to
// work and silently does nothing.
func verifyInstall(ctx *Context, expectHelper bool) {
	r := ctx.Out

	var st struct {
		Version string `json:"version"`
	}
	var lastErr error
	for range 20 {
		if err := ipc.Do(ctx.Socket, ipc.OpPing, nil, &st); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}

	if lastErr != nil {
		r.Problem("the daemon did not start · check the log for why",
			"tail -n 40 "+filepath.Join(ctx.Layout.LogDir, "daemon.err.log"))
		return
	}
	r.Printf("%s daemon is running (%s)\n", r.Good(r.Sym().OK), st.Version)

	if !expectHelper {
		r.Printf("%s helper was skipped, lid-closed holds and OS wakes are unavailable\n",
			r.Warn(r.Sym().Warn))
		return
	}

	full, err := fetchStatus(ctx)
	if err == nil && full.HelperConnected {
		r.Printf("%s privileged helper is connected (%s)\n", r.Good(r.Sym().OK), full.HelperVersion)
		return
	}
	// The daemon connects to the helper lazily, so give it a moment.
	time.Sleep(1500 * time.Millisecond)
	if full, err = fetchStatus(ctx); err == nil && full.HelperConnected {
		r.Printf("%s privileged helper is connected (%s)\n", r.Good(r.Sym().OK), full.HelperVersion)
		return
	}
	r.Problem("the privileged helper is not answering yet, lid-closed holds will not work until it does",
		"wakeguard doctor")
}

func onPath(dir string) bool {
	clean := filepath.Clean(dir)
	for _, p := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if filepath.Clean(p) == clean {
			return true
		}
	}
	return false
}

var cmdUninstall = &Command{
	Name:    "uninstall",
	Summary: "remove the services and binaries",
	Usage: `wakeguard uninstall [--keep-state] [--yes]

Stops and removes the daemon, the privileged helper, and the installed
binaries. Removing the helper needs your password.

  --keep-state   keep jobs, config, and run history in the state directory
  --yes          do not ask for confirmation`,
	Run: func(ctx *Context, args []string) error {
		fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		keepState := fs.Bool("keep-state", false, "keep jobs and history")
		assumeYes := fs.Bool("yes", false, "skip confirmation")
		if err := fs.Parse(args); err != nil {
			return err
		}

		r := ctx.Out
		l := ctx.Layout

		r.Line(r.Bold("This will remove:"))
		for _, a := range install.Artifacts(l) {
			marker := r.Muted("  (not present)")
			if _, err := os.Stat(a); err == nil {
				marker = ""
			}
			r.Printf("  %s %s%s\n", r.Muted("·"), a, marker)
		}
		if *keepState {
			r.Printf("  %s %s %s\n", r.Muted("·"), l.StateDir, r.Good("(kept)"))
		} else {
			r.Printf("  %s %s %s\n", r.Muted("·"), l.StateDir,
				r.Warn("including all jobs and run history"))
		}
		r.Blank()

		if !*assumeYes {
			fmt.Fprintf(os.Stdout, "Continue? [y/N]: ")
			line, err := readLine()
			if err != nil || !strings.EqualFold(strings.TrimSpace(line), "y") {
				r.Line(r.Muted("Cancelled, nothing was changed."))
				return nil
			}
		}

		errs := install.Uninstall(l, *keepState)
		for _, e := range errs {
			r.Problem(e.Error(), "")
		}
		if len(errs) > 0 {
			return fmt.Errorf("uninstall finished with %d problem(s)", len(errs))
		}

		r.Printf("%s uninstalled cleanly\n", r.Good(r.Sym().OK))
		if *keepState {
			r.Printf("  %s\n", r.Muted("jobs and history are still in "+l.StateDir))
		}
		return nil
	},
}

// helperSocketPath is exposed for doctor.
func helperSocketPath() string { return paths.HelperSocket }
