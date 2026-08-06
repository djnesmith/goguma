package paths

import (
	"os"
	"path/filepath"
)

// HelperSocket is the daemon-to-helper channel. /run is tmpfs on every
// systemd distro, so a stale socket cannot survive a reboot.
const HelperSocket = "/run/wakeguard-helper.sock"

// HelperBinary must live on a root-owned path; see the darwin note.
const HelperBinary = "/usr/local/libexec/wakeguard-helper"

const (
	DaemonUnit = "wakeguard-daemon.service"
	HelperUnit = "wakeguard-helper.service"
)

func platformLayout(home string) Layout {
	// XDG_STATE_HOME is the correct base for data that persists but is not
	// user-portable config — history and logs are exactly that.
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	stateDir := filepath.Join(stateHome, "wakeguard")
	return Layout{
		StateDir:      stateDir,
		LogDir:        filepath.Join(stateDir, "logs"),
		BinDir:        filepath.Join(home, ".local", "bin"),
		UnitDir:       filepath.Join(configHome, "systemd", "user"),
		SystemUnitDir: "/etc/systemd/system",
	}
}

// DaemonUnitFile is the user-level systemd unit path.
func (l Layout) DaemonUnitFile() string {
	return filepath.Join(l.UnitDir, DaemonUnit)
}

// HelperUnitFile is the system-level systemd unit path.
func (l Layout) HelperUnitFile() string {
	return filepath.Join(l.SystemUnitDir, HelperUnit)
}
