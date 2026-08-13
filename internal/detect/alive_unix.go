package detect

import (
	"errors"
	"os"
	"syscall"
)

// processAlive checks for a live process without signalling it.
//
// Signal 0 is the standard existence-and-permission probe. EPERM counts as
// alive: the process exists, we simply may not signal it, which is exactly
// the case for a job running under a different uid, and treating that as
// "exited" would release the hold while the job is still working.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM)
}
