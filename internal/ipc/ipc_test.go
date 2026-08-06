package ipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var buf bytes.Buffer
	want := payload{Name: "morning-briefing", Count: 42}
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatal(err)
	}
	var got payload
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip gave %+v, want %+v", got, want)
	}
}

func TestFrameRejectsAnOversizedAnnouncement(t *testing.T) {
	// A corrupt or hostile length prefix must not make the peer allocate
	// unbounded memory. This is the only thing standing between a garbled byte
	// and a multi-gigabyte allocation in a long-running daemon.
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 0xFFFFFFFF)
	buf.Write(hdr[:])

	var out map[string]any
	err := ReadFrame(&buf, &out)
	if err == nil {
		t.Fatal("a 4GB frame announcement was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error %q should mention the limit", err)
	}
}

func TestFrameRejectsAnOversizedWrite(t *testing.T) {
	// The same bound applies outbound, so a bug elsewhere cannot emit a frame
	// the peer is required to reject.
	huge := strings.Repeat("x", maxFrame+1)
	if err := WriteFrame(io.Discard, map[string]string{"v": huge}); err == nil {
		t.Fatal("an oversized frame was written")
	}
}

func TestTruncatedFrameIsAnError(t *testing.T) {
	// A connection dropped mid-message must surface as an error rather than a
	// half-decoded struct that looks plausible.
	var buf bytes.Buffer
	if err := WriteFrame(&buf, map[string]string{"job": "backup"}); err != nil {
		t.Fatal(err)
	}
	truncated := bytes.NewReader(buf.Bytes()[:buf.Len()-3])

	var out map[string]string
	if err := ReadFrame(truncated, &out); err == nil {
		t.Fatal("a truncated frame decoded successfully")
	}
}

// TestSocketPathLengthIsCheckedUpFront guards a failure that is otherwise
// baffling: sockaddr_un.sun_path is a fixed 104-byte buffer on macOS, and
// exceeding it fails with a bare EINVAL that says nothing about the cause.
// It is reachable through a long username or a state-dir override.
func TestSocketPathLengthIsCheckedUpFront(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120), "daemon.sock")
	_, err := Listen(long, 0o600, func(context.Context, Op, json.RawMessage) (any, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("an over-long socket path was accepted")
	}
	if !strings.Contains(err.Error(), "byte") {
		t.Errorf("error %q should explain the length limit", err)
	}
}

func TestStaleSocketIsReplacedButALiveOneIsNot(t *testing.T) {
	// A short path on purpose: macOS temp directories are long enough to trip
	// the socket-path limit, which is itself evidence the guard is reachable.
	dir := shortTempDir(t)
	path := filepath.Join(dir, "d.sock")
	handler := func(context.Context, Op, json.RawMessage) (any, error) { return nil, nil }

	// A daemon killed with SIGKILL leaves the socket file behind. Refusing to
	// start because of it would mean one crash disables WakeGuard until
	// someone cleans up by hand.
	srv, err := Listen(path, 0o600, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)

	// While it IS live, a second listener must refuse rather than steal it.
	if _, err := Listen(path, 0o600, handler); err == nil {
		t.Error("a second daemon stole a live socket")
	}

	cancel()
	srv.Close()
	time.Sleep(50 * time.Millisecond)

	// Simulate the SIGKILL case: the file exists, nothing is listening.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path, 0o600, handler); err == nil {
		t.Error("a plain file at the socket path should be refused, not replaced")
	}
}

func TestServerRejectsAProtocolMismatch(t *testing.T) {
	// The menu bar app ships independently of the daemon, so a version skew is
	// expected. It must be reported plainly rather than misrendering.
	srv, path := serveTest(t, func(_ context.Context, op Op, _ json.RawMessage) (any, error) {
		return map[string]string{"op": string(op)}, nil
	})
	defer srv.Close()

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := WriteFrame(conn, Request{Protocol: 999, Op: OpPing}); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := ReadFrame(conn, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("a mismatched protocol version was accepted")
	}
	if !strings.Contains(resp.Error, "protocol") {
		t.Errorf("error %q should name the protocol mismatch", resp.Error)
	}
}

// TestPanickingHandlerDoesNotKillTheDaemon matters more than it looks: the
// daemon holds sleep assertions, and a crash would strand them.
func TestPanickingHandlerDoesNotKillTheDaemon(t *testing.T) {
	srv, path := serveTest(t, func(_ context.Context, op Op, _ json.RawMessage) (any, error) {
		if op == OpStatus {
			panic("handler exploded")
		}
		return map[string]string{"ok": "yes"}, nil
	})
	defer srv.Close()

	if err := Do(path, OpStatus, nil, nil); err == nil {
		t.Error("a panicking handler reported success")
	}
	// The server must still be answering afterwards.
	var out map[string]string
	if err := Do(path, OpPing, nil, &out); err != nil {
		t.Fatalf("the server died after a handler panic: %v", err)
	}
}

func TestHandlerErrorsReachTheCaller(t *testing.T) {
	srv, path := serveTest(t, func(context.Context, Op, json.RawMessage) (any, error) {
		return nil, errors.New("no job named \"nope\"")
	})
	defer srv.Close()

	err := Do(path, OpJobsRemove, JobRef{Ref: "nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want the handler's message preserved", err)
	}
}

func TestDialingNothingReportsNotRunning(t *testing.T) {
	// Callers distinguish this from other failures to give actionable advice,
	// so the sentinel must survive wrapping.
	err := Do(filepath.Join(t.TempDir(), "absent.sock"), OpPing, nil, nil)
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want it to wrap ErrNotRunning", err)
	}
	// It must also name which socket, so a daemon logging a failed helper call
	// does not appear to be reporting itself as down.
	if !strings.Contains(err.Error(), "absent.sock") {
		t.Errorf("error %q should name the socket it could not reach", err)
	}
}

// TestAuthzIsEnforced covers the helper's trust boundary. The privileged
// helper exposes an operation that can pin a lid-closed laptop awake in a bag,
// so any other local user must not reach it.
func TestAuthzIsEnforced(t *testing.T) {
	me := uint32(os.Getuid())
	allow := AllowOwnerOrRoot(me)

	if err := allow(Peer{UID: me}); err != nil {
		t.Errorf("the owning user was rejected: %v", err)
	}
	if err := allow(Peer{UID: 0}); err != nil {
		t.Errorf("root was rejected: %v", err)
	}
	other := me + 1
	if err := allow(Peer{UID: other}); err == nil {
		t.Errorf("uid %d was allowed to control the helper", other)
	} else if !strings.Contains(err.Error(), "authorized") {
		t.Errorf("rejection %q should say why", err)
	}
}

func TestAuthzRejectionClosesTheConnection(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "d.sock")
	srv, err := Listen(path, 0o600,
		func(context.Context, Op, json.RawMessage) (any, error) {
			t.Error("the handler ran despite authorization failing")
			return nil, nil
		},
		// Reject everyone, including us.
		WithAuthz(func(Peer) error { return errors.New("denied for the test") }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)

	if err := Do(path, OpStatus, nil, nil); err == nil {
		t.Fatal("a rejected peer got a successful response")
	}
}

func TestConcurrentCallsAreServed(t *testing.T) {
	var mu sync.Mutex
	seen := 0
	srv, path := serveTest(t, func(context.Context, Op, json.RawMessage) (any, error) {
		mu.Lock()
		seen++
		n := seen // read under the lock; the handler runs on many goroutines
		mu.Unlock()
		return map[string]int{"n": n}, nil
	})
	defer srv.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Do(path, OpPing, nil, nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call failed: %v", err)
	}
	mu.Lock()
	total := seen
	mu.Unlock()
	if total != 20 {
		t.Errorf("handler ran %d times, want 20", total)
	}
}

// shortTempDir returns a directory short enough for a unix socket path.
//
// t.TempDir() on macOS yields something like
// /var/folders/g3/.../T/TestName123456789/001 — over 100 bytes before the
// socket name is appended, which the length guard correctly refuses.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wgt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// serveTest starts a server on a short temp path and returns it.
func serveTest(t *testing.T, h Handler) (*Server, string) {
	t.Helper()
	path := filepath.Join(shortTempDir(t), "s.sock")
	srv, err := Listen(path, 0o600, h)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)
	return srv, path
}
