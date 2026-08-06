package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNormalizeGroupCollapsesWhitespace guards the failure that makes a flat
// group list useless: two headings that look identical on screen but are
// different strings, so jobs the user filed together are shown apart.
func TestNormalizeGroupCollapsesWhitespace(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Morning jobs", "Morning jobs"},
		{" Morning jobs ", "Morning jobs"},
		{"Morning  jobs", "Morning jobs"},
		{"\tMorning\t\tjobs\n", "Morning jobs"},
		{"Morning   jobs   again", "Morning jobs again"},
		{"   ", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := NormalizeGroup(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeGroup(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Normalising an already-normalised name must not keep changing it,
		// or a job re-saved on every edit would drift.
		if again := NormalizeGroup(got); again != got {
			t.Errorf("NormalizeGroup(%q) is not idempotent: %q then %q", tc.in, got, again)
		}
	}
}

func TestValidateBoundsTheGroupName(t *testing.T) {
	job := func(group string) *Job {
		return &Job{
			ID: "j", Name: "j", Schedule: "@daily",
			Detection: DetectNone, Group: group,
		}
	}

	if err := job("").Validate(); err != nil {
		t.Errorf("an empty group means ungrouped and must always be valid: %v", err)
	}
	if err := job(strings.Repeat("g", MaxGroupLen)).Validate(); err != nil {
		t.Errorf("a %d-character group is at the limit, not over it: %v", MaxGroupLen, err)
	}
	if err := job(strings.Repeat("g", MaxGroupLen+1)).Validate(); err == nil {
		t.Errorf("a %d-character group was accepted", MaxGroupLen+1)
	} else if !strings.Contains(err.Error(), `"j"`) {
		t.Errorf("the error does not name the job, so the user cannot tell which one: %v", err)
	}

	// Validate is the single mutation path, so it is where normalisation has
	// to land: what gets stored must be what later readers group by.
	j := job("  Morning   jobs ")
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	if j.Group != "Morning jobs" {
		t.Errorf("Validate left the group as %q, want it normalised", j.Group)
	}
}

// TestSlugCannotProduceTheReservedID pins the one property that keeps the
// manual keep-awake window from colliding with a real job.
//
// The manual hold lives in the daemon's hold map under KeepAwakeJobID. If a
// user's job could slug to the same id, one of the two would silently displace
// the other mid-flight — a job's window closed by someone cancelling a coffee
// break, or a keep-awake that quietly ends when a job finishes. Trimming
// underscores reserves the whole __name__ shape for internal ids.
func TestSlugCannotProduceTheReservedID(t *testing.T) {
	for _, name := range []string{
		KeepAwakeJobID,
		"__keep_awake__",
		"  __Keep Awake__  ",
		"__KEEP_AWAKE__",
		"_keep_awake_",
	} {
		if got := Slug(name); got == KeepAwakeJobID {
			t.Errorf("Slug(%q) = %q, which collides with the reserved keep-awake id", name, got)
		}
	}

	// Interior underscores are still ordinary characters; only the reserved
	// wrapping is taken away.
	if got := Slug("keep_awake"); got != "keep_awake" {
		t.Errorf("Slug(%q) = %q, want the underscore kept", "keep_awake", got)
	}

	// And the id is refused outright on the path that bypasses Slug entirely,
	// a hand-written jobs.json.
	j := &Job{ID: KeepAwakeJobID, Name: "sneaky", Schedule: "@daily", Detection: DetectNone}
	if err := j.Validate(); err == nil {
		t.Error("a job claiming the reserved keep-awake id was accepted")
	}
}

// TestJobWithoutAGroupKeyDecodesUngrouped is the backward-compatibility guard.
// Every job registered before groups existed has no "group" key at all, and
// must keep loading rather than failing validation or acquiring a name.
func TestJobWithoutAGroupKeyDecodesUngrouped(t *testing.T) {
	const stored = `{"id":"nightly-backup","name":"nightly backup",` +
		`"schedule":"0 3 * * *","detection":"mark","enabled":true}`

	var j Job
	if err := json.Unmarshal([]byte(stored), &j); err != nil {
		t.Fatalf("a pre-groups job no longer decodes: %v", err)
	}
	if j.Group != "" {
		t.Errorf("Group = %q, want ungrouped", j.Group)
	}
	if err := j.Validate(); err != nil {
		t.Errorf("a pre-groups job no longer validates: %v", err)
	}

	// It must also round-trip back out without gaining an empty key, so an
	// older reader still sees exactly the file it wrote.
	b, err := json.Marshal(&j)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "group") {
		t.Errorf("an ungrouped job serialised a group key: %s", b)
	}
}
