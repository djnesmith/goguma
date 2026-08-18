// Package cli implements the goguma command line.
//
// Dispatch is hand-rolled rather than built on a framework: the command set
// is small and stable, and a dependency-free CLI keeps the shipped binary a
// single static file with no transitive supply chain, which is the stated
// distribution goal.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/junnam586/goguma/internal/paths"
	"github.com/junnam586/goguma/internal/render"
	"github.com/junnam586/goguma/internal/scan"
)

// Version is set from main at build time.
var Version = "dev"

// Command is one subcommand.
type Command struct {
	Name    string
	Summary string
	// Usage is the full help text, shown by `goguma help <name>`.
	Usage string
	Run   func(ctx *Context, args []string) error
	// Hidden keeps a command out of the top-level listing.
	Hidden bool
}

// Context carries what every command needs.
type Context struct {
	Out    *render.Renderer
	Err    *render.Renderer
	Layout paths.Layout
	Socket string
}

func newContext() *Context {
	layout := paths.MustResolve()
	// The same app-scheduler manifests the daemon reads, so `import` and
	// `list` never disagree with it about which sources exist.
	_, _ = scan.LoadManifests(layout.SchedulersDir())
	return &Context{
		Out:    render.New(os.Stdout),
		Err:    render.New(os.Stderr),
		Layout: layout,
		Socket: layout.DaemonSocket(),
	}
}

var commands = map[string]*Command{}

func register(c *Command) { commands[c.Name] = c }

func init() {
	register(cmdStatus)
	register(cmdList)
	register(cmdAdd)
	register(cmdRemove)
	register(cmdEdit)
	register(cmdGroup)
	register(cmdEnable)
	register(cmdDisable)
	register(cmdHistory)
	register(cmdImport)
	register(cmdScheduler)
	register(cmdSync)
	register(cmdTestMatch)
	register(cmdConfig)
	register(cmdRun)
	register(cmdAgentHook)
	register(cmdHooks)
	register(cmdAwake)
	register(cmdSkipNext)
	register(cmdSleepNow)
	register(cmdPause)
	register(cmdResume)
	register(cmdInstall)
	register(cmdUninstall)
	register(cmdDoctor)
	register(cmdVersion)
}

// Main is the entry point.
func Main(args []string) int {
	ctx := newContext()

	if len(args) == 0 {
		printHelp(ctx, nil)
		return 0
	}

	name := args[0]
	rest := args[1:]

	switch name {
	case "-h", "--help", "help":
		printHelp(ctx, rest)
		return 0
	case "-v", "--version":
		name = "version"
	}

	cmd, ok := commands[name]
	if !ok {
		render.Errorf("unknown command %q", name)
		if s := suggest(name); s != "" {
			fmt.Fprintf(os.Stderr, "  did you mean %s?\n", ctx.Err.Accent("goguma "+s))
		}
		fmt.Fprintf(os.Stderr, "  run %s to see what is available\n", ctx.Err.Accent("goguma help"))
		return 2
	}

	// Answered here rather than inside each command, because the commands that
	// take no flags never look at their arguments at all: `goguma pause --help`
	// asked what pause does and got a paused daemon and a success message. A
	// request for help must never be a way to trigger the thing being asked
	// about, so it is intercepted before Run is reached.
	if wantsHelp(rest) {
		ctx.Out.Line(cmd.Usage)
		return 0
	}

	if err := cmd.Run(ctx, rest); err != nil {
		// A command whose own flag set saw -h prints its usage and hands back
		// this sentinel. It is a fulfilled request, not a failure, so it must
		// not be reported as "goguma: flag: help requested" with a exit code of 1.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		// `goguma run` wraps somebody else's command, and its exit status is
		// that command's. Flattening it to 1 would break anything checking for
		// a particular code, and would report a failure goguma did not have.
		if code, ok := ExitCode(err); ok {
			return code
		}
		render.Errorf("%v", err)
		return 1
	}
	return 0
}

// wantsHelp reports whether args ask what a command does rather than ask it to
// run. Scanning stops at "--" so that a literal "--help" can still be passed as
// a value, which matters for the commands that take a pattern or a job name.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}

// suggest offers the closest command name for a typo.
func suggest(input string) string {
	best, bestDist := "", 1<<30
	for name := range commands {
		d := editDistance(strings.ToLower(input), name)
		if d < bestDist {
			best, bestDist = name, d
		}
	}
	// Only suggest when the guess is genuinely close; an unrelated
	// suggestion is more confusing than none.
	if bestDist <= 3 && bestDist < len(input) {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		copy(prev, cur)
	}
	return prev[len(b)]
}

func printHelp(ctx *Context, args []string) {
	r := ctx.Out

	if len(args) > 0 {
		if cmd, ok := commands[args[0]]; ok {
			r.Line(cmd.Usage)
			return
		}
		render.Errorf("no help for %q", args[0])
		return
	}

	r.Line(r.Bold("goguma") + ", wake your machine for scheduled jobs, then let it sleep")
	r.Blank()
	r.Line(r.Muted("usage:") + " goguma <command> [options]")
	r.Blank()

	for _, g := range helpGroups() {
		r.Section("  " + g.title)
		for _, n := range g.names {
			c, ok := commands[n]
			if !ok || c.Hidden {
				continue
			}
			r.Printf("    %-14s %s\n", r.Accent(c.Name), r.Muted(c.Summary))
		}
		r.Blank()
	}
	r.Line(r.Muted("  run 'goguma help <command>' for details on any of these"))
}

// helpGroup is one titled block of `goguma help`.
type helpGroup struct {
	title string
	names []string
}

// helpGroups is the top-level listing, grouped rather than alphabetical
// because the order a new user needs these in is not the order they sort in.
//
// Every non-hidden command has to appear in exactly one group. A command
// missing from here is not listed at all, which is how `goguma scheduler`
// shipped invisible: it was registered, had a Summary written for this very
// listing, and had no group to be listed in, so the only way to find it was to
// already know the word. TestEveryCommandIsListed fails when that happens
// again, which is why this is a function rather than a literal inline above.
func helpGroups() []helpGroup {
	return []helpGroup{
		{"getting started", []string{"install", "import", "add", "sync", "scheduler"}},
		{"everyday", []string{"status", "list", "history"}},
		{"managing jobs", []string{"edit", "group", "remove", "enable", "disable", "test-match"}},
		{"control", []string{"run", "hooks", "awake", "skip-next", "sleep-now", "pause", "resume"}},
		{"maintenance", []string{"config", "doctor", "uninstall", "version"}},
	}
}

// commandNames is used by help and completion.
func commandNames() []string {
	out := make([]string, 0, len(commands))
	for n, c := range commands {
		if !c.Hidden {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

var cmdVersion = &Command{
	Name:    "version",
	Summary: "print the version",
	Usage:   "goguma version\n\nPrints the CLI version, and the background service and helper versions when reachable.",
	Run: func(ctx *Context, args []string) error {
		r := ctx.Out
		r.Printf("goguma %s\n", Version)

		st, err := fetchStatus(ctx)
		if err != nil {
			r.Line(r.Muted("service   not running"))
			return nil
		}
		r.Printf("%s %s\n", r.Muted("service  "), st.DaemonVersion)
		if st.HelperConnected {
			r.Printf("%s %s\n", r.Muted("helper   "), st.HelperVersion)
		} else {
			r.Line(r.Muted("helper    not connected"))
		}
		return nil
	},
}
