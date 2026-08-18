package install

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/paths"
)

// plistText escapes a value for interpolation into a plist's XML.
//
// The plists below are assembled with Sprintf, so any path containing `&` or
// `<` produced a document launchd cannot parse. That failure is silent in the
// worst way: install reports every step succeeded, launchctl declines to load
// the job, and the user is left with a tool that never wakes anything and no
// indication why. Paths are not all controlled by us — the state directory is
// user-settable, and a home directory is whatever it is.
func plistText(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

func platformArtifacts(l paths.Layout) []string {
	return []string{
		l.DaemonPlist(),
		l.HelperPlist(),
		l.HelperBinary,
		// The unprivileged staging copy. Listed so uninstall's promise that
		// every file it wrote is removed stays literally true.
		filepath.Join(l.BinDir, "goguma-helper"),
	}
}

// daemonPlist is the user LaunchAgent.
//
// KeepAlive with SuccessfulExit=false restarts the daemon if it crashes but
// not if it exits cleanly, so `launchctl bootout` during uninstall does not
// fight a respawn. RunAtLoad starts it at login.
func daemonPlist(label, binary, logDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>--log</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>ProcessType</key>
    <string>Background</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, plistText(label), plistText(binary),
		plistText(filepath.Join(logDir, "daemon.log")),
		plistText(filepath.Join(logDir, "daemon.err.log")))
}

// helperPlist is the root LaunchDaemon.
//
// RunAtLoad matters more here than for the daemon: it makes the helper start
// at boot before any user logs in, which is what lets it clear a sleep block
// stranded by a crash or a forced power-off. Without that, a machine could
// come back up permanently unable to sleep.
func helperPlist(label, binary string, ownerUID int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>--owner-uid</string>
        <string>%d</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>StandardErrorPath</key>
    <string>/var/log/goguma-helper.log</string>
</dict>
</plist>
`, plistText(label), plistText(binary), ownerUID)
}

// BuildPlan assembles the install steps for macOS.
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

	// Located before the plan is built so a missing helper fails here, with
	// nothing yet written, rather than half way through an install.
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
		// Staged unprivileged alongside the others so a later re-run finds it.
		// This copy is never executed (the LaunchDaemon runs the root-owned
		// one in /usr/local/libexec), so it grants no privilege of its own.
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
		Description: "write the LaunchAgent that starts goguma at login",
		Path:        l.DaemonPlist(),
		Run: func() error {
			if err := os.MkdirAll(filepath.Dir(l.DaemonPlist()), 0o755); err != nil {
				return err
			}
			return os.WriteFile(l.DaemonPlist(), []byte(daemonPlist(l.DaemonService, binDaemon, l.LogDir)), 0o644)
		},
	}, Step{
		Description: "start the background service",
		Run:         func() error { return loadAgent(l.DaemonService, l.DaemonPlist()) },
	})

	if withHelper {
		uid := os.Getuid()

		p.Steps = append(p.Steps, Step{
			Description: fmt.Sprintf("install the privileged helper to %s", l.HelperBinary),
			Privileged:  true,
			Path:        l.HelperBinary,
			Run: func() error {
				if err := sudoRun("mkdir", "-p", filepath.Dir(l.HelperBinary)); err != nil {
					return err
				}
				// Root-owned and not group- or world-writable: a LaunchDaemon
				// must never execute a binary the unprivileged user can
				// rewrite, or the privilege separation is decorative.
				return sudoRun("install", "-o", "root", "-g", "wheel", "-m", "755",
					helperSrc, l.HelperBinary)
			},
		}, Step{
			Description: "write the LaunchDaemon for the privileged helper",
			Privileged:  true,
			Path:        l.HelperPlist(),
			Run: func() error {
				return writeFileSudo(l.HelperPlist(),
					[]byte(helperPlist(l.HelperService, l.HelperBinary, uid)), 0o644)
			},
		}, Step{
			Description: "load the privileged helper",
			Privileged:  true,
			Run:         func() error { return loadDaemon(l.HelperService, l.HelperPlist()) },
		})
	}
	return p, nil
}

// bootoutSettleTimeout bounds the wait for a service to actually disappear.
//
// `launchctl bootout` returns as soon as the request is accepted, not once the
// service is gone. Bootstrapping into that window fails with "Bootstrap failed:
// 5: Input/output error", an error that names no service, suggests no cause,
// and on a re-install leaves the daemon booted out and not replaced, so the
// tool ends up worse off than before it was run. Waiting for the unload to
// settle is the difference between a reliable re-install and a coin flip.
const (
	bootoutSettleTimeout = 5 * time.Second
	bootoutPollInterval  = 100 * time.Millisecond
)

// loadAgent registers a user LaunchAgent.
func loadAgent(label, plist string) error {
	target := fmt.Sprintf("gui/%d", os.Getuid())
	// Unload first so re-running install picks up a changed plist rather than
	// silently keeping the old definition running.
	_ = exec.Command("launchctl", "bootout", target+"/"+label).Run()
	waitUntilUnloaded(target+"/"+label, false)

	out, err := exec.Command("launchctl", "bootstrap", target, plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// loadDaemon registers the root LaunchDaemon.
func loadDaemon(label, plist string) error {
	_ = exec.Command("sudo", "launchctl", "bootout", "system/"+label).Run()
	waitUntilUnloaded("system/"+label, true)
	return sudoRun("launchctl", "bootstrap", "system", plist)
}

// waitUntilUnloaded polls until launchd no longer knows the service.
//
// Best-effort by design: if the service is somehow still present when the
// timeout expires, the bootstrap is attempted anyway and its own error is what
// the user sees. Returning an error here would replace a real diagnostic with a
// timeout message.
func waitUntilUnloaded(serviceTarget string, privileged bool) {
	deadline := time.Now().Add(bootoutSettleTimeout)
	for time.Now().Before(deadline) {
		var cmd *exec.Cmd
		if privileged {
			cmd = exec.Command("sudo", "-n", "launchctl", "print", serviceTarget)
		} else {
			cmd = exec.Command("launchctl", "print", serviceTarget)
		}
		// A non-zero exit means launchd cannot find it, which is what we want.
		if err := cmd.Run(); err != nil {
			return
		}
		time.Sleep(bootoutPollInterval)
	}
}

// Uninstall removes everything BuildPlan creates.
// uiPreferenceDomain is the menu bar app's UserDefaults suite.
//
// Duplicated from the app's bundle identifier because Go cannot read Swift's,
// and pinned by TestUninstallClearsTheAppsPreferences so the two cannot drift
// apart silently.
const uiPreferenceDomain = "glass.goguma.ui"

func Uninstall(l paths.Layout, keepState bool) []error {
	var errs []error

	target := fmt.Sprintf("gui/%d", os.Getuid())
	if err := exec.Command("launchctl", "bootout", target+"/"+l.DaemonService).Run(); err != nil {
		// Not an error worth reporting: the daemon may simply not be loaded.
		_ = err
	}
	if fileExists(l.HelperPlist()) {
		if err := sudoRun("launchctl", "bootout", "system/"+l.HelperService); err != nil {
			errs = append(errs, fmt.Errorf("unloading the helper: %w", err))
		}
	}

	for _, path := range []string{
		l.DaemonPlist(),
		filepath.Join(l.BinDir, "goguma-daemon"),
		filepath.Join(l.BinDir, "goguma-mark"),
		filepath.Join(l.BinDir, "goguma-helper"),
		filepath.Join(l.BinDir, "goguma"),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}

	// The menu bar app's own preferences.
	//
	// Not part of StateDir, so `--purge` never reached them, and they outlive
	// the app being dragged to the Trash. One of them is
	// `goguma.hasPresentedFirstRun`, which is what stops the popover
	// introducing itself: leaving it set means a fresh install on a machine
	// that once had goguma opens to nothing at all and the person is left with
	// an icon and no reason to click it. Removed on any uninstall, not only
	// --purge, because it is not state anybody wants kept.
	if err := exec.Command("defaults", "delete", uiPreferenceDomain).Run(); err != nil {
		// Nothing to delete is the ordinary case, and not worth reporting.
		_ = err
	}

	for _, path := range []string{l.HelperPlist(), l.HelperBinary} {
		if !fileExists(path) {
			continue
		}
		if err := sudoRun("rm", "-f", path); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}

	// Callers pass keepState=true unless the user asked to purge. The CLI
	// default is to keep, because a reinstall should not lose duration history
	// that took weeks of real runs to accumulate and cannot be reconstructed.
	// This comment used to claim that was the behaviour while the flag it
	// described defaulted the other way, so a plain uninstall deleted
	// everything.
	if !keepState {
		if err := os.RemoveAll(l.StateDir); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", l.StateDir, err))
		}
	}
	return errs
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
