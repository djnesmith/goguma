package ipc

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Peer is the identity of the process on the other end of a socket.
type Peer struct {
	UID uint32
	PID int
}

// PeerOf reads the peer's credentials from the kernel.
//
// This is the helper's trust boundary, so it must come from the kernel rather
// than from anything the client sends. macOS exposes the peer euid via
// LOCAL_PEEREUID; the pid comes from LOCAL_PEERPID and is advisory (it can be
// recycled), used only for logging — never for authorization.
func PeerOf(conn net.Conn) (Peer, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, fmt.Errorf("connection is not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{}, err
	}

	var p Peer
	var innerErr error
	err = raw.Control(func(fd uintptr) {
		xucred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			innerErr = fmt.Errorf("reading peer credentials: %w", e)
			return
		}
		p.UID = xucred.Uid
		// A missing pid is not fatal: it is only ever used for log context.
		if pid, e := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); e == nil {
			p.PID = pid
		}
	})
	if err != nil {
		return Peer{}, err
	}
	return p, innerErr
}

// AllowOwnerOrRoot authorizes only the given uid and root. This is what the
// privileged helper installs: the sleep-block primitive it exposes is
// dangerous enough (it can pin a lid-closed laptop awake in a bag) that any
// other local user must not be able to reach it.
func AllowOwnerOrRoot(owner uint32) func(Peer) error {
	return func(p Peer) error {
		if p.UID == owner || p.UID == 0 {
			return nil
		}
		return fmt.Errorf("uid %d is not authorized to control WakeGuard", p.UID)
	}
}

// AllowSelf authorizes only the current user, for the daemon's own socket.
func AllowSelf() func(Peer) error {
	return AllowOwnerOrRoot(uint32(os.Getuid()))
}
