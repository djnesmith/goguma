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

// PeerOf reads SO_PEERCRED, the kernel's record of who opened the connection.
// See the darwin implementation for why this must come from the kernel.
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
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			innerErr = fmt.Errorf("reading peer credentials: %w", e)
			return
		}
		p.UID = cred.Uid
		p.PID = int(cred.Pid)
	})
	if err != nil {
		return Peer{}, err
	}
	return p, innerErr
}

// AllowOwnerOrRoot authorizes only the given uid and root.
func AllowOwnerOrRoot(owner uint32) func(Peer) error {
	return func(p Peer) error {
		if p.UID == owner || p.UID == 0 {
			return nil
		}
		return fmt.Errorf("uid %d is not authorized to control WakeGuard", p.UID)
	}
}

// AllowSelf authorizes only the current user.
func AllowSelf() func(Peer) error {
	return AllowOwnerOrRoot(uint32(os.Getuid()))
}
