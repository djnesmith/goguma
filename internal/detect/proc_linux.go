package detect

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Snapshot lists running processes by reading /proc directly.
//
// No subprocess is needed on Linux: /proc/<pid>/cmdline is readable and gives
// the exact argv, NUL-separated. This is both faster and more accurate than
// parsing ps output, so the platforms deliberately diverge here rather than
// sharing a lowest-common-denominator implementation.
func Snapshot(ctx context.Context) ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	procs := make([]Process, 0, 256)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return procs, err
		}
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid directory
		}
		cmd, ok := readCmdline(pid)
		if !ok {
			continue
		}
		procs = append(procs, Process{PID: pid, Command: cmd})
	}
	return procs, nil
}

func readCmdline(pid int) (string, bool) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		// The process exited between listing and reading, which is routine.
		return "", false
	}
	if len(b) == 0 {
		// Kernel threads have an empty cmdline; they are never user jobs.
		return "", false
	}
	// argv is NUL-separated with a trailing NUL.
	cmd := strings.TrimRight(string(b), "\x00")
	return strings.ReplaceAll(cmd, "\x00", " "), true
}

// Alive reports whether a pid is still running.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processAlive(pid)
}
