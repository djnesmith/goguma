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
