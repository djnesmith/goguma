package cli

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
)

// exitWith carries a child process's exit status out through Main, which
// otherwise only distinguishes success from failure.
//
// `goguma run` is a wrapper, and a wrapper that flattens every non-zero status
// to 1 breaks the thing wrapping it: `goguma run -- make && deploy` would
// behave correctly, but a caller checking for a specific code would not.
type exitWith struct{ code int }

func (e exitWith) Error() string { return fmt.Sprintf("command exited with status %d", e.code) }

// ExitCode reports the status Main should exit with for this error, if the
// error carries one.
func ExitCode(err error) (int, bool) {
	var e exitWith
	if errors.As(err, &e) {
		return e.code, true
	}
	return 0, false
}

var cmdRun = &Command{
	Name:    "run",
	Summary: "run a command and hold sleep off until it finishes",
	Usage: `goguma run [--label <name>] -- <command> [args...]

Runs a command with sleep held off for exactly as long as it takes, including
with the lid closed, then releases. For work whose length nothing can predict
and no schedule describes: a coding agent, a long build, a big sync.

  goguma run -- claude -p "refactor the auth module"
  goguma run -- make -j8 release
  goguma run --label nightly-sync -- rsync -a ~/work backup:/work

The command's own output, input and exit status are passed straight through, so
this can be dropped in front of anything without changing what it does.

The '--' is required. Everything after it belongs to your command, including
flags that would otherwise be read as goguma's own; without it 'goguma run mycmd
--help' would print this page instead of your command's.

Unlike 'goguma awake' this needs no duration, because the process itself says
when it is done. Two runs at once each hold independently, and the machine stays
awake until the last of them finishes.

The hold is leased. If this wrapper is killed outright, or the machine loses
power, the hold expires by itself rather than being left on with nothing able to
release it. The safety cutouts still apply: a machine that overheats or runs low
on battery releases this exactly as it releases a job's, and no wrapped command
holds sleep for more than 12 hours however long it runs.

A run is never recorded as a job run: it teaches the estimator nothing and
appears in no job's history. It is in the event log, which is the record of what
held the machine awake.`,
	Run: func(ctx *Context, args []string) error {
		label, cmdline, err := splitRunArgs(args)
		if err != nil {
			return err
		}

		// The hold is opened before the command starts, so there is no window
		// in which the command is running unheld. If the daemon cannot be
		// reached this reports it and runs the command anyway: refusing to run
		// somebody's build because a background service is down would be a
		// wrapper that is worse than no wrapper.
		var started ipc.RunStartResp
		holding := true
		if err := callDaemon(ctx, ipc.OpRunStart,
			ipc.RunStartReq{Label: label}, &started); err != nil {
			holding = false
			ctx.Err.Printf("%s %s\n", ctx.Err.Warn(ctx.Err.Sym().Warn),
				ctx.Err.Muted("sleep is not being held: "+err.Error()))
		}

		// stderr, not stdout: a wrapper that writes to stdout corrupts whatever
		// the wrapped command's output was being piped into.
		if holding {
			r := ctx.Err
			r.Printf("%s %s %s\n", r.Good(r.Sym().Holding),
				r.Muted("holding sleep off while"), r.Bold(label+" runs"))
		}

		code, runErr := runChild(ctx, cmdline, started, holding)
		if runErr != nil {
			return runErr
		}
		if code != 0 {
			return exitWith{code: code}
		}
		return nil
	},
}

// splitRunArgs separates goguma's own flags from the command to run.
//
// The '--' is mandatory rather than merely supported. Main intercepts --help
// before a command's Run is reached, stopping its scan at '--', so a command
// line without one hands `goguma run mycmd --help` to the help printer instead
// of to mycmd. That is a wrapper silently not running what it was asked to run,
// which is the one thing this must never do, and no amount of parsing in here
// can fix it because the decision has already been taken by then.
func splitRunArgs(args []string) (label string, cmdline []string, err error) {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return "", nil, fmt.Errorf(
			"a '--' is needed before the command, as in: goguma run -- make release")
	}

	for i := 0; i < sep; i++ {
		a := args[i]
		switch {
		case a == "--label":
			if i+1 >= sep {
				return "", nil, fmt.Errorf("--label needs a name")
			}
			label = args[i+1]
			i++
		case strings.HasPrefix(a, "--label="):
			label = strings.TrimPrefix(a, "--label=")
		default:
			return "", nil, fmt.Errorf("unknown option %q before '--'", a)
		}
	}

	cmdline = args[sep+1:]
	if len(cmdline) == 0 {
		return "", nil, fmt.Errorf("nothing to run after '--'")
	}
	if strings.TrimSpace(label) == "" {
		label = cmdline[0]
	}
	return label, cmdline, nil
}

// runChild runs the command, keeping the hold alive while it does, and returns
// its exit status.
func runChild(ctx *Context, cmdline []string, started ipc.RunStartResp, holding bool) (int, error) {
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Start(); err != nil {
		// Release immediately: the hold was opened for a command that never
		// began, and waiting for the lease to lapse would hold the machine
		// awake for a minute and a half over a typo.
		endRun(ctx, started, holding, nil)
		return 0, fmt.Errorf("couldn't run %s: %w", cmdline[0], err)
	}

	// Signals are relayed rather than acted on. The child shares this process
	// group, so Ctrl-C already reaches it; what matters is that goguma does not
	// exit first, because exiting before the child means the hold is released
	// while the command is still running, and the machine can sleep underneath
	// it. So the signal is passed on and this keeps waiting for the child to
	// decide it is finished.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)

	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sig:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	// Renew at a third of the lease, so two consecutive failures still leave
	// the hold standing.
	if holding {
		lease := started.Lease.D()
		if lease > 0 {
			go renewLoop(ctx, started.ID, lease/3, done)
		}
	}

	waitErr := cmd.Wait()
	close(done)

	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
			// A process killed by a signal reports -1 here. Shells report
			// 128+signal for that, and something wrapping goguma expects the
			// shell's convention rather than a negative number it cannot use.
			if code < 0 {
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					code = 128 + int(ws.Signal())
				} else {
					code = 1
				}
			}
		} else {
			endRun(ctx, started, holding, nil)
			return 0, waitErr
		}
	}

	endRun(ctx, started, holding, &code)
	return code, nil
}

// renewLoop keeps the hold's lease alive until the command finishes.
func renewLoop(ctx *Context, id string, every time.Duration, done <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	warned := false
	for {
		select {
		case <-done:
			return
		case <-t.C:
			var resp ipc.RunRenewResp
			if err := callDaemon(ctx, ipc.OpRunRenew, ipc.RunRenewReq{ID: id}, &resp); err != nil {
				continue // transient; the lease has room for a missed renewal
			}
			// The hold lapsed or was released while the command is still
			// running. Said once rather than every tick, because the command's
			// own output is the thing the user is reading.
			if !resp.Held && !warned {
				warned = true
				ctx.Err.Printf("%s %s\n", ctx.Err.Warn(ctx.Err.Sym().Warn),
					ctx.Err.Muted("the sleep hold lapsed; the machine may sleep before this finishes"))
			}
		}
	}
}

// endRun closes the hold, best effort.
//
// Failure here is not reported: the command has already run, its output is on
// screen, and its exit status is what the caller is waiting for. A message
// about the background service after all of that would attach goguma's problem
// to somebody else's command. The lease is what actually guarantees the hold
// goes away.
func endRun(ctx *Context, started ipc.RunStartResp, holding bool, code *int) {
	if !holding || started.ID == "" {
		return
	}
	_ = callDaemon(ctx, ipc.OpRunEnd, ipc.RunEndReq{ID: started.ID, ExitCode: code}, nil)
}
