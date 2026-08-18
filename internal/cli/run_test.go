package cli

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/ipc"
)

func TestSplitRunArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantLabel string
		wantCmd   []string
		wantErr   string
	}{
		{
			name: "the plain form", args: []string{"--", "make", "release"},
			wantLabel: "make", wantCmd: []string{"make", "release"},
		},
		{
			// The case the whole feature exists for. `-p` belongs to the agent
			// and must never be read as one of goguma's own flags.
			name:      "flags after the separator belong to the command",
			args:      []string{"--", "claude", "-p", "refactor the auth module"},
			wantLabel: "claude",
			wantCmd:   []string{"claude", "-p", "refactor the auth module"},
		},
		{
			name: "an explicit label", args: []string{"--label", "nightly", "--", "rsync", "-a", "x", "y"},
			wantLabel: "nightly", wantCmd: []string{"rsync", "-a", "x", "y"},
		},
		{
			name: "the equals form", args: []string{"--label=nightly", "--", "sh", "-c", "true"},
			wantLabel: "nightly", wantCmd: []string{"sh", "-c", "true"},
		},
		{
			// A command whose own flags include --help must still reach it.
			name:      "a --help after the separator is the command's",
			args:      []string{"--", "mycmd", "--help"},
			wantLabel: "mycmd", wantCmd: []string{"mycmd", "--help"},
		},
		{
			name: "no separator", args: []string{"make", "release"},
			wantErr: "'--'",
		},
		{
			name: "nothing to run", args: []string{"--"},
			wantErr: "nothing to run",
		},
		{
			name: "a label with no value", args: []string{"--label", "--", "make"},
			wantErr: "--label needs a name",
		},
		{
			name: "an unknown option before the separator", args: []string{"--quiet", "--", "make"},
			wantErr: "unknown option",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			label, cmd, err := splitRunArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("wanted an error containing %q, got none", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if label != tc.wantLabel {
				t.Errorf("label = %q, want %q", label, tc.wantLabel)
			}
			if !reflect.DeepEqual(cmd, tc.wantCmd) {
				t.Errorf("command = %q, want %q", cmd, tc.wantCmd)
			}
		})
	}
}

// TestRunPassesTheExitStatusThrough is what makes this usable in a script.
// `goguma run -- make && deploy` has to behave exactly as `make && deploy`
// would, and something checking for a particular status has to see it.
func TestRunPassesTheExitStatusThrough(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	ctx := newContext()

	for _, want := range []int{0, 1, 7, 42} {
		// holding=false: this exercises the wrapper with no daemon, which is
		// also the degraded path a user gets when the service is down. The
		// command must still run and still report faithfully.
		code, err := runChild(ctx, []string{"sh", "-c", "exit " + itoa(want)}, ipc.RunStartResp{}, false)
		if err != nil {
			t.Fatalf("exit %d: %v", want, err)
		}
		if code != want {
			t.Errorf("exit status = %d, want %d", code, want)
		}
	}
}

// TestRunReportsASignalledCommandTheWayAShellDoes: a process killed by a signal
// reports -1 from ExitCode, which is not a status anything can use. Shells
// report 128+signal, and a wrapper should not invent a different convention.
func TestRunReportsASignalledCommandTheWayAShellDoes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	ctx := newContext()
	code, err := runChild(ctx, []string{"sh", "-c", "kill -TERM $$"}, ipc.RunStartResp{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != 143 { // 128 + SIGTERM(15)
		t.Errorf("exit status = %d, want 143 for a SIGTERM-killed command", code)
	}
	if code < 0 {
		t.Error("a negative status reached the caller")
	}
}

// TestRunReportsACommandThatDoesNotExist rather than pretending it ran.
func TestRunReportsACommandThatDoesNotExist(t *testing.T) {
	ctx := newContext()
	_, err := runChild(ctx, []string{"goguma-no-such-command-xyzzy"}, ipc.RunStartResp{}, false)
	if err == nil {
		t.Fatal("a missing command reported success")
	}
	if !strings.Contains(err.Error(), "goguma-no-such-command-xyzzy") {
		t.Errorf("error does not name the command: %v", err)
	}
}

// TestExitCodeIsCarriedOutThroughMain: Main only distinguishes success from
// failure, so the status travels as a typed error. If that link breaks, every
// non-zero status silently becomes 1.
func TestExitCodeIsCarriedOutThroughMain(t *testing.T) {
	code, ok := ExitCode(exitWith{code: 7})
	if !ok || code != 7 {
		t.Errorf("ExitCode(exitWith{7}) = %d, %v; want 7, true", code, ok)
	}
	if _, ok := ExitCode(errString("something else")); ok {
		t.Error("an ordinary error was read as carrying an exit status")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
