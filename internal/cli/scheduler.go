package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/junnam586/goguma/internal/scan"
)

var cmdScheduler = &Command{
	Name:    "scheduler",
	Summary: "teach goguma about an app that runs its own schedules",
	Usage: `goguma scheduler <command>

Some apps run jobs from inside themselves rather than through cron or launchd.
Those jobs appear in no system registry, so goguma cannot find them until it is
told where they live. This is how you tell it, and it only has to be done once
per app.

  list                  show the app schedulers goguma knows about
  add <name> <file>     work out how to read <file> and remember it
  remove <name>         forget one

Example:

  goguma scheduler add cowork ~/Library/Application\ Support/Cowork/tasks.json

'add' reads the file, works out which field is the name and which is the
schedule, and prints what it decided so you can check it. The result is a small
JSON file you can edit by hand afterwards.`,
	Run: runScheduler,
}

func runScheduler(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("goguma scheduler needs a command: list, add, or remove")
	}
	switch args[0] {
	case "list":
		return schedulerList(ctx)
	case "add":
		return schedulerAdd(ctx, args[1:])
	case "remove", "rm":
		return schedulerRemove(ctx, args[1:])
	}
	return fmt.Errorf("unknown command %q; try list, add, or remove", args[0])
}

func schedulerList(ctx *Context) error {
	r := ctx.Out
	dir := ctx.Layout.SchedulersDir()
	loaded, problems := scan.LoadManifests(dir)

	if len(loaded) == 0 && len(problems) == 0 {
		r.Line(r.Muted("No app schedulers have been added."))
		r.Blank()
		r.Line(r.Muted("goguma reads crontab and launchd by itself. An app that runs jobs"))
		r.Line(r.Muted("from inside its own config needs to be pointed at once:"))
		r.Printf("  %s\n", r.Accent("goguma scheduler add <name> <its schedule file>"))
		return nil
	}

	for _, p := range scan.Providers() {
		for _, name := range loaded {
			if p.Name() != name {
				continue
			}
			mark, note := r.Good(r.Sym().OK), ""
			if !p.Available() {
				mark, note = r.Warn(r.Sym().Warn), "  (file not found right now)"
			}
			r.Printf("%s %-16s %s%s\n", mark, p.Name(), r.Muted(p.Where()), r.Muted(note))
		}
	}
	for _, err := range problems {
		r.Problem(err.Error(), "")
	}
	return nil
}

func schedulerAdd(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("scheduler add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: goguma scheduler add <name> <schedule file>")
	}
	name, path := rest[0], rest[1]
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	r := ctx.Out
	m, notes, err := scan.InferManifest(abs, name)
	if err != nil {
		return err
	}

	// Printed before it is saved, because every line of this is a guess the
	// user is better placed to check than goguma is.
	r.Printf("Read %s\n", r.Bold(shortHome(abs)))
	r.Blank()
	r.Line(r.Muted("  goguma will read these fields:"))
	for _, n := range notes {
		r.Printf("    %s\n", r.Muted(n))
	}
	r.Blank()

	if verr := m.Validate(); verr != nil {
		r.Problem(verr.Error(), "")
		r.Line(r.Muted("  Nothing was saved. The file may not hold schedules, or the fields"))
		r.Line(r.Muted("  may be named in a way goguma could not recognise."))
		return nil
	}

	// Prove it before saving it: a manifest that parses but finds nothing is
	// the failure mode worth catching here rather than a week later.
	found, derr := scan.PreviewManifest(m)
	if derr != nil {
		return derr
	}
	if len(found) == 0 {
		r.Problem("no jobs could be read out of that file with those fields", "")
		return nil
	}
	r.Printf("  %s found %s\n", r.Good(r.Sym().OK), pluralJobsCLI(len(found)))
	for i, e := range found {
		if i == 3 {
			r.Printf("    %s\n", r.Muted(fmt.Sprintf("… and %d more", len(found)-3)))
			break
		}
		r.Printf("    %s  %s\n", e.Name, r.Muted(e.Schedule))
	}
	r.Blank()

	if err := os.MkdirAll(ctx.Layout.SchedulersDir(), 0o700); err != nil {
		return err
	}
	dest := filepath.Join(ctx.Layout.SchedulersDir(), name+".json")
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, append(b, '\n'), 0o600); err != nil {
		return err
	}

	r.Printf("%s saved to %s\n", r.Good(r.Sym().OK), r.Muted(shortHome(dest)))
	r.Line(r.Muted("  Edit that file if any of the fields above are wrong."))
	r.Blank()
	r.Printf("  %s to see what goguma will wake for.\n", r.Accent("goguma import"))
	return nil
}

func schedulerRemove(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: goguma scheduler remove <name>")
	}
	dest := filepath.Join(ctx.Layout.SchedulersDir(), args[0]+".json")
	if err := os.Remove(dest); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("goguma does not know about an app scheduler called %q", args[0])
		}
		return err
	}
	ctx.Out.Printf("%s forgot %s\n", ctx.Out.Good(ctx.Out.Sym().OK), args[0])
	ctx.Out.Line(ctx.Out.Muted("  Jobs already registered from it stay registered; " +
		"remove them with 'goguma remove <name>'."))
	return nil
}

func pluralJobsCLI(n int) string {
	if n == 1 {
		return "1 job"
	}
	return fmt.Sprintf("%d jobs", n)
}
