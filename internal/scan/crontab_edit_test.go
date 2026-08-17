package scan

import "testing"

const crontabSample = `# my jobs
PATH=/usr/local/bin:/usr/bin

0 9 * * * /usr/local/bin/calendar-sync
  30 2 * * * /opt/backup.sh --full
@daily /usr/local/bin/digest
`

// Everything except the one line must survive byte for byte. A crontab holds
// comments, PATH assignments and other people's jobs, and a rewrite that
// reformats any of them is a rewrite nobody should accept.
func TestReplaceLeavesTheRestAlone(t *testing.T) {
	out, err := ReplaceCrontabCommand(crontabSample, 4, "/usr/local/bin/calendar-sync",
		"goguma-mark calendar-sync -- /usr/local/bin/calendar-sync")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	want := `# my jobs
PATH=/usr/local/bin:/usr/bin

0 9 * * * goguma-mark calendar-sync -- /usr/local/bin/calendar-sync
  30 2 * * * /opt/backup.sh --full
@daily /usr/local/bin/digest
`
	if out != want {
		t.Errorf("crontab was rewritten wrong:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// The user's own indentation is theirs to keep.
func TestReplaceKeepsIndentation(t *testing.T) {
	out, err := ReplaceCrontabCommand(crontabSample, 5, "/opt/backup.sh --full",
		"goguma-mark backup -- /opt/backup.sh --full")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !containsSub(out, "  30 2 * * * goguma-mark backup -- /opt/backup.sh --full") {
		t.Errorf("indentation was not preserved:\n%s", out)
	}
}

// The crontab can be edited between the scan and the write. Rewriting whatever
// now sits on that line would clobber a job the user just added, so the old
// command is checked rather than trusted.
func TestReplaceRefusesAChangedLine(t *testing.T) {
	_, err := ReplaceCrontabCommand(crontabSample, 4, "/usr/local/bin/something-else",
		"goguma-mark x -- /usr/local/bin/something-else")
	if err == nil {
		t.Fatal("a line whose command no longer matches was rewritten anyway")
	}
}

func TestReplaceRefusesANonJobLine(t *testing.T) {
	for _, line := range []int{1, 2, 3, 99} {
		if _, err := ReplaceCrontabCommand(crontabSample, line, "x", "y"); err == nil {
			t.Errorf("line %d was accepted as a job", line)
		}
	}
}

func containsSub(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestWrappedLinesAdoptUnderTheirRealName is the duplicate-job bug.
//
// `import --register` rewrites a crontab line to `goguma-mark <job> -- <cmd>`,
// and the next adoption sweep read that back as something it had never seen.
// The line became a second job called "goguma-mark-<job>", scheduled at the
// same minute as the first and taking its own wake and its own hold. The
// duplicate could never be detected either, because the wrapper announces the
// job under its real name, so it accumulated never-detected runs and warned
// that the machine had been woken and held awake for nothing about a job
// goguma had invented for itself.
func TestWrappedLinesAdoptUnderTheirRealName(t *testing.T) {
	for _, tc := range []struct {
		line, want string
	}{
		{"0 3 * * * /Users/x/.local/bin/goguma-mark nightly-backup -- /opt/backup.sh", "nightly-backup"},
		{"0 3 * * * goguma-mark db-dump -- /usr/bin/pg_dump -f /tmp/o.sql", "db-dump"},
		// Unwrapped lines keep deriving a name from the command as before.
		{"0 3 * * * /opt/backup.sh", "backup"},
		// Not the wrapper, just something with a similar shape.
		{"0 3 * * * /usr/bin/other-mark thing -- /opt/x.sh", "other-mark-thing"},
	} {
		got := ParseCrontab(tc.line)
		if len(got) != 1 {
			t.Fatalf("ParseCrontab(%q) returned %d entries", tc.line, len(got))
		}
		if got[0].Name != tc.want {
			t.Errorf("ParseCrontab(%q) named it %q, want %q", tc.line, got[0].Name, tc.want)
		}
	}
}

// TestUnwrapMarkRecoversTheInnerCommand checks the other half: the command a
// wrapped line actually runs, which is what `doctor` and the job list show.
func TestUnwrapMarkRecoversTheInnerCommand(t *testing.T) {
	name, inner, ok := UnwrapMark("/Users/x/.local/bin/goguma-mark nightly-backup -- /opt/backup.sh --full")
	if !ok {
		t.Fatal("a wrapped line was not recognised as wrapped")
	}
	if name != "nightly-backup" {
		t.Errorf("name = %q, want nightly-backup", name)
	}
	if inner != "/opt/backup.sh --full" {
		t.Errorf("inner = %q, want /opt/backup.sh --full", inner)
	}
	if _, _, ok := UnwrapMark("/opt/backup.sh"); ok {
		t.Error("an unwrapped line was reported as wrapped")
	}
}
