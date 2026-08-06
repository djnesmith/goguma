package model

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// TestDurationRoundTrip is the regression guard for a bug that would have
// silently reverted user settings.
//
// Durations are rendered for humans ("1h 30m") and parsed back from the same
// text when config.json is reloaded. time.ParseDuration rejects both the
// space and the day/week units that HumanDuration produces, so any two-unit
// value saved successfully and then failed to load on the next daemon start.
func TestDurationRoundTrip(t *testing.T) {
	values := []time.Duration{
		0,
		500 * time.Millisecond,
		time.Second,
		2500 * time.Millisecond,
		30 * time.Second,
		90 * time.Second,
		5 * time.Minute,
		time.Hour,
		90 * time.Minute, // renders "1h 30m" — the original failure
		2*time.Hour + 5*time.Minute,
		24 * time.Hour,
		36 * time.Hour, // renders "1d 12h"
		7 * 24 * time.Hour,
	}
	for _, want := range values {
		text := HumanDuration(want)
		if want == 0 {
			continue // rendered as "0s"; parsed separately below
		}
		got, err := ParseDuration(text)
		if err != nil {
			t.Errorf("HumanDuration(%v) = %q, which does not parse back: %v", want, text, err)
			continue
		}
		// Rendering is lossy below its smallest displayed unit, so compare
		// with a tolerance proportional to that unit rather than exactly.
		tolerance := time.Second
		switch {
		case want >= 24*time.Hour:
			tolerance = time.Hour
		case want >= time.Hour:
			tolerance = time.Minute
		case want >= time.Minute:
			tolerance = time.Second
		}
		if diff := got - want; diff > tolerance || diff < -tolerance {
			t.Errorf("round trip of %v via %q gave %v (off by %v)", want, text, got, diff)
		}
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	type holder struct {
		D Duration `json:"d"`
	}
	for _, want := range []time.Duration{
		90 * time.Second, 90 * time.Minute, 36 * time.Hour, 5 * time.Minute,
	} {
		b, err := json.Marshal(holder{D: Duration(want)})
		if err != nil {
			t.Fatal(err)
		}
		var got holder
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("unmarshalling %s failed: %v", b, err)
			continue
		}
		if diff := got.D.D() - want; diff > time.Minute || diff < -time.Minute {
			t.Errorf("JSON round trip of %v gave %v (via %s)", want, got.D.D(), b)
		}
	}
}

func TestParseDurationAcceptedForms(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"90s", 90 * time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1d", 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"1h 30m", 90 * time.Minute}, // the human-rendered form
		{"  1h  30m  ", 90 * time.Minute},
		{"1d 12h", 36 * time.Hour},
		{"1.5h", 90 * time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"2h5m30s", 2*time.Hour + 5*time.Minute + 30*time.Second},
		{"-5m", -5 * time.Minute},
		{"1H30M", 90 * time.Minute}, // case-insensitive
	}
	for _, tc := range tests {
		got, err := ParseDuration(tc.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) failed: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseDurationRejectsNonsense(t *testing.T) {
	for _, in := range []string{
		"", "   ", "abc", "5", "5x", "h", "5m3", "--5m",
	} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) should have failed, got %v", in, got)
		}
	}
}

func TestParseDurationErrorsAreActionable(t *testing.T) {
	// A user hand-editing config.json needs to know which units are valid,
	// not just that their value was wrong.
	_, err := ParseDuration("5 fortnights")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !contains(msg, "unit") {
		t.Errorf("error %q should mention the valid units", msg)
	}
}

func TestHumanDurationFormatting(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "500ms"},
		{time.Second, "1s"},
		{2500 * time.Millisecond, "2.5s"},
		{42 * time.Second, "42s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h 30m"},
		{24 * time.Hour, "1d"},
		{36 * time.Hour, "1d 12h"},
	}
	for _, tc := range tests {
		if got := HumanDuration(tc.in); got != tc.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHumanDurationHasNoDoubledUnit(t *testing.T) {
	// A fractional-seconds value once rendered as "47.9ss".
	for _, d := range []time.Duration{
		47900 * time.Millisecond, 2500 * time.Millisecond, 1100 * time.Millisecond,
	} {
		got := HumanDuration(d)
		if len(got) >= 2 && got[len(got)-1] == 's' && got[len(got)-2] == 's' {
			t.Errorf("HumanDuration(%v) = %q has a doubled unit suffix", d, got)
		}
	}
}

func TestClock(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00"},
		{38 * time.Second, "00:38"},
		{90 * time.Second, "01:30"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"},
		{-5 * time.Second, "00:00"},
	}
	for _, tc := range tests {
		if got := Clock(tc.in); got != tc.want {
			t.Errorf("Clock(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"morning-briefing", "morning-briefing"},
		{"Morning Briefing", "morning-briefing"},
		{"  spaced  out  ", "spaced-out"},
		{"com.google.Keystone", "com.google.keystone"},
		{"weird!!!chars###here", "weird-chars-here"},
		{"---leading", "leading"},
	}
	for _, tc := range tests {
		if got := Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestInvalidMatchPatternIsRejected(t *testing.T) {
	// An uncompilable pattern used to be accepted and stored. The job then
	// looked correctly registered while the daemon's matcher failed to build
	// on every tick, so the job was never observed — waking the machine and
	// holding it for the full ceiling on every run, indefinitely, with nothing
	// visibly wrong.
	j := &Job{
		ID: "x", Name: "x", Schedule: "0 9 * * *",
		Detection: DetectPattern, Match: "([unclosed",
	}
	err := j.Validate()
	if err == nil {
		t.Fatal("an invalid regexp was accepted; the job could never be detected")
	}
	if !contains(err.Error(), "invalid") {
		t.Errorf("error %q should say the pattern is invalid", err)
	}

	// A valid one still passes.
	j.Match = "hermes.*briefing"
	if err := j.Validate(); err != nil {
		t.Errorf("a valid pattern was rejected: %v", err)
	}

	// Other modes do not carry a pattern and must not be affected.
	for _, mode := range []DetectionMode{DetectMark, DetectNone} {
		k := &Job{ID: "y", Name: "y", Schedule: "@daily", Detection: mode}
		if err := k.Validate(); err != nil {
			t.Errorf("%s job rejected: %v", mode, err)
		}
	}
}

func TestSlugNeutralisesPathTraversal(t *testing.T) {
	// The id becomes a filename under the history directory, so a name
	// containing path separators must not be able to escape it.
	for _, name := range []string{
		"../../etc/passwd", "/etc/shadow", "..", "a/../../b", "x\x00y",
	} {
		got := Slug(name)
		if contains(got, "/") || contains(got, `\`) || got == ".." || got == "." {
			t.Errorf("Slug(%q) = %q, which is not a safe single filename", name, got)
		}
	}
}

func TestHumanDurationSurvivesTheMostNegativeDuration(t *testing.T) {
	// Negating math.MinInt64 overflows back to itself, so the natural
	// `-HumanDuration(-d)` recursed until the stack was exhausted. The value
	// is reachable: time.Time{}.Sub(now) saturates to exactly this.
	done := make(chan string, 1)
	go func() { done <- HumanDuration(math.MinInt64) }()
	select {
	case got := <-done:
		if got == "" {
			t.Error("expected some rendering of the extreme value")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HumanDuration(MinInt64) did not return; it is recursing")
	}
}

func TestHumanUntilHandlesAZeroTime(t *testing.T) {
	// A zero time arrives whenever a schedule has no future occurrence.
	// Rendering the two thousand years since year one is both useless and,
	// before the fix, fatal.
	done := make(chan string, 1)
	go func() { done <- HumanUntil(time.Time{}, time.Now()) }()
	select {
	case got := <-done:
		if got != "never" {
			t.Errorf("HumanUntil(zero) = %q, want %q", got, "never")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HumanUntil(zero time) did not return")
	}
}
