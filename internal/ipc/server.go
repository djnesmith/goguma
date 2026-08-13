package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Handler processes one request and returns the payload to send back.
type Handler func(ctx context.Context, op Op, payload json.RawMessage) (any, error)

// Server listens on a Unix socket and dispatches frames to a Handler.
type Server struct {
	path     string
	ln       net.Listener
	handler  Handler
	log      *slog.Logger
	authz    func(Peer) error
	wg       sync.WaitGroup
	closeMu  sync.Mutex
	closed   bool
	shutdown chan struct{}

	// conns tracks accepted connections so Close can sever them. Closing
	// only the listener left keep-alive clients (the GUI polls over one
	// connection) pinning their handler goroutines, which blocked shutdown
	// at wg.Wait until launchd or systemd killed the process.
	connMu sync.Mutex
	conns  map[net.Conn]struct{}

	// socketUID owns the socket file. -1 leaves it with the process's own uid,
	// which is right for the daemon and wrong for the root helper. See
	// WithSocketOwner.
	socketUID int
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithAuthz installs a peer-credential check. The helper uses this as its
// trust boundary: it is the only thing standing between an unprivileged
// process and a root-held sleep block.
func WithAuthz(f func(Peer) error) ServerOption {
	return func(s *Server) { s.authz = f }
}

func WithLogger(l *slog.Logger) ServerOption {
	return func(s *Server) { s.log = l }
}

// WithSocketOwner hands the socket file to a uid other than the process's own.
//
// Required whenever a root process serves an unprivileged client, which is
// exactly the helper's situation. Binding as root produces a root-owned socket,
// and at mode 0600 the daemon (running as the logged-in user) cannot open it
// at all. It never reaches the peer-credential check that was supposed to
// authorise it; the connection fails at the filesystem, and the error surfaces
// as "nothing is listening on the socket", which points at a helper that is in
// fact running perfectly.
//
// Ownership rather than a wider mode: 0600 owned by the one permitted uid keeps
// the socket unreachable by every other unprivileged user on the machine, which
// a group- or world-accessible mode would not. `WithAuthz` still runs and is
// still the trust boundary; this only lets the authorised client get far
// enough to be checked.
func WithSocketOwner(uid int) ServerOption {
	return func(s *Server) { s.socketUID = uid }
}

// maxSocketPath is the practical limit on a Unix socket path.
//
// sockaddr_un.sun_path is a fixed 104-byte buffer on macOS and 108 on Linux;
// exceeding it fails with a bare EINVAL ("invalid argument") that says nothing
// about the actual cause. Checking up front turns a genuinely baffling startup
// failure into an actionable one, which matters because it is reachable in
// practice through a long username or a GOGUMA_STATE_DIR override.
const maxSocketPath = 100

// Listen binds the socket. mode is applied to the socket file itself.
//
// A stale socket from a previous run is removed first; a daemon that was
// SIGKILLed leaves the file behind, and refusing to start because of it would
// mean a crash permanently disables goguma until the user cleans up by
// hand.
func Listen(path string, mode os.FileMode, h Handler, opts ...ServerOption) (*Server, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf(
			"the socket path is %d bytes, over the %d-byte limit the operating system allows: %s\n"+
				"set GOGUMA_STATE_DIR to a shorter directory",
			len(path), maxSocketPath, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}

	s := &Server{
		path:      path,
		handler:   h,
		log:       slog.Default(),
		shutdown:  make(chan struct{}),
		socketUID: -1,
	}
	// Applied before bind so the socket's ownership is known by the time the
	// file exists; a client could otherwise connect during the window between
	// bind and chown.
	for _, o := range opts {
		o(s)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	s.ln = ln

	// Set permissions after bind; the umask would otherwise widen them.
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	if s.socketUID >= 0 && s.socketUID != os.Getuid() {
		if err := os.Chown(path, s.socketUID, -1); err != nil {
			ln.Close()
			return nil, fmt.Errorf(
				"handing %s to uid %d: %w (without this the client cannot open the socket)",
				path, s.socketUID, err)
		}
	}
	return s, nil
}

// removeStaleSocket deletes a socket file only if nothing is listening on it,
// so two concurrent daemons cannot silently steal each other's socket.
func removeStaleSocket(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to replace it", path)
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return fmt.Errorf("another goguma process is already listening on %s", path)
	}
	return os.Remove(path)
}

// Serve accepts connections until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
		case <-s.shutdown:
		}
		s.ln.Close()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			case <-s.shutdown:
				s.wg.Wait()
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return nil
			}
			s.log.Warn("accept failed", "err", err)
			continue
		}
		s.connMu.Lock()
		if s.conns == nil {
			s.conns = map[net.Conn]struct{}{}
		}
		s.conns[conn] = struct{}{}
		s.connMu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.connMu.Lock()
				delete(s.conns, conn)
				s.connMu.Unlock()
			}()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if s.authz != nil {
		peer, err := PeerOf(conn)
		if err != nil {
			s.log.Warn("could not read peer credentials; rejecting", "err", err)
			_ = WriteFrame(conn, Response{Protocol: ProtocolVersion, Error: "peer verification failed"})
			return
		}
		if err := s.authz(peer); err != nil {
			s.log.Warn("rejected connection", "uid", peer.UID, "pid", peer.PID, "err", err)
			_ = WriteFrame(conn, Response{Protocol: ProtocolVersion, Error: err.Error()})
			return
		}
	}

	// A client that connects and stalls must not pin a goroutine forever.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	for {
		var req Request
		if err := ReadFrame(conn, &req); err != nil {
			return // includes clean EOF
		}
		// A request is in hand; give the handler room to work. The idle
		// deadline above must not also cap dispatch: the CLI deliberately
		// waits 60s for sync and import scans, and a 30s ceiling here made
		// the reply fail after the daemon had already mutated state, so the
		// user saw an error for work that succeeded.
		_ = conn.SetDeadline(time.Now().Add(dispatchDeadline))
		resp := s.dispatch(ctx, req)
		if err := WriteFrame(conn, resp); err != nil {
			return
		}
		// Connections are usually one-shot, but the GUI holds one open to
		// poll status; extend the deadline after each exchange.
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
}

// dispatchDeadline bounds one request's handling and reply. It must exceed
// the longest client-side budget (the CLI grants sync and import 60s) or the
// server gives up on a reply the client is still happily waiting for.
const dispatchDeadline = 90 * time.Second

// dispatch's result is named so the panic-recovery deferred function below
// actually delivers its message: with an unnamed result, a panic returned
// the zero Response and the client saw a blank "request failed".
func (s *Server) dispatch(ctx context.Context, req Request) (resp Response) {
	resp = Response{Protocol: ProtocolVersion}

	if req.Protocol != 0 && req.Protocol != ProtocolVersion {
		resp.Error = fmt.Sprintf(
			"protocol mismatch: client speaks v%d, this daemon speaks v%d; update both to the same release",
			req.Protocol, ProtocolVersion)
		return resp
	}

	// A panicking handler must not take the daemon down: it holds sleep
	// assertions that would be stranded.
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("handler panicked", "op", req.Op, "panic", r)
			resp.OK = false
			resp.Error = fmt.Sprintf("internal error handling %s", req.Op)
		}
	}()

	out, err := s.handler(ctx, req.Op, req.Payload)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	if out != nil {
		b, err := json.Marshal(out)
		if err != nil {
			resp.Error = "encoding response: " + err.Error()
			return resp
		}
		resp.Payload = b
	}
	resp.OK = true
	return resp
}

// Close stops the listener and severs every accepted connection.
//
// The socket file is NOT removed here: closing a net.Listen unix listener
// already unlinks its own path, and an unconditional extra remove could
// delete a replacement daemon's freshly bound socket during an overlapping
// restart, leaving the new daemon serving an unlinked inode nobody can
// reach. A path a crash leaves behind is handled by removeStaleSocket on
// the next start.
func (s *Server) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.shutdown)
	err := s.ln.Close()
	s.connMu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.connMu.Unlock()
	return err
}
