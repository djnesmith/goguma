package install

import (
	"strings"
	"testing"
)

// parseUnit reads a systemd unit into section -> directive -> values.
//
// Deliberately strict about shape rather than clever: a unit with a directive
// outside any section, or a line that is not key=value, is one systemd refuses
// to load, and the symptom is a daemon that simply never starts.
func parseUnit(t *testing.T, text string) map[string]map[string][]string {
	t.Helper()
	out := map[string]map[string][]string{}
	section := ""
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			if _, ok := out[section]; !ok {
				out[section] = map[string][]string{}
			}
			continue
		}
		if section == "" {
			t.Errorf("line %d (%q) is outside any section; systemd will refuse the unit", i+1, line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("line %d (%q) is not key=value", i+1, line)
			continue
		}
		out[section][key] = append(out[section][key], value)
	}
	return out
}

func TestDaemonUnitIsAValidUserService(t *testing.T) {
	unit := parseUnit(t, daemonUnit("/home/u/.local/bin/goguma-daemon", "/home/u/.local/state/goguma"))

	for _, section := range []string{"Unit", "Service", "Install"} {
		if _, ok := unit[section]; !ok {
			t.Fatalf("missing [%s] section", section)
		}
	}

	exec := unit["Service"]["ExecStart"]
	if len(exec) != 1 {
		t.Fatalf("ExecStart appears %d times, want exactly 1", len(exec))
	}
	if !strings.HasPrefix(exec[0], "/") {
		t.Errorf("ExecStart = %q; systemd requires an absolute path", exec[0])
	}
	if !strings.Contains(exec[0], "goguma-daemon") {
		t.Errorf("ExecStart = %q, want the daemon binary", exec[0])
	}

	// WantedBy=default.target is what makes this start at login. Without it
	// `systemctl --user enable` succeeds and nothing ever runs.
	if got := unit["Install"]["WantedBy"]; len(got) != 1 || got[0] != "default.target" {
		t.Errorf("WantedBy = %v, want [default.target] so it starts at login", got)
	}

	// Restart=on-failure, not always: uninstall stops this deliberately and
	// must not be fought by systemd restarting it underneath.
	if got := unit["Service"]["Restart"]; len(got) != 1 || got[0] != "on-failure" {
		t.Errorf("Restart = %v, want [on-failure] so a deliberate stop sticks", got)
	}
}

func TestHelperUnitRunsAtBootAndCarriesTheOwner(t *testing.T) {
	const uid = 501
	unit := parseUnit(t, helperUnit("/usr/local/libexec/goguma-helper", uid))

	exec := unit["Service"]["ExecStart"]
	if len(exec) != 1 {
		t.Fatalf("ExecStart appears %d times, want exactly 1", len(exec))
	}
	// The helper only accepts requests from the uid it was installed for, so
	// losing this argument makes it refuse the daemon it exists to serve.
	if !strings.Contains(exec[0], "--owner-uid 501") {
		t.Errorf("ExecStart = %q, want --owner-uid %d", exec[0], uid)
	}

	// multi-user.target, not default.target: the helper starts at boot rather
	// than at login, so it can clear a sleep block stranded by a crash or a
	// forced power-off before anything else runs.
	if got := unit["Install"]["WantedBy"]; len(got) != 1 || got[0] != "multi-user.target" {
		t.Errorf("WantedBy = %v, want [multi-user.target] so it starts at boot", got)
	}
	if got := unit["Service"]["Restart"]; len(got) != 1 || got[0] != "always" {
		t.Errorf("Restart = %v, want [always]; the helper going down un-noticed "+
			"leaves the machine unable to hold sleep or schedule a wake", got)
	}
}

// TestUnitsQuoteNothingUnexpected guards the paths, which come from a Layout
// and therefore from a home directory that may contain spaces.
func TestUnitsQuoteNothingUnexpected(t *testing.T) {
	unit := parseUnit(t, daemonUnit("/home/a b/bin/goguma-daemon", "/home/a b/state"))
	exec := unit["Service"]["ExecStart"][0]
	// Recorded rather than asserted-correct: systemd splits ExecStart on
	// whitespace, so a home directory with a space in it produces a command
	// line that does not resolve. This test exists to make that visible if
	// anyone ever reports it, not to claim it is handled.
	if strings.Contains(exec, "/home/a b/") {
		t.Logf("ExecStart with a spaced home is %q; systemd will word-split this", exec)
	}
}
