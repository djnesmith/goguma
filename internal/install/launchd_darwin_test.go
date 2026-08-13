package install

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/junnam586/goguma/internal/paths"
)

// parsePlist round-trips a plist through the system parser.
//
// The point is not to re-implement plist parsing but to fail the way launchd
// will. A malformed plist is not rejected loudly: launchctl declines to load it
// and the service simply never starts, which surfaces days later as "goguma
// isn't running" with nothing obviously wrong.
//
// plutil ships with macOS, so this needs nothing installed.
func parsePlist(t *testing.T, text string) map[string]string {
	t.Helper()

	cmd := exec.Command("plutil", "-lint", "-s", "-")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plutil rejects this plist (%v): %s\n%s", err, out, text)
	}

	// Read back the values that matter, each via plutil so the test agrees with
	// the parser launchd uses rather than with a regex over the source.
	out := map[string]string{}
	for _, key := range []string{
		"Label", "RunAtLoad", "KeepAlive", "ProcessType", "StandardErrorPath",
	} {
		c := exec.Command("plutil", "-extract", key, "raw", "-o", "-", "-")
		c.Stdin = strings.NewReader(text)
		if v, err := c.Output(); err == nil {
			out[key] = strings.TrimSpace(string(v))
		}
	}

	// ProgramArguments is an array, and `-extract ... raw` on an array yields
	// its element count rather than its contents, so each index is read out and
	// joined. Asserting against "3" is how this test first passed a lie.
	var args []string
	for i := 0; ; i++ {
		c := exec.Command("plutil", "-extract",
			fmt.Sprintf("ProgramArguments.%d", i), "raw", "-o", "-", "-")
		c.Stdin = strings.NewReader(text)
		v, err := c.Output()
		if err != nil {
			break
		}
		args = append(args, strings.TrimSpace(string(v)))
	}
	out["ProgramArguments"] = strings.Join(args, " ")
	return out
}

func TestDaemonPlistIsLoadable(t *testing.T) {
	got := parsePlist(t, daemonPlist(paths.DaemonLabel, "/Users/u/.local/bin/goguma-daemon", "/Users/u/Library/Logs/goguma"))

	if got["Label"] != paths.DaemonLabel {
		t.Errorf("Label = %q, want %q; launchctl addresses the job by this and "+
			"uninstall boots out that exact string", got["Label"], paths.DaemonLabel)
	}
	// RunAtLoad is what starts the daemon at login. Without it the install
	// succeeds, nothing runs, and the machine quietly stops waking.
	if got["RunAtLoad"] != "true" {
		t.Errorf("RunAtLoad = %q, want true", got["RunAtLoad"])
	}
	if got["ProcessType"] != "Background" {
		t.Errorf("ProcessType = %q, want Background", got["ProcessType"])
	}
	if !strings.Contains(got["ProgramArguments"], "goguma-daemon") {
		t.Errorf("ProgramArguments = %q, want the daemon binary", got["ProgramArguments"])
	}
}

// TestDaemonPlistKeepAliveOnlyRestartsFailures pins the shape of KeepAlive.
//
// A bare <true/> would restart the daemon whatever happened to it, including
// the deliberate stop uninstall performs, so uninstall would race a service
// that keeps coming back. The dictionary form restarts only on unsuccessful
// exit.
func TestDaemonPlistKeepAliveOnlyRestartsFailures(t *testing.T) {
	text := daemonPlist(paths.DaemonLabel, "/bin/true", "/tmp")
	parsePlist(t, text) // still has to be a valid plist

	if !strings.Contains(text, "SuccessfulExit") {
		t.Error("KeepAlive has no SuccessfulExit key, so a deliberate stop " +
			"during uninstall would be undone by launchd restarting it")
	}
}

func TestHelperPlistCarriesItsOwner(t *testing.T) {
	const uid = 501
	got := parsePlist(t, helperPlist(paths.HelperLabel, "/usr/local/libexec/goguma-helper", uid))

	if got["Label"] != paths.HelperLabel {
		t.Errorf("Label = %q, want %q", got["Label"], paths.HelperLabel)
	}
	// The helper only accepts requests from the uid it was installed for, so
	// losing this argument makes it refuse the daemon it exists to serve.
	if !strings.Contains(got["ProgramArguments"], "--owner-uid") ||
		!strings.Contains(got["ProgramArguments"], "501") {
		t.Errorf("ProgramArguments = %q, want --owner-uid %d", got["ProgramArguments"], uid)
	}
	// Unlike the daemon, the helper should come back from anything: it is what
	// clears a sleep block stranded by a crash, and a machine left blocked
	// never sleeps again until someone notices.
	if got["KeepAlive"] != "true" {
		t.Errorf("KeepAlive = %q, want true", got["KeepAlive"])
	}
}

// TestPlistsSurviveAwkwardPaths is the injection check.
//
// Both plists are built with Sprintf into XML. A home directory containing an
// ampersand or a quote is ordinary, and unescaped it produces a document that
// plutil rejects — which means an install that reports success and a service
// that never loads.
func TestPlistsSurviveAwkwardPaths(t *testing.T) {
	for _, path := range []string{
		"/Users/a b/bin/goguma-daemon",
		"/Users/tom&jerry/bin/goguma-daemon",
		`/Users/o'brien/bin/goguma-daemon`,
		"/Users/<script>/bin/goguma-daemon",
	} {
		t.Run(path, func(t *testing.T) {
			cmd := exec.Command("plutil", "-lint", "-s", "-")
			cmd.Stdin = strings.NewReader(daemonPlist(paths.DaemonLabel, path, "/tmp/logs"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("a home directory of this shape produces a plist launchd "+
					"cannot load: %v: %s", err, out)
			}
		})
	}
}
