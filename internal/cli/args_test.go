package cli

import (
	"slices"
	"testing"
)

// TestFlagsParseAfterTheSubject guards a silent failure: Go's flag package
// stops at the first positional, so `history <job> --json` dropped --json and
// printed a human table to a caller that asked for JSON.
func TestFlagsParseAfterTheSubject(t *testing.T) {
	tests := []struct {
		args     []string
		wantRef  string
		wantRest []string
	}{
		{[]string{"morning-briefing", "--json"}, "morning-briefing", []string{"--json"}},
		{[]string{"--json", "morning-briefing"}, "morning-briefing", []string{"--json"}},
		{[]string{"job", "--limit", "50"}, "job", []string{"--limit", "50"}},
		{[]string{"job", "--limit=50"}, "job", []string{"--limit=50"}},
		{[]string{"job", "--limit", "50", "--json"}, "job", []string{"--limit", "50", "--json"}},
		{[]string{"job"}, "job", nil},
		{nil, "", nil},
	}
	for _, tc := range tests {
		ref, rest := splitRef(tc.args)
		if ref != tc.wantRef {
			t.Errorf("splitRef(%v) ref = %q, want %q", tc.args, ref, tc.wantRef)
		}
		if !slices.Equal(rest, tc.wantRest) {
			t.Errorf("splitRef(%v) rest = %v, want %v", tc.args, rest, tc.wantRest)
		}
	}
}

func TestBooleanFlagDoesNotSwallowTheSubject(t *testing.T) {
	// If --json were assumed to take a value it would consume the job name,
	// and the command would report that no job was given.
	ref, rest := splitRef([]string{"--json", "nightly-backup"})
	if ref != "nightly-backup" {
		t.Errorf("ref = %q, want the job name; a boolean flag ate it", ref)
	}
	if !slices.Equal(rest, []string{"--json"}) {
		t.Errorf("rest = %v, want just the flag", rest)
	}
}

func TestDoubleDashPassesEverythingThrough(t *testing.T) {
	// This is what lets a wrapped command carry its own flags without them
	// being reinterpreted.
	flags, positional := hoistFlags([]string{"--json", "job", "--", "restic", "-v", "backup"})
	if !slices.Equal(flags, []string{"--json"}) {
		t.Errorf("flags = %v, want only the pre-dash flag", flags)
	}
	if !slices.Equal(positional, []string{"job", "restic", "-v", "backup"}) {
		t.Errorf("positional = %v, want the subject plus the untouched command", positional)
	}
}
