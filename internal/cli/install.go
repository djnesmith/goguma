package cli

import (
	"flag"
	"fmt"
	"github.com/junnam586/goguma/internal/agenthooks"
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

	// Summarised, not enumerated.
	//
	// This listed all nine steps and then echoed all nine again as they ran:
	// eighteen lines to say nine things, on the screen somebody is reading to
	// decide whether to type their password. The list is here to be understood
	// before it is agreed to, and four binaries landing in one directory is one
	// fact, not four.
	//
	// The progress below still names each step as it happens. That is where the
	// detail belongs: while it is happening, next to the password prompt, in the
	// place a failure would appear.
	privileged := 0
	for _, s := range plan.Steps {
		if s.Privileged {
			privileged++
		}
	}
	r.Line(r.Bold("goguma will:"))
	r.Blank()
	r.Printf("  %s %s\n", r.Muted("·"),
		fmt.Sprintf("install the goguma commands to %s", l.BinDir))
	r.Printf("  %s %s\n", r.Muted("·"),
		"start a background service, and start it again at login")
	if privileged > 0 {
		r.Printf("  %s %s%s\n", r.Muted("·"),
			"install a small helper that runs as root, which is the only way to "+
				"wake a sleeping Mac",
			r.Warn("  (asks for your Mac password)"))
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

	// Last, after the scan it describes. Also after the PATH warning, so a
	// machine that needs one is told before being told it is finished.
	printSetupDone(r)
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
	managed := waitForAdoption(ctx, r)
	if len(managed) == 0 {
		// A finished search that found nothing, which is now what this means:
		// the wait above gave the scanner its time rather than reporting
		// whatever had turned up by the second the prompt returned.
		r.Blank()
		r.Printf("%s %s\n", r.Muted(r.Sym().Idle),
			r.Muted("no scheduled jobs on this machine yet · anything you add later shows up on its own"))
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
	// Twenty seconds, for the same reason the helper gets twenty: the first
	// launch of a newly downloaded binary is the slow one, and it is also the
	// only launch a new user ever watches.
	// A bar, because twenty seconds of a still cursor after a password prompt
	// reads as a hang. It shows the share of the allowance used rather than
	// progress towards success, so a full bar means "about to give up".
	ok := r.WaitFor("  starting the background service", 40, 500*time.Millisecond, func() bool {
		if err := ipc.Do(ctx.Socket, ipc.OpPing, nil, &st); err == nil {
			lastErr = nil
			return true
		} else {
			lastErr = err
			return false
		}
	})
	_ = ok

	if lastErr != nil {
		r.Problem("the background service hasn't answered yet · it may still be starting",
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
	// Forty attempts at half a second: twenty seconds, against the three this
	// used to allow.
	//
	// The old window was measured against a helper that had been run before.
	// The first launch of a freshly downloaded, freshly notarized binary also
	// pays for Gatekeeper to validate it, and that can take longer than three
	// seconds on its own. Anyone installing from the website therefore got
	// "the privileged helper didn't start" for a helper that was running as
	// root and answering a few seconds later, on the one screen that decides
	// whether they think the tool works.
	r.WaitFor("  starting the privileged helper", 40, 500*time.Millisecond, func() bool {
		lastHelperErr = ipc.DoTimeout(paths.HelperSocket, 2*time.Second,
			ipc.OpHelperStatus, nil, &hs)
		return lastHelperErr == nil
	})
	if lastHelperErr != nil {
		r.Problem("the privileged helper hasn't answered yet · it may still be starting",
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

	// Coding agents are the daemon's job, not the installer's.
	//
	// It reconciles them against the agent_hooks setting on every start, so an
	// agent installed next month is set up without anyone remembering to, and
	// the toggle in the app takes effect where it is flipped. Doing it here as
	// well would only mean doing it twice.

	// Where this ends, and where everything after it happens.
	//
}

// printSetupDone is the last thing setup says, and has to be said last.
//
// It lived at the end of verifyInstall, which runs before the job scan is
// waited for, so the terminal announced "Setup is done. You can close this
// window." and then sat there for another fifteen seconds finding jobs. Anyone
// who took it at its word closed the window mid-scan; anyone who did not watched
// a finished tool keep working and reasonably concluded something was stuck.
//
// Setup runs in Terminal because installing a root helper needs a real password
// prompt, and nothing else about goguma does. Without this, a window full of
// command output is the last thing a new user sees, and the tool reads as one
// you drive by typing: the menu bar app they just downloaded goes unmentioned by
// the thing they were told to run.
func printSetupDone(r *render.Renderer) {
	r.Blank()
	r.Printf("%s %s\n", r.Good(r.Sym().OK), r.Bold("Setup is done. You can close this window."))
	r.Printf("  %s\n", r.Muted("Everything else is in the menu bar: what is being held awake, "))
	r.Printf("  %s\n", r.Muted("what is coming next, the job list, and every setting."))

	// A link, printed once, at the end.
	//
	// Not a prompt: an install that has just asked for a root password is the
	// worst possible moment to also ask for an email address, and a form here
	// would be the account this tool spends a whole page promising it does not
	// have. A URL someone can ignore costs nothing and asks for nothing.
	r.Blank()
	r.Printf("  %s\n", r.Muted("to hear when something breaks or gets fixed:"))
	r.Printf("  %s\n", r.Accent(signupURL))
	printStarAsk(r)
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
		for _, h := range agenthooks.Harnesses {
			if h.Present() && agenthooks.Inspect(h, gogumaBinDir()).Installed {
				r.Printf("  %s %s %s\n", r.Muted("·"), shortenHome(h.Path()),
					r.Muted("(goguma's line only)"))
			}
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

		// Take goguma's line back out of every agent it was added to, before
		// the binary it names stops existing.
		//
		// Left behind, that line is a command that cannot run, fired on every
		// prompt and every tool call of an agent that has nothing to do with
		// goguma any more. Uninstalling a tool must not leave the tools around
		// it worse off, and this is the only thing goguma writes that lives
		// outside its own directories.
		removeAgentHooks(ctx)

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

// removeAgentHooks strips goguma's hook from every agent that has it.
//
// Best effort and quiet about nothing to do: an uninstall should not fail
// because somebody else's config file is read-only, and most machines have no
// coding agent on them at all.
func removeAgentHooks(ctx *Context) {
	r := ctx.Out
	binDir := gogumaBinDir()
	for _, h := range agenthooks.Harnesses {
		if !h.Present() || !agenthooks.Inspect(h, binDir).Installed {
			continue
		}
		doc, err := agenthooks.ReadConfig(h.Path())
		if err != nil {
			r.Problem(fmt.Sprintf("couldn't read %s to remove goguma's hook", shortenHome(h.Path())), err.Error())
			continue
		}
		if _, err := agenthooks.WriteConfig(h.Path(), agenthooks.Apply(doc, h, binDir, true)); err != nil {
			r.Problem(fmt.Sprintf("couldn't remove goguma's hook from %s", shortenHome(h.Path())), err.Error())
			continue
		}
		r.Printf("%s %s\n", r.Good(r.Sym().OK),
			r.Muted("removed goguma's line from "+h.Name))
	}
}

// waitForAdoption syncs, then waits until the job count stops changing.
//
// One sync and one list was not enough. `SyncNow` is synchronous, so the call
// does finish what it starts, but a daemon that came up two seconds ago has not
// finished working out what is on the machine, and the first pass found a
// fraction of it. Setup then printed that fraction, or printed nothing, and the
// rest arrived in the menu bar ten seconds after the terminal said it was done
// — so the number a new user was given was wrong at the moment they read it.
//
// Waiting for the count to settle rather than sleeping a fixed amount: a
// machine with two cron lines is done almost at once and should not be held for
// somebody else's worst case, and a machine with fifty is not finished just
// because a timer expired. Three identical counts in a row, half a second
// apart, with a hard stop so a scanner that never settles cannot hang setup.
func waitForAdoption(ctx *Context, r *render.Renderer) []ipc.JobView {
	var syncResp struct {
		Added int `json:"added"`
	}
	_ = callDaemon(ctx, ipc.OpSync, nil, &syncResp)

	list := func() []ipc.JobView {
		var resp ipc.JobsListResp
		if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
			return nil
		}
		var managed []ipc.JobView
		for _, v := range resp.Jobs {
			if v.Job.Managed {
				managed = append(managed, v)
			}
		}
		return managed
	}

	// Two thresholds, because zero means two different things.
	//
	// A count that has stopped moving is finished, and three readings half a
	// second apart is enough to say so once something has been found. A count
	// of zero is not the same claim: early on it means the scan has not begun,
	// and settling on it would report "no scheduled jobs" to somebody with a
	// crontab full of them. So zero has to hold for longer before it is
	// believed — long enough to be an answer, short enough that a machine which
	// genuinely has none is not held for the full allowance.
	const settledAfter, settledAtZero = 3, 8
	last, stable := -1, 0
	var jobs []ipc.JobView

	r.WaitFor("  reading this machine's schedulers", 40, 500*time.Millisecond, func() bool {
		jobs = list()
		if len(jobs) == last {
			stable++
		} else {
			last, stable = len(jobs), 0
		}
		if last == 0 {
			return stable >= settledAtZero
		}
		return stable >= settledAfter
	})
	return jobs
}
