package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/junnam586/goguma/internal/scan"
)

// applyCrontabWrap puts the wrapper into the user's crontab.
//
// `import` used to end by printing the new line and telling the user to run
// `crontab -e` and paste it. That is a setup command finishing with homework,
// and the cost of skipping the homework is invisible: the job is already
// registered as exactly-timed, so it just records "never detected" on every
// run afterwards and produces a warning about a configuration the user was
// never told was incomplete.
//
// The old code declined to do this on the grounds that editing a file the user
// owns is a bigger promise than the command should make. The promise is fine;
// what was missing was the care that earns it:
//
//   - the original is copied to `crontab.backup` before anything is written,
//     so there is always something to put back
//   - the rewrite is a single line, checked against the command the scan saw,
//     so a crontab edited in the meantime is refused rather than clobbered
//   - the result is read back and re-parsed, and the old crontab is restored
//     if the wrapped job is not in it
func applyCrontabWrap(ctx *Context, entryLine int, oldCommand, wrapped string) error {
	before, err := readCrontab()
	if err != nil {
		return err
	}

	after, err := scan.ReplaceCrontabCommand(before, entryLine, oldCommand, wrapped)
	if err != nil {
		return err
	}

	backup := ctx.Layout.CrontabBackup()
	if err := os.WriteFile(backup, []byte(before), 0o600); err != nil {
		return fmt.Errorf("couldn't save a backup to %s, so nothing was changed: %w", backup, err)
	}

	if err := installCrontab(after); err != nil {
		return fmt.Errorf("couldn't write the crontab (the original is untouched): %w", err)
	}

	// Read back rather than trust the exit status. `crontab` accepts input and
	// reports success in cases where what lands is not what was sent.
	live, err := readCrontab()
	if err != nil || !wrappedIsLive(live, entryLine, wrapped) {
		if restoreErr := installCrontab(before); restoreErr != nil {
			return fmt.Errorf(
				"the crontab was changed and couldn't be verified or restored; "+
					"your original is saved at %s: %w", backup, restoreErr)
		}
		return fmt.Errorf("the change didn't take, so the crontab was put back as it was")
	}
	return nil
}

// wrappedIsLive re-parses the installed crontab and confirms the job on that
// line really is the wrapped one.
func wrappedIsLive(text string, line int, wrapped string) bool {
	for _, e := range scan.ParseCrontab(text) {
		if e.Line == line && e.Command == wrapped {
			return true
		}
	}
	return false
}

func readCrontab() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// An empty crontab exits non-zero on most platforms, which is not a
		// failure to read one.
		if len(out) == 0 {
			return "", fmt.Errorf("couldn't read your crontab: %w", err)
		}
	}
	return string(out), nil
}

func installCrontab(text string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = bytes.NewReader([]byte(text))
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
