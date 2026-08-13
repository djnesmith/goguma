package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/junnam586/goguma/internal/paths"
)

// sandboxLayout is a Layout that addresses nothing real.
//
// The service names matter as much as the directories. Uninstall stops
// services by name, and while those names came from package constants there
// was no such thing as a harmless Layout: any test that called Uninstall would
// have booted out the developer's own daemon, which is why this function went
// untested through two shipped bugs. Naming the services after the test makes
// the teardown address services that do not exist, so it runs and does nothing.
func sandboxLayout(t *testing.T) paths.Layout {
	t.Helper()
	root := t.TempDir()
	return paths.Layout{
		StateDir:      filepath.Join(root, "state"),
		LogDir:        filepath.Join(root, "logs"),
		BinDir:        filepath.Join(root, "bin"),
		UnitDir:       filepath.Join(root, "units"),
		SystemUnitDir: filepath.Join(root, "system-units"),
		DaemonService: "test.goguma.absent.daemon",
		HelperService: "test.goguma.absent.helper",
		// Inside the sandbox, so the sudo removal has nothing real to reach.
		HelperBinary: filepath.Join(root, "libexec", "goguma-helper"),
	}
}

// populate lays down everything an install would have written, except the
// root-owned files, which no test may create.
func populate(t *testing.T, l paths.Layout) {
	t.Helper()
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.UnitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.HistoryDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goguma", "goguma-daemon", "goguma-mark", "goguma-helper"} {
		if err := os.WriteFile(filepath.Join(l.BinDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(unitPath(l), []byte("unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{l.JobsFile(), l.ConfigFile()} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUninstallRemovesEveryBinaryItInstalled(t *testing.T) {
	l := sandboxLayout(t)
	populate(t, l)

	if errs := Uninstall(l, true); len(errs) != 0 {
		t.Fatalf("uninstall reported problems on a clean sandbox: %v", errs)
	}

	for _, name := range []string{"goguma", "goguma-daemon", "goguma-mark", "goguma-helper"} {
		if _, err := os.Stat(filepath.Join(l.BinDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall", name)
		}
	}
	if _, err := os.Stat(unitPath(l)); !os.IsNotExist(err) {
		t.Error("the service definition survived uninstall")
	}
}

// TestUninstallKeepsStateUnlessPurged is the regression guard for the bug that
// shipped: the flag defaulted to deleting jobs and duration history while the
// comment above it claimed the opposite. History takes weeks of real runs to
// accumulate and cannot be reconstructed.
func TestUninstallKeepsStateUnlessPurged(t *testing.T) {
	l := sandboxLayout(t)
	populate(t, l)

	Uninstall(l, true)

	if _, err := os.Stat(l.JobsFile()); err != nil {
		t.Errorf("jobs.json was deleted despite keepState: %v", err)
	}
	if _, err := os.Stat(l.HistoryDir()); err != nil {
		t.Errorf("run history was deleted despite keepState: %v", err)
	}
}

func TestUninstallPurgesStateWhenAsked(t *testing.T) {
	l := sandboxLayout(t)
	populate(t, l)

	Uninstall(l, false)

	if _, err := os.Stat(l.StateDir); !os.IsNotExist(err) {
		t.Error("the state directory survived an explicit purge")
	}
}

// TestUninstallIsIdempotent covers the second run, which is what a user does
// after a partial failure. Reporting problems for files that are already gone
// turns a successful cleanup into an error message.
func TestUninstallIsIdempotent(t *testing.T) {
	l := sandboxLayout(t)
	populate(t, l)

	Uninstall(l, true)
	if errs := Uninstall(l, true); len(errs) != 0 {
		t.Errorf("second uninstall reported problems: %v", errs)
	}
}

// TestUninstallOnAMachineThatNeverInstalled is the other order users manage to
// find: running uninstall before install, or after deleting things by hand.
func TestUninstallOnAMachineThatNeverInstalled(t *testing.T) {
	l := sandboxLayout(t)
	if errs := Uninstall(l, true); len(errs) != 0 {
		t.Errorf("uninstall on an empty machine reported problems: %v", errs)
	}
}
