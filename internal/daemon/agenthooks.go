package daemon

import (
	"os"
	"path/filepath"

	"github.com/junnam586/goguma/internal/agenthooks"
	"github.com/junnam586/goguma/internal/config"
)

// reconcileAgentHooks brings every coding agent on this machine into line with
// the agent_hooks setting.
//
// The daemon owns this rather than the installer, for two reasons.
//
// It has to keep being true. An agent installed a month after goguma would
// otherwise never be set up, and the user would have no reason to suspect that
// closing the lid on it behaves differently from the one that was there on
// install day. Reconciling on every start makes "which agents are configured"
// a question about what is on the machine now rather than what was there once.
//
// And the setting has to mean something from wherever it is changed. The menu
// bar app sets config over IPC and never runs the CLI, so a toggle there would
// otherwise do nothing at all until the next install. Turning it off takes the
// configuration back out, which is the only reading of "off" that is not a lie.
//
// Failures are logged and dropped. Somebody else's config file being
// unreadable, or read-only, is not a reason for goguma's daemon to fail to
// start, and the hooks command reports the same conditions properly when a
// human asks.
func (d *Daemon) reconcileAgentHooks(cfg config.Config) {
	binDir := gogumaBinDir()
	if binDir == "" {
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
		// Already in the state the setting asks for.
		if st.Installed && !st.Stale == cfg.AgentHooks {
			continue
		}
		if !cfg.AgentHooks && !st.Installed {
			continue
		}
		doc, err := agenthooks.ReadConfig(h.Path())
		if err != nil {
			d.log.Debug("couldn't read an agent's config", "agent", h.ID, "err", err)
			continue
		}
		if _, err := agenthooks.WriteConfig(h.Path(),
			agenthooks.Apply(doc, h, binDir, !cfg.AgentHooks)); err != nil {
			d.log.Warn("couldn't update an agent's config", "agent", h.ID, "err", err)
			continue
		}
		if cfg.AgentHooks {
			d.log.Info("agent will report when it is working", "agent", h.ID)
		} else {
			d.log.Info("agent will no longer report to goguma", "agent", h.ID)
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
