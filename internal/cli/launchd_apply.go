//go:build darwin

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// applyLaunchdWrap puts the wrapper into a launchd job's plist and reloads it.
//
// A launchd job is not a line to paste, it is a `ProgramArguments` array in a
// property list plus a service that has to be told to re-read it, so `import`
// used to hand this one back to the user with instructions. That is the same
// half-finished setup the crontab path had: the job is registered as
// exactly-timed the moment the choice is made, so an edit the user never
// performs shows up as a job that records never-detected forever.
//
// The plist is edited with the OS's own tools rather than by rewriting the
// file. `plutil` and `PlistBuddy` handle XML and binary plists identically and
// preserve every key goguma does not know about, which a naive marshal would
// silently drop.
func applyLaunchdWrap(ctx *Context, plistPath, label, oldCommand, markPath, slug string) error {
	if plistPath == "" || label == "" {
		return fmt.Errorf("this job has no plist on disk to edit")
	}

	// Where the plist lives decides both how it is written and which launchd
	// domain owns the job. All three locations are handled; see plistScope.
	scope, err := scopeOf(plistPath)
	if err != nil {
		return err
	}

	args, err := programArguments(plistPath)
	if err != nil {
		return err
	}
	program, hasProgram := programKey(plistPath)
	if len(args) == 0 && !hasProgram {
		return fmt.Errorf("%s names no command to run", filepath.Base(plistPath))
	}

	// Already wrapped, in whichever key launchd will actually read.
	if (len(args) > 0 && filepath.Base(args[0]) == "goguma-mark") ||
		(hasProgram && filepath.Base(program) == "goguma-mark") {
		return nil
	}

	// The plist can be edited between the scan and the write. `commandOf`
	// prefers ProgramArguments and falls back to Program, so the comparison
	// has to be made against whichever the scan actually saw.
	live := program
	if len(args) > 0 {
		live = strings.Join(args, " ")
	}
	if live != oldCommand {
		return fmt.Errorf("%s now runs %q, not what goguma read; nothing was changed", filepath.Base(plistPath), live)
	}

	backup, err := backUpPlist(ctx, plistPath, label)
	if err != nil {
		return err
	}

	if err := scope.edit(plistPath, markPath, slug); err != nil {
		scope.restore(backup, plistPath)
		return fmt.Errorf("couldn't edit %s (it was put back as it was): %w", filepath.Base(plistPath), err)
	}
	// A plist that no longer parses would make launchd refuse the job
	// entirely, so this is checked before anything is reloaded.
	if out, lerr := exec.Command("plutil", "-lint", plistPath).CombinedOutput(); lerr != nil {
		scope.restore(backup, plistPath)
		return fmt.Errorf("the edited plist didn't validate, so it was put back: %s", bytes.TrimSpace(out))
	}

	if err := scope.reload(plistPath, label); err != nil {
		scope.restore(backup, plistPath)
		_ = scope.reload(plistPath, label)
		return fmt.Errorf("couldn't reload the job (the plist was put back): %w", err)
	}

	// Read back rather than trust the edit.
	after, err := programArguments(plistPath)
	liveProgram, stillHasProgram := programKey(plistPath)
	badProgram := stillHasProgram && liveProgram != markPath
	if err != nil || len(after) < 2 || after[0] != markPath || after[1] != slug || badProgram {
		scope.restore(backup, plistPath)
		_ = scope.reload(plistPath, label)
		return fmt.Errorf("the change didn't take, so %s was put back as it was", filepath.Base(plistPath))
	}
	return nil
}

// programArguments reads the array as the OS sees it, so binary and XML plists
// behave the same.
func programArguments(path string) ([]string, error) {
	out, err := exec.Command("plutil", "-extract", "ProgramArguments", "json", "-o", "-", path).Output()
	if err != nil {
		return nil, nil // no such key; the caller reports it
	}
	var args []string
	if err := json.Unmarshal(out, &args); err != nil {
		return nil, fmt.Errorf("couldn't read ProgramArguments from %s: %w", filepath.Base(path), err)
	}
	return args, nil
}

// insertWrapper puts the wrapper in front of whatever the job runs.
//
// `Program` is the trap here. launchd reads it as the executable path and uses
// ProgramArguments only as argv, so a plist carrying both keys and wrapped by
// touching the array alone would exec the *original* binary with argv shifted
// to [goguma-mark, slug, ...]. The wrapper would never run, the job would get
// an argument list it never asked for, and goguma would record never-detected
// while quietly corrupting the thing it was supposed to be timing. Whenever
// Program is present it is repointed at the wrapper.
//
// A plist with Program and no ProgramArguments has no array to insert into, so
// one is built: [wrapper, slug, the original program].
func insertWrapper(path, markPath, slug string) error {
	args, _ := programArguments(path)
	program, hasProgram := programKey(path)

	if len(args) == 0 {
		if !hasProgram {
			return fmt.Errorf("%s names no command to run", filepath.Base(path))
		}
		if err := plistBuddy(path, "Add :ProgramArguments array"); err != nil {
			return err
		}
		for i, value := range []string{markPath, slug, program} {
			if err := plistBuddy(path, fmt.Sprintf("Add :ProgramArguments:%d string %s", i, value)); err != nil {
				return err
			}
		}
	} else {
		for i, value := range []string{markPath, slug} {
			if err := plistBuddy(path, fmt.Sprintf("Add :ProgramArguments:%d string %s", i, value)); err != nil {
				return err
			}
		}
	}

	if hasProgram {
		if err := plistBuddy(path, "Set :Program "+markPath); err != nil {
			return err
		}
	}
	return nil
}

// programKey reads the Program string, and whether the key is there at all.
func programKey(path string) (string, bool) {
	out, err := exec.Command("plutil", "-extract", "Program", "raw", "-o", "-", path).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func plistBuddy(path, command string) error {
	cmd := exec.Command("/usr/libexec/PlistBuddy", "-c", command, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s", bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}

// plistScope is where a plist lives, which decides two things at once: whether
// writing it needs root, and which launchd domain the job belongs to.
//
//	~/Library/LaunchAgents    yours          gui/<uid>    no root
//	/Library/LaunchAgents     all users      gui/<uid>    root to write
//	/Library/LaunchDaemons    the system     system       root to write and load
//
// Getting the domain wrong is not a small mistake. Bootstrapping a daemon into
// `gui/<uid>` fails, and doing it the other way round would load a user agent
// as root. Both are decided here, from the path, rather than assumed.
type plistScope struct {
	domain    string
	needsRoot bool
}

func scopeOf(path string) (plistScope, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return plistScope{}, err
	}
	gui := "gui/" + strconv.Itoa(os.Getuid())
	switch {
	case strings.HasPrefix(path, filepath.Join(home, "Library", "LaunchAgents")+string(os.PathSeparator)):
		return plistScope{domain: gui}, nil
	case strings.HasPrefix(path, "/Library/LaunchAgents/"):
		return plistScope{domain: gui, needsRoot: true}, nil
	case strings.HasPrefix(path, "/Library/LaunchDaemons/"):
		return plistScope{domain: "system", needsRoot: true}, nil
	}
	return plistScope{}, fmt.Errorf("%s isn't in a launchd directory goguma edits", path)
}

// edit applies the wrapper, through sudo when the file is root-owned.
//
// The root case is not a second implementation. The plist is copied to a
// temporary file, wrapped there with exactly the code the unprivileged path
// uses, validated, and only then installed back over the original. So the
// edit itself is never performed as root, and a failure halfway leaves the
// real file untouched rather than half-written.
func (s plistScope) edit(path, markPath, slug string) error {
	if !s.needsRoot {
		return insertWrapper(path, markPath, slug)
	}

	tmp, err := os.CreateTemp("", "goguma-plist-*.plist")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpName, original, 0o644); err != nil {
		return err
	}
	if err := insertWrapper(tmpName, markPath, slug); err != nil {
		return err
	}
	if out, lerr := exec.Command("plutil", "-lint", tmpName).CombinedOutput(); lerr != nil {
		return fmt.Errorf("the edited copy didn't validate, so nothing was installed: %s", bytes.TrimSpace(out))
	}
	return sudoInstall(tmpName, path)
}

func (s plistScope) restore(backup, path string) {
	if s.needsRoot {
		_ = sudoInstall(backup, path)
		return
	}
	if b, err := os.ReadFile(backup); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// reload makes launchd re-read the plist, in the domain that owns it.
//
// bootout on a job that is not loaded is an error and an expected one, so it
// is not checked; bootstrap is the step that has to succeed.
func (s plistScope) reload(path, label string) error {
	run := func(args ...string) *exec.Cmd { return exec.Command("launchctl", args...) }
	if s.needsRoot {
		run = func(args ...string) *exec.Cmd {
			return exec.Command("sudo", append([]string{"launchctl"}, args...)...)
		}
	}

	boot := run("bootout", s.domain+"/"+label)
	boot.Stdin, boot.Stdout, boot.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = boot.Run()

	cmd := run("bootstrap", s.domain, path)
	cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		// launchctl's own words are cryptic where the cause is not.
		if strings.Contains(msg, "Service is disabled") || strings.Contains(msg, "disabled") {
			return fmt.Errorf("launchd has this job disabled; run 'launchctl enable %s/%s' first",
				s.domain, label)
		}
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// sudoInstall puts src over dst as root, preserving dst's mode and owner the
// way `install` does.
func sudoInstall(src, dst string) error {
	cmd := exec.Command("sudo", "install", "-m", "0644", "-o", "root", "-g", "wheel", src, dst)
	cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s", bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}

func backUpPlist(ctx *Context, path, label string) (string, error) {
	dir := filepath.Join(ctx.Layout.StateDir, "launchd-backup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, label+".plist")
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return "", fmt.Errorf("couldn't save a backup to %s, so nothing was changed: %w", dest, err)
	}
	return dest, nil
}

// needsRootToWrap reports whether wrapping this plist will ask for a password,
// so the prompt can say so before the user chooses rather than after.
func needsRootToWrap(path string) bool {
	scope, err := scopeOf(path)
	return err == nil && scope.needsRoot
}
