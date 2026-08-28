package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/junnam586/goguma/internal/agenthooks"
	"github.com/junnam586/goguma/internal/ipc"
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
'goguma hooks remove' takes out what it added and nothing else, and the
background service leaves them out until 'goguma hooks install' puts them back.

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

// hooksStatus reports what each agent is configured to do, and — separately —
// whether goguma will act on what they report.
//
// The two are not the same thing and must not be printed as if they were. A
// hook being installed means the agent will SAY when it is working; whether
// that opens a hold is `agent_hooks`. With the setting off this used to read
// "holding sleep off while it works" beside every agent, which was flatly
// untrue: they were reporting and nothing was being held.
func hooksStatus(ctx *Context, binDir string) error {
	r := ctx.Out
	sym := r.Sym()

	// Best effort. A daemon that is not running cannot be asked, and the
	// installed/not-installed half of this is still worth printing; assume the
	// default rather than refusing to say anything.
	holding, known := true, false
	var cfgResp ipc.ConfigResp
	if err := ipc.Do(ctx.Socket, ipc.OpConfigGet, nil, &cfgResp); err == nil {
		holding, known = cfgResp.Config.AgentHooks, true
	}
	installedNote := "reporting; holds sleep off while it works"
	summary := "all set. The machine stays awake while an agent works, and sleeps when it stops."
	switch {
	case !known:
		// Say what is installed, and do not guess at what it will do. The
		// setting lives in the daemon, and the daemon is not answering.
		installedNote = "reporting (the background service isn't running, so " +
			"whether it holds is unknown)"
		summary = "start the background service to see whether these hold sleep off."
	case !holding:
		installedNote = "reporting; not held (agent_hooks is off)"
		summary = "agents report and are shown in the menu bar, but nothing is held: " +
			"the Mac sleeps normally unless you keep it awake yourself."
	}

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
			r.Printf("  %s %-14s %s\n", r.Good(sym.OK), h.ID, r.Muted(installedNote))
		default:
			anyPresent, anyMissing = true, true
			r.Printf("  %s %-14s %s\n", r.Muted(sym.Idle), h.ID, r.Muted("found, not set up yet"))
		}
	}

	r.Blank()
	if _, err := os.Stat(ctx.Layout.HooksOptOut()); err == nil {
		r.Printf("  %s\n", r.Muted(
			"taken out by 'goguma hooks remove'; the background service will leave them out. "+
				"Put them back with: "+r.Accent("goguma hooks install")))
		r.Blank()
	}
	switch {
	case !anyPresent:
		r.Printf("  %s\n", r.Muted("no coding agents found. Wrap one directly instead: goguma run -- <command>"))
	case anyMissing:
		r.Printf("  %s\n", r.Muted("set them up with: "+r.Accent("goguma hooks install")))
	default:
		r.Printf("  %s\n", r.Muted(summary))
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

	// Record the removal, or clear the record on install.
	//
	// The daemon reinstalls the hooks on every start — they are the signal, and
	// `agent_hooks` only decides whether that signal opens a hold — so without
	// this marker `hooks remove` would be quietly undone at the next login.
	optOut := ctx.Layout.HooksOptOut()
	if remove {
		if err := os.WriteFile(optOut, []byte("removed by goguma hooks remove\n"), 0o600); err != nil {
			r.Printf("  %s\n", r.Warn(
				"couldn't record the removal, so the background service may put these back: "+err.Error()))
		}
	} else if err := os.Remove(optOut); err != nil && !os.IsNotExist(err) {
		r.Printf("  %s\n", r.Warn("couldn't clear the opt-out marker: "+err.Error()))
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
