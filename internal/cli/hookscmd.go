package cli

import (
	"fmt"
	"github.com/junnam586/goguma/internal/agenthooks"
	"os"
	"path/filepath"
	"strings"
)

var cmdHooks = &Command{
	Name:    "hooks",
	Summary: "keep the machine awake while a coding agent works",
	Usage: `goguma hooks
goguma hooks install [<agenthooks.Harness>...]
goguma hooks remove [<agenthooks.Harness>...]

Sets up your coding agents to tell goguma when they are working, so the machine
stays awake until they finish and sleeps again once they do, including with the
lid shut.

With no arguments it reports what is installed on this machine and whether each
one is configured. 'install' configures every agent it finds; name one or more
to do just those.

  goguma hooks
  goguma hooks install
  goguma hooks install claude-code
  goguma hooks remove

Known agents: claude-code, codex, cursor.

This is needed because an agent cannot be recognised from outside. Its process
is there whether or not it is working, and processor use does not separate the
two, because an agent waiting on a model is a process waiting on a socket. The
agent has to say so, and each of these can.

Each session holds separately, so two agents at once do not release each other
and the machine stays awake until the last of them is done. Every hold is
leased: if an agent exits without saying so, or the machine loses power, it
lapses by itself rather than being left on. The safety cutouts still apply.

What it writes is one command per event in that agent's own configuration file,
alongside whatever is already there. Your existing hooks are kept, a copy of the
file is made first, and if the result does not parse the original goes back.
'goguma hooks remove' takes out what it added and nothing else.

Nothing here is recorded as a job run.`,
	Run: func(ctx *Context, args []string) error {
		_, positional := hoistFlags(args)

		action := "status"
		if len(positional) > 0 {
			switch positional[0] {
			case "install", "add", "enable":
				action, positional = "install", positional[1:]
			case "remove", "uninstall", "disable":
				action, positional = "remove", positional[1:]
			default:
				return fmt.Errorf("unknown subcommand %q; try 'goguma hooks', 'goguma hooks install', or 'goguma hooks remove'", positional[0])
			}
		}

		chosen, err := pickHarnesses(positional)
		if err != nil {
			return err
		}
		binDir := gogumaBinDir()

		if action == "status" {
			return hooksStatus(ctx, binDir)
		}
		return hooksApply(ctx, chosen, binDir, action == "remove")
	},
}

// gogumaBinDir is the directory holding the running goguma.
//
// Taken from the running binary rather than from a configured path, so the
// command written into a hook is the one that actually exists. A hook runs with
// whatever environment its agenthooks.Harness provides, which often has no ~/.local/bin on
// PATH, and a bare `goguma` there fails silently: no error, no hold, no clue.
func gogumaBinDir() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(agenthooks.ExpandHome("~/.local/bin"))
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func pickHarnesses(names []string) ([]agenthooks.Harness, error) {
	if len(names) == 0 {
		return agenthooks.Harnesses, nil
	}
	var out []agenthooks.Harness
	for _, n := range names {
		found := false
		for _, h := range agenthooks.Harnesses {
			if strings.EqualFold(h.ID, n) || strings.EqualFold(h.Name, n) {
				out, found = append(out, h), true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown agent %q; known ones are %s",
				n, strings.Join(agenthooks.SortedIDs(), ", "))
		}
	}
	return out, nil
}

func hooksStatus(ctx *Context, binDir string) error {
	r := ctx.Out
	sym := r.Sym()

	var anyPresent, anyMissing bool
	for _, h := range agenthooks.Harnesses {
		st := agenthooks.Inspect(h, binDir)
		switch {
		case !st.Found:
			r.Printf("  %s %-14s %s\n", r.Muted(sym.Idle), h.ID, r.Muted("not installed on this machine"))
		case st.Err != nil:
			anyPresent = true
			r.Printf("  %s %-14s %s\n", r.Warn(sym.Warn), h.ID, r.Warn(st.Err.Error()))
		case st.Stale:
			anyPresent, anyMissing = true, true
			r.Printf("  %s %-14s %s\n", r.Warn(sym.Warn), h.ID,
				r.Warn("configured, but for a goguma somewhere else; re-run install"))
		case st.Installed:
			anyPresent = true
			r.Printf("  %s %-14s %s\n", r.Good(sym.OK), h.ID, r.Muted("holding sleep off while it works"))
		default:
			anyPresent, anyMissing = true, true
			r.Printf("  %s %-14s %s\n", r.Muted(sym.Idle), h.ID, r.Muted("found, not set up yet"))
		}
	}

	r.Blank()
	switch {
	case !anyPresent:
		r.Printf("  %s\n", r.Muted("no coding agents found. Wrap one directly instead: goguma run -- <command>"))
	case anyMissing:
		r.Printf("  %s\n", r.Muted("set them up with: "+r.Accent("goguma hooks install")))
	default:
		r.Printf("  %s\n", r.Muted("all set. The machine stays awake while an agent works, and sleeps when it stops."))
	}
	return nil
}

func hooksApply(ctx *Context, chosen []agenthooks.Harness, binDir string, remove bool) error {
	r := ctx.Out
	sym := r.Sym()

	var touched, skipped int
	for _, h := range chosen {
		if !h.Present() {
			skipped++
			continue
		}
		path := h.Path()
		doc, err := agenthooks.ReadConfig(path)
		if err != nil {
			r.Printf("  %s %-14s %s\n", r.Warn(sym.Warn), h.ID, r.Warn(err.Error()))
			continue
		}
		doc = agenthooks.Apply(doc, h, binDir, remove)
		backup, err := agenthooks.WriteConfig(path, doc)
		if err != nil {
			r.Printf("  %s %-14s %s\n", r.Danger(sym.Warn), h.ID, r.Danger(err.Error()))
			continue
		}
		touched++
		verb := "now tells goguma when it is working"
		if remove {
			verb = "no longer tells goguma anything"
		}
		r.Printf("  %s %-14s %s\n", r.Good(sym.OK), h.ID, r.Muted(verb))
		r.Printf("    %s\n", r.Muted(shortenHome(path)))
		if backup != "" {
			r.Printf("    %s\n", r.Muted("previous version kept at "+shortenHome(backup)))
		}
	}

	r.Blank()
	switch {
	case touched == 0 && skipped > 0:
		r.Printf("  %s\n", r.Muted("none of those are installed here. Wrap a command instead: goguma run -- <command>"))
	case touched == 0:
		r.Printf("  %s\n", r.Muted("nothing to change"))
	case remove:
		r.Printf("  %s\n", r.Muted("restart the agent for this to take effect"))
	default:
		r.Printf("  %s\n", r.Muted("restart the agent for this to take effect, then check with: "+r.Accent("goguma status")))
	}
	return nil
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
