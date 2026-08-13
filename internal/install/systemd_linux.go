package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/junnam586/goguma/internal/paths"
)

func platformArtifacts(l paths.Layout) []string {
	return []string{
		l.DaemonUnitFile(),
		l.HelperUnitFile(),
		paths.HelperBinary,
		// The unprivileged staging copy; see the darwin equivalent.
		filepath.Join(l.BinDir, "goguma-helper"),
	}
}

// daemonUnit is the user-level systemd service.
//
// WantedBy=default.target starts it on login. Restart=on-failure mirrors the
// launchd KeepAlive policy: recover from a crash, but do not fight a
// deliberate stop during uninstall.
func daemonUnit(binary, logDir string) string {
	return fmt.Sprintf(`[Unit]
Description=goguma daemon: wakes this machine for scheduled jobs
Documentation=https://github.com/junnam586/goguma
After=default.target

[Service]
Type=simple
ExecStart=%s --log %s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, binary, filepath.Join(logDir, "daemon.log"))
}

// helperUnit is the system-level privileged service.
//
// It starts at boot rather than at login so it can clear a sleep block left
// stranded by a crash or a forced power-off before anything else runs.
func helperUnit(binary string, ownerUID int) string {
	return fmt.Sprintf(`[Unit]
Description=goguma privileged helper: blocks sleep and schedules wakes
Documentation=https://github.com/junnam586/goguma
DefaultDependencies=no
After=basic.target

[Service]
Type=simple
ExecStart=%s --owner-uid %d
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binary, ownerUID)
}

// BuildPlan assembles the install steps for Linux.
func BuildPlan(l paths.Layout, withHelper bool) (*Plan, error) {
	cliSrc, err := findBinary("goguma")
	if err != nil {
		return nil, err
	}
	daemonSrc, err := findBinary("goguma-daemon")
	if err != nil {
		return nil, err
	}
	markSrc, err := findBinary("goguma-mark")
	if err != nil {
		return nil, err
	}

	// Resolved up front so a missing helper fails before anything is written.
	// See findBinary for why BinDir is searched as well as the sibling dir.
	var helperSrc string
	if withHelper {
		helperSrc, err = findBinary("goguma-helper", l.BinDir)
		if err != nil {
			return nil, err
		}
	}

	p := &Plan{}
	binCLI := filepath.Join(l.BinDir, "goguma")
	binDaemon := filepath.Join(l.BinDir, "goguma-daemon")
	binMark := filepath.Join(l.BinDir, "goguma-mark")

	copies := []struct{ src, dst, label string }{
		{cliSrc, binCLI, "goguma"},
		{daemonSrc, binDaemon, "goguma-daemon"},
		{markSrc, binMark, "goguma-mark"},
	}
	if withHelper {
		// Staged unprivileged so a re-run finds it. Never executed: the system
		// unit runs the root-owned copy.
		copies = append(copies, struct{ src, dst, label string }{
			helperSrc, filepath.Join(l.BinDir, "goguma-helper"), "goguma-helper",
		})
	}

	for _, c := range copies {
		p.Steps = append(p.Steps, Step{
			Description: fmt.Sprintf("install %s to %s", c.label, l.BinDir),
			Path:        c.dst,
			Run:         func() error { return copyFile(c.src, c.dst, 0o755) },
		})
	}

	p.Steps = append(p.Steps, Step{
		Description: "write the user systemd unit",
		Path:        l.DaemonUnitFile(),
		Run: func() error {
			if err := os.MkdirAll(filepath.Dir(l.DaemonUnitFile()), 0o755); err != nil {
				return err
			}
			return os.WriteFile(l.DaemonUnitFile(), []byte(daemonUnit(binDaemon, l.LogDir)), 0o644)
		},
	}, Step{
		Description: "enable and start the daemon",
		Run: func() error {
			if err := run("systemctl", "--user", "daemon-reload"); err != nil {
				return err
			}
			return run("systemctl", "--user", "enable", "--now", paths.DaemonUnit)
		},
	})

	if withHelper {
		uid := os.Getuid()

		p.Steps = append(p.Steps, Step{
			Description: fmt.Sprintf("install the privileged helper to %s", paths.HelperBinary),
			Privileged:  true,
			Path:        paths.HelperBinary,
			Run: func() error {
				if err := sudoRun("mkdir", "-p", filepath.Dir(paths.HelperBinary)); err != nil {
					return err
				}
				// Root-owned; see the darwin note on why this must not be
				// writable by the unprivileged user.
				return sudoRun("install", "-o", "root", "-g", "root", "-m", "755",
					helperSrc, paths.HelperBinary)
			},
		}, Step{
			Description: "write the system systemd unit for the helper",
			Privileged:  true,
			Path:        l.HelperUnitFile(),
			Run: func() error {
				return writeFileSudo(l.HelperUnitFile(),
					[]byte(helperUnit(paths.HelperBinary, uid)), 0o644)
			},
		}, Step{
			Description: "enable and start the privileged helper",
			Privileged:  true,
			Run: func() error {
				if err := sudoRun("systemctl", "daemon-reload"); err != nil {
					return err
				}
				return sudoRun("systemctl", "enable", "--now", paths.HelperUnit)
			},
		})
	}
	return p, nil
}

// Uninstall removes everything BuildPlan creates.
func Uninstall(l paths.Layout, keepState bool) []error {
	var errs []error

	_ = run("systemctl", "--user", "disable", "--now", paths.DaemonUnit)

	if fileExists(l.HelperUnitFile()) {
		if err := sudoRun("systemctl", "disable", "--now", paths.HelperUnit); err != nil {
			errs = append(errs, fmt.Errorf("stopping the helper: %w", err))
		}
	}

	for _, path := range []string{
		l.DaemonUnitFile(),
		filepath.Join(l.BinDir, "goguma-daemon"),
		filepath.Join(l.BinDir, "goguma-mark"),
		filepath.Join(l.BinDir, "goguma-helper"),
		filepath.Join(l.BinDir, "goguma"),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}
	_ = run("systemctl", "--user", "daemon-reload")

	hadHelperUnit := fileExists(l.HelperUnitFile())
	for _, path := range []string{l.HelperUnitFile(), paths.HelperBinary} {
		if !fileExists(path) {
			continue
		}
		if err := sudoRun("rm", "-f", path); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}
	// Checked BEFORE the removal above: testing afterwards asked about a
	// file that was just deleted, so the reload never ran and systemd kept
	// a dangling not-found unit until some unrelated reload.
	if hadHelperUnit {
		_ = sudoRun("systemctl", "daemon-reload")
	}

	// Kept unless the user explicitly purged; see the darwin equivalent for why
	// the default runs this way round.
	if !keepState {
		if err := os.RemoveAll(l.StateDir); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", l.StateDir, err))
		}
	}
	return errs
}

func run(args ...string) error {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
