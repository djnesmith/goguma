package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/detect"
	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/model"
	"github.com/junnam586/goguma/internal/power"
	"github.com/junnam586/goguma/internal/render"
	"github.com/junnam586/goguma/internal/scan"
	"github.com/junnam586/goguma/internal/schedule"
)

var cmdImport = &Command{
	Name:    "import",
	Summary: "find scheduled jobs on this machine worth waking for",
	Usage: `goguma import [--all] [--register] [--yes]

Scans your crontab, launchd agents, and systemd timers, then shows only the
entries that would actually be missed while the machine sleeps.

Reports by default and changes nothing. The daemon already adopts what it
finds, so the common reason to run this is to read the report: what is
scheduled, what has been missing runs, and how often. Registering by hand is
the exception, and it asks first.

Most scheduled entries on a typical machine are excluded for structural
reasons; they belong to the OS, they run continuously, they have no clock
trigger, or they fire so often that waking for them would cost more battery
than the job is worth. What remains is ranked by how often it has genuinely
been firing while this machine was asleep.

  --all       also list everything that was filtered out, with the reason
  --deep      also search the filesystem for schedulers goguma cannot read.
              These are reported as leads to investigate, never registered:
              a file containing cron syntax may be dormant, commented out, or
              written for a different machine.
  --register  ask how to cover each one, and register your answers
  --yes       register every one with the recommended option, without asking`,
	Run: runImport,
}

func runImport(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	showAll := fs.Bool("all", false, "list filtered entries too")
	// Reporting is the default, and registering is opt-in.
	//
	// This was the other way round, with `--dry-run` to make it safe. That
	// made the safe path the one you had to know about: a bare `import` read
	// like a report and then started asking to change things, and the command
	// could not be demonstrated without a flag on screen explaining that it
	// would not do anything.
	//
	// Adoption settled it. The daemon covers what it finds on its own, so the
	// reason to run this is almost always to read the report; registering by
	// hand is the exception and now says so.
	register := fs.Bool("register", false, "ask about each candidate and register the answers")
	assumeYes := fs.Bool("yes", false, "register every candidate with the recommended option")
	deep := fs.Bool("deep", false, "also search the filesystem for unknown schedulers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// --yes is a way of answering the questions, so it implies asking them.
	// Without this, `import --yes` reported and exited, which reads as the
	// flag having been ignored.
	readOnly := !*register && !*assumeYes

	r := ctx.Out
	bg := context.Background()

	r.Line(r.Muted("Scanning scheduled jobs on this machine…"))
	entries, coverage := scan.DiscoverAll(bg)

	// Miss-risk needs sleep history. The OS log reaches back further than
	// goguma has been installed, which is what makes the first run of
	// `import` useful rather than empty.
	hist, err := power.New().SleepHistory(14 * 24 * time.Hour)
	if err != nil || hist == nil {
		hist = &schedule.SleepHistory{}
	}

	now := time.Now()
	opts := scan.DefaultOptions()
	// Honor the configured import interval. `goguma config set
	// min_import_interval 15m` printed success and then this path kept
	// filtering with the shipped 1h default, so a 30-minute job the user
	// explicitly lowered the floor for stayed invisible with no explanation.
	// A daemon that is not running falls back to the defaults, which is the
	// same answer config get would give.
	var cfgResp ipc.ConfigResp
	if err := callDaemon(ctx, ipc.OpConfigGet, nil, &cfgResp); err == nil {
		if v := cfgResp.Config.MinImportInterval.D(); v > 0 {
			opts.MinInterval = v
		}
	}
	keep, filtered := scan.Evaluate(entries, hist, now, opts)

	sleepCoverage := hist.Coverage(now)
	printCoverage(r, coverage, len(entries))
	if sleepCoverage > 0 {
		asleep := totalAsleep(hist)
		r.Printf("This machine was asleep %s of the last %s.\n",
			r.Bold(model.HumanDuration(asleep)), model.HumanDuration(sleepCoverage))
	} else {
		r.Line(r.Muted("No sleep history is available yet, so miss-risk is unknown for now."))
	}
	r.Blank()

	if *showAll {
		printFiltered(r, filtered)
	}
	if *deep {
		printHints(ctx, r, bg)
	}

	if len(keep) == 0 {
		// Deliberately not a clean bill of health. The most misleading thing
		// this command could do is tell someone who is unsure what they have
		// scheduled that nothing needs attention, when the real answer may be
		// that their jobs live somewhere it never looked.
		r.Printf("%s Nothing was found that needs goguma.\n", r.Good(r.Sym().OK))
		r.Blank()
		r.Line(r.Muted("  Everything found either runs while the machine is awake, recovers on"))
		r.Line(r.Muted("  its own after a missed window, or runs continuously."))
		r.Blank()
		printBlindSpots(r, coverage)
		if !*showAll && len(filtered) > 0 {
			r.Printf("  %s to see all %d entries and why each was excluded.\n",
				r.Accent("goguma import --all"), len(filtered))
		}
		return nil
	}

	r.Printf("%s worth waking for:\n", r.Bold(fmt.Sprintf("%d of %d", len(keep), len(entries))))
	r.Blank()

	existing := existingJobIDs(ctx)
	registeredCmds := registeredCommands(ctx)
	registered := 0

	for i, c := range keep {
		printCandidate(r, i+1, c, existing)

		if readOnly {
			r.Blank()
			continue
		}
		action := chooseAction(ctx, c, keep, registeredCmds, *assumeYes)
		r.Blank()
		switch action {
		case actionSkip:
			continue
		case actionWrap, actionPattern, actionWakeOnly:
			if err := registerCandidate(ctx, c, action); err != nil {
				r.Problem(fmt.Sprintf("could not register %s: %v", c.Name, err), "")
				continue
			}
			registered++
		}
	}

	if readOnly {
		// Says what to do next rather than what did not happen. "Nothing was
		// changed" answers a worry nobody has when reporting is the default.
		if len(keep) > 0 {
			r.Line(r.Muted("goguma is already waking for these. " +
				"To wrap one for exact timing: goguma import --register"))
		}
		return nil
	}
	if registered > 0 {
		r.Printf("%s registered %d job(s).  %s\n", r.Good(r.Sym().OK), registered,
			r.Muted("check them with: goguma list"))
	}
	return nil
}

// printCoverage reports what was actually searched.
//
// Without this, `import` finding nothing is indistinguishable from `import`
// not looking in the right place, and the person most likely to run it is
// precisely the one who does not know where their jobs are defined.
func printCoverage(r *render.Renderer, cov []scan.Coverage, total int) {
	r.Printf("Examined %s.\n", r.Bold(fmt.Sprintf("%d scheduled entries", total)))
	for _, c := range cov {
		switch {
		case c.Err != nil:
			r.Printf("  %s %-9s %s\n", r.Warn(r.Sym().Warn), c.Source,
				r.Warn("could not be read: "+c.Err.Error()))
		case !c.Available:
			r.Printf("  %s %-9s %s\n", r.Muted(r.Sym().Disabled), c.Source,
				r.Muted("none on this machine  ·  "+c.Where))
		default:
			r.Printf("  %s %-9s %s\n", r.Good(r.Sym().OK), c.Source,
				fmt.Sprintf("%d found  %s", c.Found, r.Muted("·  "+c.Where)))
		}
	}
}

// printHints reports files that look like schedule definitions but are not
// registries goguma can read.
//
// These are shown as leads to investigate, never as jobs. A text search finds
// schedule *syntax*, which is not the same as a schedule that runs: a README
// explaining how to install a job, a crontab checked into a repository for a
// different machine, and a test fixture all contain valid cron lines and
// schedule nothing on this computer. Registering any of them would wake the
// machine for work that never happens.
func printHints(ctx *Context, r *render.Renderer, bg context.Context) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	r.Line(r.Muted("Searching for schedulers goguma does not know about…"))

	hints := scan.FindSchedulerHints(bg, []string{home}, 5, 40)
	hints = scan.VerifyAgainstInstalledCrontab(bg, hints)

	if len(hints) == 0 {
		r.Printf("  %s\n\n", r.Muted("no schedule-shaped files found outside the sources above"))
		return
	}

	r.Section(fmt.Sprintf("  possible schedulers (%d) · leads, not jobs", len(hints)))
	for _, h := range hints {
		var state string
		switch h.Live {
		case scan.LiveYes:
			state = r.Good("in effect")
		case scan.LiveNo:
			state = r.Muted("not installed here")
		default:
			state = r.Warn("cannot tell if it is in effect")
		}
		r.Printf("    %s  %s\n", shortenHomePath(h.Path, home), state)
		r.Printf("      %s\n", r.Muted(h.Why))
		if h.Sample != "" {
			r.Printf("      %s\n", r.Muted(render.Truncate(h.Sample, max(r.Width()-10, 40))))
		}
	}
	r.Blank()
	r.Line(r.Muted("  These are not registered. Containing a schedule is not the same as"))
	r.Line(r.Muted("  running one, a file can hold perfectly good cron syntax and be"))
	r.Line(r.Muted("  dormant, commented out, or meant for a different machine."))
	r.Line(r.Muted("  Register anything real with 'goguma add'."))
	r.Blank()
}

func shortenHomePath(p, home string) string {
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// printBlindSpots is shown when nothing was found, listing where goguma
// cannot see. Many tools run their own scheduler entirely inside their own
// config, and those jobs never appear in crontab or launchd.
func printBlindSpots(r *render.Renderer, cov []scan.Coverage) {
	var missing []string
	for _, c := range cov {
		if !c.Available {
			missing = append(missing, c.Source)
		}
	}
	r.Section("  if you expected to see something here")
	if len(missing) > 0 {
		r.Printf("    %s\n", r.Muted("nothing was found in: "+strings.Join(missing, ", ")))
	}
	r.Line(r.Muted("    goguma reads crontab, launchd/systemd, and the app schedulers it"))
	r.Line(r.Muted("    has been told about. A tool that runs jobs from inside its own"))
	r.Line(r.Muted("    config is invisible until you point goguma at its schedule file:"))
	r.Printf("    %s\n", r.Accent("goguma scheduler add <name> <its schedule file>"))
	r.Blank()
	r.Line(r.Muted("    Or register one job by hand, regardless of what runs it:"))
	r.Printf("    %s\n", r.Accent("goguma add --name <name> --cron '<schedule>'"))
	r.Blank()
}

func totalAsleep(h *schedule.SleepHistory) time.Duration {
	var total time.Duration
	for _, iv := range h.Intervals {
		if !iv.Wake.IsZero() {
			total += iv.Wake.Sub(iv.Sleep)
		}
	}
	return total
}

func printCandidate(r *render.Renderer, n int, c scan.Candidate, existing map[string]bool) {
	sym := r.Sym()

	head := fmt.Sprintf("[%d] %s", n, r.Bold(c.Name))
	if existing[model.Slug(c.Name)] {
		head += r.Muted("  (already registered)")
	}
	r.Line(head)

	schedText := c.Schedule
	if c.Parsed != nil {
		schedText = c.Parsed.Display + r.Muted("  ("+c.Schedule+")")
	}
	pairs := [][2]string{
		{"source", string(c.Source) + r.Muted("  "+c.File)},
		{"schedule", schedText},
		{"command", render.Truncate(c.Command, max(r.Width()-16, 30))},
	}
	if !c.NextFire.IsZero() {
		pairs = append(pairs, [2]string{"next run",
			c.NextFire.Format("Mon 2 Jan 15:04") + r.Muted("  "+model.HumanUntil(c.NextFire, time.Now()))})
	}
	// Directly observed lateness is the strongest evidence available, so it
	// leads and is stated as fact rather than as an estimate.
	if c.HasLateness && c.Lateness >= scan.LatenessThreshold && !c.LastRun.IsZero() {
		pairs = append(pairs, [2]string{"last run",
			r.Danger(fmt.Sprintf("%s late", model.HumanDuration(c.Lateness))) +
				r.Muted(fmt.Sprintf("  (ran %s, scheduled for %s)",
					c.LastRun.Format("Mon 15:04"),
					c.LastRun.Add(-c.Lateness).Format("15:04")))})
	} else {
		pairs = append(pairs, [2]string{"missed", riskPhrase(r, c)})
	}
	r.KeyValue(pairs)

	// launchd calendar jobs are deferred to the next wake rather than lost,
	// per launchd.plist(5). Saying so keeps the pitch honest: for these,
	// goguma buys punctuality, not survival.
	if c.SelfHealing && c.Source == scan.SourceLaunchd {
		r.Printf("      %s\n", r.Muted(
			"launchd runs this on the next wake if it is missed, so it is late rather than skipped."))
	}
	if c.HasLateness && c.Lateness >= scan.LatenessThreshold && c.TimeSensitive {
		r.Printf("      %s\n", r.Muted(
			"this job does eventually run, but not at the time it is scheduled for."))
	}
	_ = sym
}

func riskPhrase(r *render.Renderer, c scan.Candidate) string {
	summary := scan.MissedSummary(c.Risk)
	switch c.Risk.Level() {
	case schedule.RiskHigh:
		return r.Danger(summary)
	case schedule.RiskSome:
		return r.Warn(summary)
	case schedule.RiskNone:
		return r.Good(summary)
	default:
		return r.Muted(summary)
	}
}

func printFiltered(r *render.Renderer, filtered []scan.Candidate) {
	if len(filtered) == 0 {
		return
	}
	r.Section(fmt.Sprintf("  excluded (%d)", len(filtered)))

	// Group by reason so the output is a short summary rather than hundreds
	// of near-identical lines.
	byReason := map[scan.Reason][]string{}
	order := []scan.Reason{}
	for _, c := range filtered {
		if _, seen := byReason[c.Reason]; !seen {
			order = append(order, c.Reason)
		}
		byReason[c.Reason] = append(byReason[c.Reason], c.Name)
	}
	for _, reason := range order {
		names := byReason[reason]
		r.Printf("    %s %s\n", r.Muted(fmt.Sprintf("%3d", len(names))), string(reason))
		const showMax = 4
		shown := names
		if len(shown) > showMax {
			shown = shown[:showMax]
		}
		line := strings.Join(shown, ", ")
		if len(names) > showMax {
			line += fmt.Sprintf(", and %d more", len(names)-showMax)
		}
		r.Printf("        %s\n", r.Muted(render.Truncate(line, max(r.Width()-10, 40))))
	}
	r.Blank()
}

type importAction int

const (
	actionSkip importAction = iota
	actionWrap
	actionPattern
	actionWakeOnly
)

// chooseAction asks how to detect this job.
//
// The wrapper is presented first and marked recommended, because it is the
// only mechanism that yields an exact duration and a real exit code. Pattern
// matching stays available for entries the user does not want to modify.
func chooseAction(ctx *Context, c scan.Candidate, others []scan.Candidate, registered []string, assumeYes bool) importAction {
	r := ctx.Out

	// A job the user cannot instrument gets a different, honest set of
	// options. Offering to wrap a command line that lives inside another
	// application's config would be advice they cannot act on.
	if !c.Wrappable {
		return chooseUnwrappableAction(ctx, c, assumeYes)
	}

	wrapped := wrapCommand(ctx, c)
	pattern := detect.SuggestPattern(c.Command)
	// Offered only when it identifies this job and nothing else on the machine.
	//
	// A pattern that also matches a sibling is worse than none: the daemon
	// releases a hold when *a* match exits, so the fastest sibling closes
	// everyone's window and every duration recorded is wrong. Presenting it as
	// an option invited exactly that.
	patternUsable := detect.Distinctive(pattern, append(siblingCommands(c, others), registered...))

	if assumeYes {
		r.Printf("      %s registering with the wrapper\n", r.Good(r.Sym().OK))
		return actionWrap
	}

	r.Blank()
	r.Printf("      %s %s\n", r.Accent("[w] wrap"), r.Muted("exact detection · recommended"))
	switch c.Source {
	case scan.SourceCrontab:
		r.Printf("          %s\n", r.Muted("goguma changes this crontab line to, keeping a backup:"))
	case scan.SourceLaunchd:
		if needsRootToWrap(c.File) {
			r.Printf("          %s\n", r.Muted(
				"goguma edits the plist and reloads the job, keeping a backup."))
			r.Printf("          %s\n", r.Warn("this one is system-wide, so it asks for your password:"))
		} else {
			r.Printf("          %s\n", r.Muted("goguma edits the plist and reloads the job, keeping a backup:"))
		}
	default:
		r.Printf("          %s\n", r.Muted("change the "+string(c.Source)+" line to:"))
	}
	r.Printf("          %s\n", r.Accent(wrapped))
	if patternUsable {
		r.Printf("      %s %s\n", r.Accent("[p] pattern"), r.Muted("no changes to your setup"))
		r.Printf("          %s\n", r.Muted("watch for: "+pattern))
	} else {
		r.Printf("      %s %s\n", r.Muted("[p] pattern"), r.Muted("unavailable"))
		r.Printf("          %s\n", r.Muted(patternRefusal(pattern)))
	}
	r.Printf("      %s\n", r.Accent("[s] skip"))
	r.Blank()

	for {
		fmt.Fprintf(os.Stdout, "      choice [w/p/s]: ")
		line, err := readLine()
		if err != nil {
			return actionSkip
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "w", "wrap", "":
			return actionWrap
		case "p", "pattern":
			if !patternUsable {
				r.Printf("      %s\n", r.Warn(patternRefusal(pattern)))
				continue
			}
			return actionPattern
		case "s", "skip", "n", "no":
			return actionSkip
		}
		r.Printf("      %s\n", r.Muted("please answer w, p, or s"))
	}
}

// chooseUnwrappableAction handles jobs run by an application's own scheduler.
//
// There is no command line to wrap, and usually no distinct process to match
// either, the work happens inside the application's own process. So the
// realistic choice is to wake the machine for a bounded window and accept that
// the job itself cannot be observed.
func chooseUnwrappableAction(ctx *Context, c scan.Candidate, assumeYes bool) importAction {
	r := ctx.Out

	if assumeYes {
		r.Printf("      %s registering as wake-only\n", r.Good(r.Sym().OK))
		return actionWakeOnly
	}

	r.Blank()
	r.Printf("      %s\n", r.Muted(
		string(c.Source)+" runs this itself, so there is no command line to wrap."))
	r.Printf("      %s %s\n", r.Accent("[k] wake only"), r.Muted("recommended"))
	r.Printf("          %s\n", r.Muted(
		"wake the machine for it and hold briefly; the job is not watched,"))
	r.Printf("          %s\n", r.Muted(
		"so the hold is a fixed window rather than its real runtime"))
	r.Printf("      %s %s\n", r.Accent("[p] pattern"), r.Muted("try to watch for a process anyway"))
	r.Printf("      %s\n", r.Accent("[s] skip"))
	r.Blank()

	for {
		fmt.Fprintf(os.Stdout, "      choice [k/p/s]: ")
		line, err := readLine()
		if err != nil {
			return actionSkip
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "k", "wake", "":
			return actionWakeOnly
		case "p", "pattern":
			return actionPattern
		case "s", "skip", "n", "no":
			return actionSkip
		}
		r.Printf("      %s\n", r.Muted("please answer k, p, or s"))
	}
}

// wrapCommand builds the wrapped command line for a candidate.
//
// The wrapper is named by absolute path, never as `goguma-mark`.
//
// cron runs jobs with PATH=/usr/bin:/bin and launchd with
// /usr/bin:/bin:/usr/sbin:/sbin. The wrapper installs to ~/.local/bin, which is
// on neither. A bare name therefore produces a line that runs fine in the
// user's shell, where PATH includes their bin dir, and fails with "command not
// found" under the scheduler, which does not. That does not degrade timing: it
// stops the job running at all. Wrapping a working job must never be able to
// break it, so the path is resolved here and checked by the caller.
func wrapCommand(ctx *Context, c scan.Candidate) string {
	return fmt.Sprintf("%s %s -- %s", shellQuote(markBinary(ctx)), model.Slug(c.Name), c.Command)
}

// shellQuote makes a path safe as one word in a crontab line.
//
// macOS home directories are routinely "/Users/Jun Nam", so the wrapper's
// absolute path routinely contains a space. A crontab command is handed to a
// shell, which splits on it: the line runs "/Users/Jun" and the job dies with
// "command not found". That is the same failure as using a bare name, and the
// same severity, since a wrapped job that cannot start is a job that stopped
// running.
//
// Quoted only here. launchd takes argv as literal array elements, so quoting
// the plist entry would make the quotes part of the filename.
func shellQuote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\$`&|;<>()*?[]#~!") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// markBinary is the absolute path to goguma-mark.
//
// The installed location first, then a sibling of the running binary, which is
// what a build tree or an unpacked release looks like.
func markBinary(ctx *Context) string {
	installed := filepath.Join(ctx.Layout.BinDir, "goguma-mark")
	if isExecutableFile(installed) {
		return installed
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		if sibling := filepath.Join(filepath.Dir(exe), "goguma-mark"); isExecutableFile(sibling) {
			return sibling
		}
	}
	// Nothing found. Returning the installed path rather than a bare name
	// keeps the failure legible: the line says exactly which file is missing.
	return installed
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func readLine() (string, error) {
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no input")
	}
	return sc.Text(), nil
}

func registerCandidate(ctx *Context, c scan.Candidate, action importAction) error {
	job := model.Job{
		Name:      c.Name,
		ID:        model.Slug(c.Name),
		Schedule:  c.Schedule,
		Command:   c.Command,
		Source:    string(c.Source),
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	switch action {
	case actionWrap:
		job.Detection = model.DetectMark
	case actionPattern:
		job.Detection = model.DetectPattern
		job.Match = detect.SuggestPattern(c.Command)
	case actionWakeOnly:
		job.Detection = model.DetectNone
	}
	if err := job.Validate(); err != nil {
		return err
	}

	var saved model.Job
	if err := callDaemon(ctx, ipc.OpJobsAdd, job, &saved); err != nil {
		return err
	}

	r := ctx.Out
	r.Printf("      %s registered %s\n", r.Good(r.Sym().OK), r.Bold(saved.Name))
	if action == actionWakeOnly {
		r.Printf("      %s\n", r.Muted(
			"the machine will wake for it; adjust the window with --max-runtime"))
	}
	if action == actionWrap {
		wrapped := wrapCommand(ctx, c)

		// Applied, not recited.
		//
		// This used to print the line and tell the user to run `crontab -e`
		// and paste it, on the grounds that editing a file they own is a
		// bigger promise than the command should make. But the job is already
		// registered as exactly-timed at this point, so skipping the paste
		// leaves a job that records "never detected" on every run and warns
		// about a setup the user was never told was half-finished. A setup
		// command that ends in homework, and silently punishes forgetting it,
		// is worse than one that does the work carefully.
		if c.Source == scan.SourceCrontab {
			if err := applyCrontabWrap(ctx, c.Line, c.Command, wrapped); err != nil {
				r.Printf("      %s %s\n", r.Warn(r.Sym().Warn), r.Muted(err.Error()))
				r.Printf("      %s\n", r.Muted("update the line yourself with 'crontab -e':"))
				r.Printf("      %s\n", r.Accent(wrapped))
				return nil
			}
			r.Printf("      %s %s\n", r.Good(r.Sym().OK),
				r.Muted("crontab updated (a copy of the old one is in "+
					shortHome(ctx.Layout.CrontabBackup())+")"))
			return nil
		}

		if c.Source == scan.SourceLaunchd {
			err := applyLaunchdWrap(ctx, c.File, c.Label, c.Command, markBinary(ctx), model.Slug(c.Name))
			if err != nil {
				r.Printf("      %s %s\n", r.Warn(r.Sym().Warn), r.Muted(err.Error()))
				r.Printf("      %s\n", r.Muted("put these two entries at the front of ProgramArguments"))
				r.Printf("      %s\n", r.Muted("in "+c.File+", then reload the job:"))
				r.Printf("      %s\n", r.Accent("<string>"+markBinary(ctx)+"</string>"))
				r.Printf("      %s\n", r.Accent("<string>"+model.Slug(c.Name)+"</string>"))
				return nil
			}
			r.Printf("      %s %s\n", r.Good(r.Sym().OK),
				r.Muted("plist updated and the job reloaded (a copy of the old one is in "+
					shortHome(filepath.Join(ctx.Layout.StateDir, "launchd-backup"))+")"))
			return nil
		}
	}
	return nil
}

// shortHome trims the home directory off a path for display.
func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// registeredCommands is every already-registered job's command line, so a
// suggested pattern is checked against the whole machine and not just this
// scan. Best-effort: with the daemon down it returns nothing, which degrades
// to the old same-scan-only check.
func registeredCommands(ctx *Context) []string {
	var resp ipc.JobsListResp
	if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
		return nil
	}
	var out []string
	for _, v := range resp.Jobs {
		if v.Job.Command != "" {
			out = append(out, v.Job.Command)
		}
	}
	return out
}

func existingJobIDs(ctx *Context) map[string]bool {
	out := map[string]bool{}
	var resp ipc.JobsListResp
	if err := callDaemon(ctx, ipc.OpJobsList, nil, &resp); err != nil {
		return out
	}
	for _, v := range resp.Jobs {
		out[v.Job.ID] = true
	}
	return out
}

// siblingCommands is every other command from this import run a pattern
// could be confused with; the caller appends the already-registered jobs'
// commands. Checking only the current scan let two sibling jobs registered
// one at a time accept the same colliding pattern, and a shared pattern
// releases everyone's hold when the fastest sibling exits.
func siblingCommands(self scan.Candidate, others []scan.Candidate) []string {
	out := make([]string, 0, len(others)+1)
	out = append(out, self.Command)
	for _, o := range others {
		if o.Command != self.Command {
			out = append(out, o.Command)
		}
	}
	return out
}

// patternRefusal explains why watching is not on offer, in terms of what it
// would do rather than what it is.
func patternRefusal(pattern string) string {
	if pattern == "" {
		return "no distinctive text in this command to watch for"
	}
	return "\"" + pattern + "\" would also match another job, " +
		"so it cannot tell them apart"
}
