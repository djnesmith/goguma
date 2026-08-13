package paths

import "path/filepath"

// HelperSocket is the daemon-to-helper channel. It lives under /var/run
// because the helper runs as root before any user is logged in, and a
// user-owned path would be both unavailable and untrustworthy at that point.
const HelperSocket = "/var/run/goguma-helper.sock"

// HelperBinary is where `install` places the privileged helper. Deliberately
// outside the user's home: a root LaunchDaemon must not execute a binary from
// a directory the unprivileged user can rewrite, or the privilege separation
// is decorative.
const HelperBinary = "/usr/local/libexec/goguma-helper"

// Launchd labels, also used as the plist filenames.
const (
	DaemonLabel = "glass.goguma.daemon"
	HelperLabel = "glass.goguma.helper"
)

func platformLayout(home string) Layout {
	return Layout{
		StateDir:      filepath.Join(home, "Library", "Application Support", "goguma"),
		LogDir:        filepath.Join(home, "Library", "Logs", "goguma"),
		BinDir:        filepath.Join(home, ".local", "bin"),
		UnitDir:       filepath.Join(home, "Library", "LaunchAgents"),
		SystemUnitDir: "/Library/LaunchDaemons",
	}
}

// DaemonPlist is the user LaunchAgent definition path.
func (l Layout) DaemonPlist() string {
	return filepath.Join(l.UnitDir, DaemonLabel+".plist")
}

// HelperPlist is the root LaunchDaemon definition path.
func (l Layout) HelperPlist() string {
	return filepath.Join(l.SystemUnitDir, HelperLabel+".plist")
}
