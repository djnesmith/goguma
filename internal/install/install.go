// Package install registers and removes WakeGuard's system services.
//
// A stated v1 requirement is that installation needs no manual plist or unit
// editing, and that uninstall leaves nothing behind. Both halves live here so
// they can be read against each other — every file this package creates is
// listed in Artifacts, and uninstall removes exactly that list.
package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/junnam/wakeguard/internal/paths"
)

// Plan describes what an install will do, so it can be shown before anything
// is changed.
type Plan struct {
	Steps []Step
}

// Step is one action, with whether it needs elevation.
type Step struct {
	Description string
	// Privileged steps are the ones that will prompt for a password. Being
	// explicit about which they are, before running anything, is the minimum
	// courtesy for a tool that installs a root daemon.
	Privileged bool
	// Path is the file created or modified, for the uninstall inventory.
	Path string
	Run  func() error
}

// Artifacts lists every path an install creates.
func Artifacts(l paths.Layout) []string {
	return append([]string{
		filepath.Join(l.BinDir, "wakeguard"),
		filepath.Join(l.BinDir, "wakeguard-mark"),
		filepath.Join(l.BinDir, "wakeguard-daemon"),
	}, platformArtifacts(l)...)
}

// copyFile duplicates a binary, preserving the executable bit.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()

	// Write to a temp file then rename, so replacing a running binary cannot
	// leave a partially written executable on disk.
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// sudoRun executes a command with elevation.
//
// Uses the user's own sudo rather than embedding any privilege of its own, so
// the authorization decision stays where the OS put it and the prompt is the
// one the user already recognises.
func sudoRun(args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo %s: %w", args[0], err)
	}
	return nil
}

// writeFileSudo writes content to a root-owned path via a temp file.
func writeFileSudo(path string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "wakeguard-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := sudoRun("install", "-m", fmt.Sprintf("%o", mode), tmpName, path); err != nil {
		return err
	}
	return nil
}

// ExecutableDir is where the currently running binary lives, used to find its
// siblings when installing from a build directory.
func ExecutableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return filepath.Dir(resolved), nil
}

// findBinary locates a component binary, searching the executable's own
// directory first, then any extra directories the caller knows about, then
// PATH. Installing from a build tree and from an unpacked release both work
// without the user specifying paths.
//
// The extra directories matter for the helper. Install copies the CLI, daemon
// and mark wrapper into BinDir, but the helper's destination is
// /usr/local/libexec, so nothing ever put a copy of it next to the installed
// CLI. Re-running `wakeguard install` from the installed location therefore
// failed with "could not find wakeguard-helper", which reads as a broken
// install when everything is fine — the binary was simply never staged where
// the re-run would look. Install now stages it into BinDir as well, and this
// searches there.
func findBinary(name string, extraDirs ...string) (string, error) {
	if dir, err := ExecutableDir(); err == nil {
		if p, ok := executableAt(filepath.Join(dir, name)); ok {
			return p, nil
		}
	}
	for _, dir := range extraDirs {
		if p, ok := executableAt(filepath.Join(dir, name)); ok {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("could not find %s next to the wakeguard binary or on PATH; "+
		"build it first with: go build ./cmd/%s", name, name)
}

func executableAt(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return path, true
}
