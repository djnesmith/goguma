package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/install"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/render"
	"github.com/junnam586/goguma/internal/scan"
	"github.com/junnam586/goguma/internal/schedule"
)

var cmdInstall = &Command{
	Name:    "install",
	Summary: "install and start the background services",
	Usage: `goguma install [--no-helper] [--dry-run]

Installs the binaries, registers the background service so it starts at login,
and installs the privileged helper.

The helper is a separate root service that does exactly two things: block
sleep, and register a wake with the OS. All scheduling and policy stays in the
unprivileged background service. Installing it requires your password once.

  --no-helper   skip the privileged helper. goguma will still hold sleep
                off with the lid OPEN, but can't hold a lid-closed machine
                awake, and can't schedule OS wakes, which is most of what
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

	r.Line(r.Bold("goguma will:"))
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
		// succeeding, and `sudo` gives it no context of its own, so without
		// this it reads as an unexplained password box from a tool that was
		// working a second ago. It also fails outright when this is run
		// anywhere without a terminal, which is worth knowing up front.
		r.Blank()
		r.Line(r.Bold("macOS will ask for your login password."))
		r.Printf("%s\n", r.Muted(
			"Waking a sleeping Mac needs root access, so the helper that does it is"))
		r.Printf("%s\n", r.Muted(
			"installed as root. That prompt is macOS's own sudo. Your password goes to"))
		r.Printf("%s\n", r.Muted(
			"macOS and nowhere else, and sudo needs a terminal, which is why this is one."))

		// What is about to be installed as root, checked rather than asserted.
		//
		// Everything else printed here is the tool describing itself. This is
		// the operating system describing the actual bytes, and it is the only
		// line on the screen that a tampered build could not produce.
		if err := reportHelperSignature(r); err != nil {
			return err
		}
		r.Blank()
	}

	if *dryRun {
		r.Line(r.Muted("Dry run, nothing was changed."))
		return nil
	}
	if privileged > 0 {
		r.Printf("%s\n", r.Muted(fmt.Sprintf(
			"%s run as root. The helper only blocks sleep and schedules wakes; it holds",
			pluralSteps(privileged))))
		r.Printf("%s\n", r.Muted(
			"no schedules or policy of its own, and `goguma uninstall` removes it."))
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
			fmt.Sprintf("%s isn't on your PATH, so the goguma command won't be found", l.BinDir),
			fmt.Sprintf(`echo 'export PATH="%s:$PATH"' >> ~/.zshrc`, l.BinDir))
	}

	reportAdopted(ctx)

	r.Blank()
	r.Line(r.Bold("Next:"))
	r.Printf("  %s   look for anything else worth waking for\n", r.Accent("goguma import"))
	r.Printf("  %s   see what it is doing\n", r.Accent("goguma status"))
	return nil
}

// reportAdopted tells the user what goguma started covering by itself.
//
// Adoption happens without being asked, which is the right default: installing
// the tool is the statement that jobs should survive sleep, and a second opt-in
// mostly produces installs that quietly do nothing. But acting unprompted is
// only acceptable if it is not also invisible. The cost is stated in wakes per
// day, because a tool whose purpose is saving battery owes the user an account
// of the battery it intends to spend.
func reportAdopted(ctx *Context) {
	r := ctx.Out

	// Force the first sync rather than racing it. Two seconds after boot the
	// daemon has usually not scanned yet, so listing immediately reported an
	// empty machine and install said nothing, while the jobs were adopted
	// moments later in silence. That contradicts the whole disclosure: the
	// unprompted adoption below is only acceptable because install states it.
	var syncResp struct {
		Added int `json:"added"`
	}
	_ = callDaemon(ctx, ipc.OpSync, nil, &syncResp)

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
					"most of it is %q at %.0f wakes a day, consider 'goguma disable %s'",
					cost.Busiest, cost.BusiestWakes, model.Slug(cost.Busiest))))
			}
		} else {
			r.Printf("%s\n", r.Muted(line))
		}
	}
	r.Printf("  %s\n", r.Muted("turn any of it off with 'goguma disable <name>', "+
		"or all of it with 'goguma config set auto_adopt off'"))
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
// answering; reporting success without checking is how an install appears to
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
		r.Problem("the background service didn't start · check the log for why",
			"tail -n 40 "+filepath.Join(ctx.Layout.LogDir, "daemon.err.log"))
		return
	}
	r.Printf("%s background service is running (%s)\n", r.Good(r.Sym().OK), st.Version)

	if !expectHelper {
		r.Printf("%s helper was skipped, lid-closed holds and OS wakes are unavailable\n",
			r.Warn(r.Sym().Warn))
		return
	}

	// Ask the helper, not the daemon.
	//
	// This used to read the daemon's cached "am I linked to the helper" flag,
	// which answers a different question. That flag only turns true once the
	// daemon has made a successful call, and a call issued while the helper is
	// being swapped can sit on the socket for the full HelperTimeout, so the
	// daemon can take tens of seconds to notice a helper that came up in
	// under one. Waiting 1.5s for that and then declaring the helper "not
	// answering" reported a failed install on a helper that was running as
	// root and replying in a quarter of a second.
	//
	// The installer is running as the helper's owner and the socket is
	// owner-only, so it can dial the helper itself. That is the actual claim
	// being made here, checked against the actual process.
	var hs ipc.HelperStatusResp
	var lastHelperErr error
	for range 12 {
		if lastHelperErr = ipc.DoTimeout(paths.HelperSocket, 2*time.Second,
			ipc.OpHelperStatus, nil, &hs); lastHelperErr == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastHelperErr != nil {
		r.Problem("the privileged helper didn't start, lid-closed holds and OS wakes won't work",
			"goguma doctor")
		return
	}
	r.Printf("%s privileged helper is running (%s)\n", r.Good(r.Sym().OK), hs.Version)

	// The daemon links to it on its own schedule, and until it does, `status`
	// says the helper is down. Say so here rather than leaving the user to
	// find a contradiction between this output and the next command they run.
	if full, err := fetchStatus(ctx); err == nil && !full.HelperConnected {
		r.Printf("  %s\n", r.Muted("the background service picks it up within a minute"))
	}

	// A link, printed once, at the end.
	//
	// Not a prompt: an install that has just asked for a root password is the
	// worst possible moment to also ask for an email address, and a form here
	// would be the account this tool spends a whole page promising it does not
	// have. A URL someone can ignore costs nothing and asks for nothing.
	r.Blank()
	r.Printf("  %s\n", r.Muted("to hear when something breaks or gets fixed:"))
	r.Printf("  %s\n", r.Accent(signupURL))
}

// signupURL is where people can leave an email address if they want to.
// Deliberately a page on the site rather than anything goguma posts to.
const signupURL = "https://getgoguma.com/updates"

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
	Usage: `goguma uninstall [--purge] [--yes]

Stops and removes the background service, the privileged helper, and the
installed binaries. Removing the helper needs your password.

Jobs, config, and run history are kept, so reinstalling picks up where you
left off. Duration history takes weeks of real runs to accumulate and can't
be reconstructed, so throwing it away is opt-in.

  --purge   also delete jobs, config, and run history
  --yes     don't ask for confirmation`,
	Run: func(ctx *Context, args []string) error {
		fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		purge := fs.Bool("purge", false, "also delete jobs, config, and history")
		assumeYes := fs.Bool("yes", false, "skip confirmation")
		if err := fs.Parse(args); err != nil {
			return err
		}
		keepState := !*purge

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
		if keepState {
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

		// Cancel the pending OS wake while the daemon can still reach the
		// helper. Pause does exactly the teardown uninstall needs (release
		// holds, cancel the wake, clear the bookkeeping); without it the
		// wake survived in the OS schedule and the machine woke once, or
		// with wake-or-power-on powered itself on, for a tool that was
		// already gone. Best-effort: a daemon that is not running has
		// nothing registered to cancel through it.
		_ = callDaemon(ctx, ipc.OpPause, nil, nil)

		errs := install.Uninstall(l, keepState)
		for _, e := range errs {
			r.Problem(e.Error(), "")
		}
		if len(errs) > 0 {
			return fmt.Errorf("uninstall finished with %d problem(s)", len(errs))
		}

		r.Printf("%s uninstalled cleanly\n", r.Good(r.Sym().OK))
		if keepState {
			r.Printf("  %s\n", r.Muted("jobs and history are still in "+l.StateDir))
		}
		return nil
	},
}

// helperSocketPath is exposed for doctor.
func helperSocketPath() string { return paths.HelperSocket }

// pluralSteps avoids "1 step(s)", which is a programmer declining to choose.
func pluralSteps(n int) string {
	if n == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", n)
}

// reportHelperSignature prints what macOS says about the binary that is about
// to be installed as root, and refuses to continue if it says the wrong thing.
//
// This is the difference between a promise and a check. Everything else the
// installer prints is goguma describing itself, which a modified copy would
// print just as convincingly. `codesign --verify` is the operating system
// reading the actual bytes, and a binary altered after signing fails it.
//
// A missing or unrunnable `codesign` prints nothing rather than claiming
// anything, because "we could not check" and "we checked and it is fine" must
// never look the same.
func reportHelperSignature(r *render.Renderer) error {
	src, err := install.HelperSource()
	if err != nil {
		// Nothing to verify yet; BuildPlan already failed for this reason if
		// it mattered.
		return nil
	}
	sig, err := install.VerifyHelper(src)
	if err != nil {
		// Returned, not printed. The dispatcher renders whatever comes back,
		// so printing here as well put the same failure on screen twice.
		return fmt.Errorf(
			"%w\n  refusing to install it as root; download goguma again from the releases page", err)
	}
	if desc := sig.Describe(); desc != "" {
		r.Blank()
		r.Printf("  %s %s\n", r.Good(r.Sym().OK), r.Muted("the helper is "+desc))
	}
	return nil
}
