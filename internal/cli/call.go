package cli

import (
	"errors"
	"time"

	"github.com/junnam586/goguma/internal/ipc"
)

// callDaemon sends a request and translates transport errors into advice.
//
// A raw "nothing is listening on /path/daemon.sock" is accurate but unhelpful
// as the last thing a user sees. Every CLI path goes through here so the
// absent-daemon case reads the same way everywhere, instead of some commands
// explaining what to do and others leaking a socket path.
func callDaemon(ctx *Context, op ipc.Op, payload, out any) error {
	err := callDaemonTimeout(ctx, op, payload, out, timeoutFor(op))
	return err
}

// timeoutFor bounds a call by what the operation actually has to do.
//
// Most commands are a state read and answer instantly. `sync` re-reads every
// watched scheduler and, on a cold cache, reconstructs two weeks of sleep
// history from the system log, around four seconds on macOS, which lands
// right on the default limit. Giving the one slow command room is better than
// loosening the bound for reads that should never take that long.
func timeoutFor(op ipc.Op) time.Duration {
	switch op {
	case ipc.OpSync, ipc.OpScanImport:
		return 60 * time.Second
	default:
		return ipc.DefaultTimeout
	}
}

func callDaemonTimeout(ctx *Context, op ipc.Op, payload, out any, timeout time.Duration) error {
	c, derr := ipc.Dial(ctx.Socket, timeout)
	if derr != nil {
		if errors.Is(derr, ipc.ErrNotRunning) {
			return errDaemonDown()
		}
		return derr
	}
	defer c.Close()
	err := c.Call(op, payload, out)
	if errors.Is(err, ipc.ErrNotRunning) {
		return errDaemonDown()
	}
	return err
}
