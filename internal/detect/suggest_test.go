package detect

import "testing"

// TestSuggestPatternUsesTheDistinguishingToken guards the failure that made
// every job of a given runner share one pattern.
//
// Task runners put the job's identity last — `hermes cron run <job>`,
// `npm run <script>`, `make <target>`. Anchoring on the first arguments gave
// `hermes.*cron.*run` for every hermes job, so each job's pattern matched all
// of its siblings.
func TestSuggestPatternUsesTheDistinguishingToken(t *testing.T) {
	cases := []struct{ command, want string }{
		{"hermes cron run gmail-meeting-calendar-sync", `hermes.*run.*gmail-meeting-calendar-sync`},
		{"hermes cron run imessage-calendar-sync", `hermes.*run.*imessage-calendar-sync`},
		{"npm run nightly-report", `npm.*run.*nightly-report`},
		{"make backup", `make.*backup`},
	}
	for _, c := range cases {
		if got := SuggestPattern(c.command); got != c.want {
			t.Errorf("SuggestPattern(%q)\n got  %q\n want %q", c.command, got, c.want)
		}
	}
}

// The whole point: sibling jobs must not collide.
func TestSiblingJobsGetDifferentPatterns(t *testing.T) {
	a := "hermes cron run gmail-meeting-calendar-sync"
	b := "hermes cron run imessage-calendar-sync"
	all := []string{a, b}

	pa, pb := SuggestPattern(a), SuggestPattern(b)
	if pa == pb {
		t.Fatalf("siblings share a pattern: %q", pa)
	}
	if !Distinctive(pa, all) {
		t.Errorf("pattern %q for %q is not distinctive among siblings", pa, a)
	}
	if !Distinctive(pb, all) {
		t.Errorf("pattern %q for %q is not distinctive among siblings", pb, b)
	}
}

// Prompt prose must never become a pattern, and the result must be refused
// rather than silently matching every invocation of the program.
func TestPromptTextIsNotScrapedIntoAPattern(t *testing.T) {
	for _, cmd := range []string{
		`claude -p "summarise my unread email"`,
		`claude -p "daily briefing" --output-format json`,
		`cd ~/notes && claude -p "weekly review"`,
		// A flag value is not an identity either: this used to yield
		// "claude.*inbox-triage", which reads distinctive but is still a flag
		// value, and the same reasoning that rejects "claude.*json" applies.
		`claude --agent inbox-triage`,
	} {
		got := SuggestPattern(cmd)
		if got != "claude" {
			t.Errorf("SuggestPattern(%q) = %q, want bare %q (prose dropped)", cmd, got, "claude")
		}
		// Bare "claude" matches an interactive session too, so it must be
		// refused when anything else on the machine looks like claude.
		others := []string{cmd, "/path/to/claude --output-format stream-json --resume=abc"}
		if Distinctive(got, others) {
			t.Errorf("pattern %q was accepted despite matching an interactive session", got)
		}
	}
}

// `cd x && real` must anchor on the real program, not on `cd`.
func TestShellChainAnchorsOnTheRealProgram(t *testing.T) {
	if got := SuggestPattern("cd /srv && ./backup.sh nightly"); got != `backup\.sh.*nightly` {
		t.Errorf("got %q", got)
	}
	if got := SuggestPattern("/bin/sh -c 'rsync -a /a /b'"); got != "rsync" {
		t.Errorf("shell wrapping: got %q", got)
	}
}

func TestDistinctiveRejectsNothingAndGarbage(t *testing.T) {
	if Distinctive("", []string{"a"}) {
		t.Error("empty pattern must never be usable")
	}
	if Distinctive("([", []string{"a"}) {
		t.Error("uncompilable pattern must never be usable")
	}
	if Distinctive("x", nil) {
		t.Error("a pattern matching nothing is not distinctive")
	}
}
