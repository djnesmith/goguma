package detect

import (
	"os"
	"testing"
)

func procs(cmds ...string) []Process {
	out := make([]Process, 0, len(cmds))
	for i, c := range cmds {
		out = append(out, Process{PID: 1000 + i, Command: c})
	}
	return out
}

func TestMatcherFindsTargets(t *testing.T) {
	m, err := Compile("hermes.*morning-briefing")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Find(procs(
		"hermes cron run morning-briefing",
		"hermes cron run evening-digest",
		"/usr/bin/unrelated",
	))
	if len(got) != 1 || got[0].Command != "hermes cron run morning-briefing" {
		t.Errorf("Find matched %+v, want only the morning-briefing process", got)
	}
}

func TestGogumaProcessesAreNeverMatched(t *testing.T) {
	// Without this guard, `goguma add --match backup` self-matches the
	// moment the user runs `goguma list` in a terminal: the registered
	// pattern is stored in jobs.json and appears in the daemon's own command
	// line. The daemon would conclude the backup job is running and hold the
	// machine awake indefinitely.
	m, err := Compile("backup")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Find(procs(
		"/usr/local/bin/goguma-daemon --log /x/daemon.log",
		"/usr/local/libexec/goguma-helper --owner-uid 501",
		"goguma-mark backup -- restic backup /home",
		"/Users/x/.local/bin/goguma list",
		"goguma status",
	))
	if len(got) != 0 {
		t.Errorf("matched goguma's own processes: %+v", got)
	}
}

func TestTheRealJobUnderTheWrapperStillMatches(t *testing.T) {
	// The wrapper itself is excluded, but the wrapped command runs as its own
	// process and must remain visible; otherwise a job configured for
	// pattern detection would become undetectable simply by being wrapped.
	m, err := Compile("restic backup")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Find(procs(
		"goguma-mark nightly -- restic backup /home",
		"restic backup /home",
	))
	if len(got) != 1 || got[0].Command != "restic backup /home" {
		t.Errorf("Find matched %+v, want only the real restic process", got)
	}
}

func TestAJobNamedLikeGogumaIsNotExcluded(t *testing.T) {
	// The guard keys on the program name, not on the substring appearing
	// anywhere. A user's own script that merely mentions goguma must still
	// be matchable.
	m, err := Compile("sync-goguma-config")
	if err != nil {
		t.Fatal(err)
	}
	got := m.Find(procs("/opt/scripts/sync-goguma-config --dry-run"))
	if len(got) != 1 {
		t.Errorf("a user script mentioning goguma was wrongly excluded: %+v", got)
	}
}

func TestSelfPIDIsNeverMatched(t *testing.T) {
	m, err := Compile(".*")
	if err != nil {
		t.Fatal(err)
	}
	self := Process{PID: os.Getpid(), Command: "some-test-binary"}
	if got := m.Find([]Process{self}); len(got) != 0 {
		t.Errorf("matched our own process: %+v", got)
	}
}

func TestCompileRejectsBadPatterns(t *testing.T) {
	for _, p := range []string{"", "   ", "([unclosed"} {
		if _, err := Compile(p); err == nil {
			t.Errorf("Compile(%q) should have failed", p)
		}
	}
}

func TestSuggestPattern(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		// The program plus its distinctive arguments, not the whole command
		// line: absolute paths and redirection differ between how cron
		// invokes a job and how it appears in the process table.
		{"hermes cron run morning-briefing", `hermes.*run.*morning-briefing`},
		{"/usr/local/bin/backup.sh --full > /dev/null", `backup\.sh`},
		{"/bin/sh -c '/opt/thing.sh --now'", `thing\.sh`},
		// Quoted prose is dropped BEFORE segment splitting, so separators
		// inside a prompt cannot smuggle prose words into the pattern.
		{`claude -p "fetch news; summarize top stories"`, `claude`},
		// The last segment of a shell chain is the one that does the work.
		{"cd /backups && restic backup nightly", `restic.*backup.*nightly`},
	}
	for _, tc := range tests {
		got := SuggestPattern(tc.command)
		if got == "" {
			t.Errorf("SuggestPattern(%q) returned nothing", tc.command)
			continue
		}
		// Exact output is asserted deliberately: this table once recorded
		// expectations that had silently drifted from real behavior because
		// nothing read the want field.
		if got != tc.want {
			t.Errorf("SuggestPattern(%q) = %q, want %q", tc.command, got, tc.want)
			continue
		}
		// The suggestion must actually match the command it was derived from,
		// which is the property that matters rather than its exact text.
		m, err := Compile(got)
		if err != nil {
			t.Errorf("SuggestPattern(%q) produced an invalid regexp %q: %v", tc.command, got, err)
			continue
		}
		if len(m.Find(procs(tc.command))) != 1 {
			t.Errorf("SuggestPattern(%q) = %q, which does not match its own command",
				tc.command, got)
		}
	}
}

func TestSuggestPatternEscapesRegexpMetacharacters(t *testing.T) {
	// A command containing regexp metacharacters must not produce a pattern
	// that silently matches something else, or fails to compile at all.
	got := SuggestPattern("/opt/run+thing.sh --a[b]")
	m, err := Compile(got)
	if err != nil {
		t.Fatalf("SuggestPattern produced an invalid regexp %q: %v", got, err)
	}
	if len(m.Find(procs("/opt/run+thing.sh --a[b]"))) != 1 {
		t.Errorf("pattern %q does not match the literal command it came from", got)
	}
}

func TestStripShellWrapping(t *testing.T) {
	tests := []struct{ in, want string }{
		{`/bin/sh -c "/opt/backup.sh --full"`, "/opt/backup.sh --full"},
		{`/bin/bash -c '/opt/x.sh'`, "/opt/x.sh"},
		{"restic backup /home", "restic backup /home"},
	}
	for _, tc := range tests {
		if got := stripShellWrapping(tc.in); got != tc.want {
			t.Errorf("stripShellWrapping(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAliveRejectsNonsensePIDs(t *testing.T) {
	if Alive(0) || Alive(-1) {
		t.Error("Alive should reject non-positive pids")
	}
	if !Alive(os.Getpid()) {
		t.Error("Alive should report the current process as running")
	}
}
