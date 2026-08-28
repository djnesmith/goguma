package daemon

import (
	"os"
	"path/filepath"

	"github.com/junnam586/goguma/internal/agenthooks"
	"github.com/junnam586/goguma/internal/config"
)

// reconcileAgentHooks makes sure every coding agent on this machine is
// configured to report when it is working.
//
// Unconditionally, in both states of `agent_hooks`. The hooks are the signal;
// the setting decides only whether that signal opens a hold, and that decision
// is made in StartRun. See Config.AgentHooks for why the two were separated.
//
// The one thing that stops it is `goguma hooks remove`, which records the
// removal so this does not undo it at the next start.
func (d *Daemon) reconcileAgentHooks(cfg config.Config) {
	binDir := gogumaBinDir()
	if binDir == "" {
		return
	}
	// Somebody ran `goguma hooks remove`. Reinstalling here would undo it at
	// the next login and make that command a no-op with a success message.
	if _, err := os.Stat(d.store.Layout().HooksOptOut()); err == nil {
		return
	}
	for _, h := range agenthooks.Harnesses {
		if !h.Present() {
			continue
		}
		st := agenthooks.Inspect(h, binDir)
		if st.Err != nil {
			d.log.Debug("couldn't read an agent's config", "agent", h.ID, "err", st.Err)
			continue
		}
		// Installed in both states, and this is the whole point.
		//
		// `agent_hooks off` means "do not hold sleep off for agents", not "stop
		// listening to them". Those used to be the same thing: switching off
		// took the hook lines back out, so agents stopped reporting, so the
		// passive display had no data and never appeared — goguma could not say
		// "an agent is working and nothing is holding the Mac awake" because it
		// had just deafened itself.
		//
		// The hook is what carries the signal; whether that signal opens a hold
		// is decided in StartRun, against the live setting. Leaving the hooks in
		// place costs an agent one local socket write per prompt and per tool
		// call, and buys the only view anyone has of what is running.
		if st.Installed && !st.Stale {
			continue
		}
		doc, err := agenthooks.ReadConfig(h.Path())
		if err != nil {
			d.log.Debug("couldn't read an agent's config", "agent", h.ID, "err", err)
			continue
		}
		if _, err := agenthooks.WriteConfig(h.Path(),
			agenthooks.Apply(doc, h, binDir, false)); err != nil {
			d.log.Warn("couldn't update an agent's config", "agent", h.ID, "err", err)
			continue
		}
		if cfg.AgentHooks {
			d.log.Info("agent will report, and will hold sleep off", "agent", h.ID)
		} else {
			d.log.Info("agent will report; holding is off, so it will only be shown",
				"agent", h.ID)
		}
	}
}

// gogumaBinDir is the directory holding goguma's own binaries.
//
// Taken from this running daemon, because `goguma` is installed beside it and
// the command written into an agent's config has to be the one that exists. A
// hook runs with whatever environment its harness provides, which frequently
// has no ~/.local/bin on PATH, and a bare `goguma` there fails silently.
func gogumaBinDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}
