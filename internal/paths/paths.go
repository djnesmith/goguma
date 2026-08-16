// Package paths resolves every on-disk and socket location goguma uses.
// Centralised so that `uninstall` can enumerate exactly what to remove and
// leave nothing behind, a stated v1 requirement.
package paths

import (
	"os"
	"path/filepath"
)

// Layout is the full set of locations for the current platform and user.
type Layout struct {
	// StateDir holds jobs, config, history, logs, and the daemon socket.
	StateDir string
	// LogDir holds daemon/helper stdout+stderr captured by launchd/systemd.
	LogDir string
	// BinDir is where the CLI and wrapper are expected on PATH.
	BinDir string
	// UnitDir is where the user-level service definition is written
	// (~/Library/LaunchAgents or ~/.config/systemd/user).
	UnitDir string
	// SystemUnitDir is where the privileged helper's definition is written.
	SystemUnitDir string

	// DaemonService and HelperService name the two services to the init system:
	// launchd labels on darwin, unit names on linux.
	//
	// Fields rather than the package constants they default to, because
	// install.Uninstall stops services by name, and reading that name from a
	// constant made the function impossible to test. A test could pass any
	// Layout it liked and the teardown would still address the real services,
	// so exercising it would stop the developer's own daemon. Everything else
	// here is already per-Layout; these were the last two globals holding out.
	DaemonService string
	HelperService string

	// HelperBinary is where the privileged helper is installed.
	//
	// A field for the same reason as the service names, and more urgently:
	// uninstall removes this path with `sudo rm`. While it was a constant, a
	// test that called Uninstall attempted to delete the real helper off the
	// developer's machine, and was stopped only by sudo wanting a password.
	HelperBinary string
}

func (l Layout) JobsFile() string   { return filepath.Join(l.StateDir, "jobs.json") }
func (l Layout) ConfigFile() string { return filepath.Join(l.StateDir, "config.json") }
func (l Layout) StateFile() string  { return filepath.Join(l.StateDir, "state.json") }
func (l Layout) EventsFile() string { return filepath.Join(l.StateDir, "events.jsonl") }
func (l Layout) SleepLogFile() string {
	return filepath.Join(l.StateDir, "sleepwake.jsonl")
}
func (l Layout) HistoryDir() string { return filepath.Join(l.StateDir, "history") }
func (l Layout) HistoryFile(jobID string) string {
	return filepath.Join(l.HistoryDir(), jobID+".jsonl")
}

// DaemonSocket is the CLI/GUI to daemon channel. It lives inside StateDir so
// its permissions follow the user's own directory.
func (l Layout) DaemonSocket() string { return filepath.Join(l.StateDir, "daemon.sock") }

// CrontabBackup is written before `import` ever rewrites a crontab line.
func (l Layout) CrontabBackup() string { return filepath.Join(l.StateDir, "crontab.backup") }

// SchedulersDir holds manifests describing apps that run their own schedules.
//
// A scheduler living inside an application is invisible to crontab and
// launchd, and there are far more such applications than goguma will ever
// have hand-written readers for. A manifest here teaches it about one without
// a new release; see scan.Manifest.
func (l Layout) SchedulersDir() string { return filepath.Join(l.StateDir, "schedulers") }

// EnsureDirs creates the state tree. Mode 0o700 throughout: jobs.json holds
// the user's command lines and the socket accepts privileged-ish requests,
// so neither should be group- or world-readable.
func (l Layout) EnsureDirs() error {
	for _, d := range []string{l.StateDir, l.LogDir, l.HistoryDir(), l.SchedulersDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Resolve returns the layout for the current user, honouring
// GOGUMA_STATE_DIR so tests and parallel dev instances can be fully
// isolated from a real installation.
func Resolve() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	l := platformLayout(home)
	if override := os.Getenv("GOGUMA_STATE_DIR"); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return Layout{}, err
		}
		l.StateDir = abs
		l.LogDir = filepath.Join(abs, "logs")
	}
	return l, nil
}

// MustResolve is for command entry points, where a failure to find a home
// directory is unrecoverable anyway.
func MustResolve() Layout {
	l, err := Resolve()
	if err != nil {
		panic("goguma: cannot resolve state directory: " + err.Error())
	}
	return l
}
