// Command goguma-mark wraps a scheduled job so goguma knows exactly
// when it starts and stops.
//
//	goguma-mark <job-name> -- <command> [args...]
//
// It runs the command unchanged and exits with the command's exit status,
// having told the daemon the real start time, end time, and exit code. That
// is strictly better than pattern-matching the process table: it cannot miss
// a fast job, cannot be confused by a similar command line, and is the only
// way to observe whether the job actually succeeded.
//
// The overriding design constraint is that this must never be the reason a
// user's job fails. Every interaction with the daemon is best-effort: if the
// daemon is down, unreachable, slow, or returns an error, the wrapped command
// still runs and its exit status is still propagated faithfully.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
	"github.com/junnam586/goguma/internal/paths"
)

const usage = `goguma-mark: tell goguma exactly when a job runs

usage:
  goguma-mark <job-name> -- <command> [args...]

example, in a crontab line:
  0 9 * * * goguma-mark morning-briefing -- hermes cron run morning-briefing

The command runs unchanged and its exit status is passed through. If the
goguma background service isn't running, the command still runs normally.
`

// markTimeout is short by design. This runs on the job's critical path, so a
// slow or wedged daemon must cost the job almost nothing.
const markTimeout = 2 * time.Second

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	jobName := args[0]
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintf(os.Stderr, "goguma-mark: no command given after %q\n\n%s", jobName, usage)
		os.Exit(2)
	}

	os.Exit(run(jobName, rest))
}

func run(jobName string, argv []string) int {
	sock := paths.MustResolve().DaemonSocket()
	commandLine := strings.Join(argv, " ")

	cmd := exec.Command(argv[0], argv[1:]...)
	// Full stdio passthrough: cron captures the job's output for mail, and
	// swallowing it would change behaviour the user depends on.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "goguma-mark: can't run %s: %v\n", argv[0], err)
		// Report the failed start so the window closes rather than being held
		// open for a job that will never appear.
		notify(sock, ipc.OpMarkEnd, ipc.MarkEndReq{Job: jobName, ExitCode: 127})
		return 127
	}

	// Forward termination signals to the child so the job sees the same
	// signals it would have received without the wrapper. Installed BEFORE
	// the start-mark round trip: that call can block for the full mark
	// timeout against a slow daemon, and a SIGTERM landing in that window
	// used to kill the wrapper at Go's default disposition, orphaning the
	// child and never sending the end-mark, so the window was held to its
	// ceiling for a job nobody was running.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	notify(sock, ipc.OpMarkStart, ipc.MarkStartReq{
		Job: jobName, PID: cmd.Process.Pid, Command: commandLine,
	})

	err := cmd.Wait()
	close(done)
	signal.Stop(sigs)

	code := exitCode(err)
	notify(sock, ipc.OpMarkEnd, ipc.MarkEndReq{
		Job: jobName, PID: cmd.Process.Pid, ExitCode: code,
	})
	return code
}

// exitCode extracts the child's status, mirroring shell conventions for a
// process killed by a signal (128 + signal number).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// notify sends a mark, ignoring every failure.
//
// A warning goes to stderr only when the daemon is reachable but rejects the
// mark; that means the job name is not registered, which is a real
// misconfiguration worth surfacing in cron's output. A daemon that is simply
// not running is silent: that is an expected state, not an error, and noisy
// mail from every cron run would be worse than useless.
func notify(sock string, op ipc.Op, payload any) {
	c, err := ipc.Dial(sock, markTimeout)
	if err != nil {
		return
	}
	defer c.Close()

	var resp ipc.MarkResp
	if err := c.Call(op, payload, &resp); err != nil {
		return
	}
	if !resp.Known {
		if name, ok := jobNameOf(payload); ok {
			fmt.Fprintf(os.Stderr,
				"goguma-mark: no job named %q is registered; this run isn't being tracked.\n"+
					"  register it with: goguma add --name %s --cron '<schedule>' --detection mark\n",
				name, name)
		}
	}
}

func jobNameOf(payload any) (string, bool) {
	switch p := payload.(type) {
	case ipc.MarkStartReq:
		return p.Job, true
	case ipc.MarkEndReq:
		return p.Job, true
	}
	return "", false
}
