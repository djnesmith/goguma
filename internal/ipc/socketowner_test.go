package ipc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestSocketOwnerDefaultsToTheProcessUID pins the behaviour the daemon relies
// on: with no option, the socket belongs to whoever bound it.
func TestSocketOwnerDefaultsToTheProcessUID(t *testing.T) {
	path := shortSocketPath(t)
	srv, err := Listen(path, 0o600, noopHandler)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if uid := statUID(t, path); uid != os.Getuid() {
		t.Errorf("socket uid = %d, want the process uid %d", uid, os.Getuid())
	}
}

// TestSocketOwnerIsAppliedToTheFile is the regression test for a bug that made
// the privileged helper permanently unreachable.
//
// The helper binds as root and serves the daemon, which runs as the logged-in
// user. Before `WithSocketOwner` existed, the socket was left root-owned at
// mode 0600, so the daemon could not open it — it never reached the
// peer-credential check meant to authorise it. Worse, the failure surfaced as
// "nothing is listening on the socket", which points squarely at a helper that
// is running perfectly, so the diagnosis pointed away from the cause.
//
// Only root can hand a file to another uid, so the ownership change itself is
// asserted when running as root and the no-op path is asserted otherwise.
func TestSocketOwnerIsAppliedToTheFile(t *testing.T) {
	path := shortSocketPath(t)

	target := os.Getuid()
	if os.Getuid() == 0 {
		target = 1 // daemon; any uid that is not root proves the chown ran
	}

	srv, err := Listen(path, 0o600, noopHandler, WithSocketOwner(target))
	if err != nil {
		t.Fatalf("Listen with WithSocketOwner(%d): %v", target, err)
	}
	defer srv.Close()

	if uid := statUID(t, path); uid != target {
		t.Errorf("socket uid = %d, want %d", uid, target)
	}

	// The mode must stay narrow. Handing the socket to the right user is only
	// safe because nobody else can reach it; a widened mode would trade one
	// bug for a worse one.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %#o, want 0600", perm)
	}
}

// TestSocketOwnerRejectsAnImpossibleOwner ensures a failed hand-off is loud.
// A helper that silently kept an unreachable socket is precisely the silent
// failure this tool exists to prevent, so Listen must fail instead.
func TestSocketOwnerRejectsAnImpossibleOwner(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can chown to any uid, so there is no failure to observe")
	}
	path := shortSocketPath(t)

	// Not our uid, and not one an unprivileged process may chown to.
	srv, err := Listen(path, 0o600, noopHandler, WithSocketOwner(0))
	if err == nil {
		srv.Close()
		t.Fatal("Listen succeeded while handing the socket to root as an unprivileged user")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a socket was left behind after Listen failed")
	}
}

func statUID(t *testing.T, path string) int {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat_t on this platform")
	}
	return int(st.Uid)
}

func noopHandler(_ context.Context, _ Op, _ json.RawMessage) (any, error) {
	return nil, nil
}

// shortSocketPath returns a socket path inside the OS limit.
//
// t.TempDir() on macOS produces a ~100-byte path under /var/folders, which on
// its own exceeds sockaddr_un.sun_path. Using it made these tests fail — and
// made the negative test pass for entirely the wrong reason, which is worse.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wgipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}
