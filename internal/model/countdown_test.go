package model

import (
	"testing"
	"time"
)

// TestHumanCountdownMatchesTheApp pins the CLI to the same shape the menu bar
// app uses (WGDuration.countdownString). Two surfaces describing the same
// instant in different words reads as one of them being broken.
func TestHumanCountdownMatchesTheApp(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{8 * time.Second, "8s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m"},
		{90 * time.Second, "1m"}, // truncates, never rounds up into the future
		{119 * time.Second, "1m"},
		{2 * time.Minute, "2m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h"},
		{time.Hour + time.Minute, "1h 1m"},
		{2 * time.Hour, "2h"},
	}
	for _, c := range cases {
		if got := HumanCountdown(c.in); got != c.want {
			t.Errorf("HumanCountdown(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A zero time still has to survive: an April 31st cron expression parses and
// never fires, and the saturating subtraction that produces used to crash.
func TestHumanCountdownSurvivesTheSaturatedGap(t *testing.T) {
	if got := HumanUntil(time.Time{}, time.Now()); got != "never" {
		t.Errorf("HumanUntil(zero) = %q, want %q", got, "never")
	}
	if got := HumanCountdown(time.Duration(-1 << 63)); got == "" {
		t.Error("HumanCountdown of the most negative duration returned empty")
	}
}
