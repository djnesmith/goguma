package detect

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Snapshot lists running processes with their full command lines.
//
// This shells out to `ps` rather than using sysctl(KERN_PROC_ALL) directly.
// Reading another process's full argv on macOS requires KERN_PROCARGS2, which
// is restricted and increasingly gated by platform hardening; `ps` is
// setgid-procview precisely to make this work, so it sees command lines that
// a plain sysctl from our process would not. The cost is one short-lived
// subprocess, and only while a wake window is actually open.
func Snapshot(ctx context.Context) ([]Process, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// -A all processes, -ww unlimited width so long command lines are not
	// truncated mid-argument (a truncated line silently breaks matching).
	out, err := exec.CommandContext(ctx, "ps", "-Awwo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(string(out))
}

func parsePS(out string) ([]Process, error) {
	var procs []Process
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimLeft(sc.Text(), " \t")
		if line == "" {
			continue
		}
		sp := strings.IndexAny(line, " \t")
		pid := 0
		perr := error(nil)
		if sp > 0 {
			pid, perr = strconv.Atoi(line[:sp])
		}
		if sp <= 0 || perr != nil {
			// Not a "PID command" row. `ps -o args=` prints argv newlines
			// raw, so a multi-line command (`python3 -c $'...\n...'`,
			// `osascript -e` bodies) continues on lines of its own. They
			// belong to the previous process: dropping them truncated the
			// command at the newline and hid exactly the tokens a match
			// pattern needs, and a continuation that happened to start
			// with digits parsed as a phantom process. (A continuation
			// line that starts with "<digits> " is still ambiguous; that
			// shape is accepted as a new row because rejecting it would
			// need the row's real PID list to disambiguate.)
			if n := len(procs); n > 0 {
				procs[n-1].Command += " " + strings.TrimSpace(line)
			}
			continue
		}
		cmd := strings.TrimSpace(line[sp+1:])
		if cmd == "" {
			continue
		}
		procs = append(procs, Process{PID: pid, Command: cmd})
	}
	// A partial table must not be treated as authoritative: a pattern job
	// judged against half the process list reads "not running" and releases
	// its hold mid-run.
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning ps output: %w", err)
	}
	return procs, nil
}

// Alive reports whether a pid is still running.
//
// Used to end a hold as soon as a known process exits. Signal 0 performs the
// permission and existence check without delivering anything.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processAlive(pid)
}
