package schedule

import (
	"testing"
	"time"
)

func nyc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata on this machine")
	}
	return loc
}

// A fire time inside the skipped spring-forward hour must run right after
// the jump, the way vixie cron runs it. Skipping the whole day left the
// machine asleep through the job on exactly the 2-3am slots crontabs favor.
func TestSpringForwardFireRunsAfterTheJump(t *testing.T) {
	loc := nyc(t)
	sched, err := ParseAt("30 2 * * *", loc, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// DST starts 2026-03-08 02:00 -> 03:00 in America/New_York.
	from := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)

	first := sched.Next(from)
	want := time.Date(2026, 3, 8, 3, 0, 0, 0, loc)
	if !first.Equal(want) {
		t.Fatalf("Next = %s, want the post-jump instant %s", first, want)
	}
	// And the day after is back to normal.
	second := sched.Next(first)
	wantNext := time.Date(2026, 3, 9, 2, 30, 0, 0, loc)
	if !second.Equal(wantNext) {
		t.Errorf("following fire = %s, want %s", second, wantNext)
	}
}

// A fixed-time fire inside the repeated fall-back hour runs once. The second
// occurrence of 01:30 woke the machine for a run that never came and
// recorded a phantom miss every November.
func TestFallBackFireRunsOnce(t *testing.T) {
	loc := nyc(t)
	sched, err := ParseAt("30 1 * * *", loc, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// DST ends 2026-11-01 02:00 EDT -> 01:00 EST.
	from := time.Date(2026, 10, 31, 12, 0, 0, 0, loc)

	first := sched.Next(from)
	if got := first.UTC(); got != time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) {
		t.Fatalf("first fire = %s, want 01:30 EDT (05:30 UTC)", got)
	}
	second := sched.Next(first)
	if second.Sub(first) < 23*time.Hour {
		t.Fatalf("second fire %s is only %s after the first; the repeated "+
			"hour fired twice", second, second.Sub(first))
	}
}

// The plain-English fallback must never return (nil, nil): a stored job
// carrying such a schedule crash-looped the daemon on every tick.
func TestParseAtNeverReturnsNilNil(t *testing.T) {
	for _, expr := range []string{
		"every friday to monday at 9pm",
		"every fri-mon at 9am",
		"every sat-sun at 10am",
	} {
		sched, err := ParseAt(expr, time.Local, time.Time{})
		if sched == nil && err == nil {
			t.Fatalf("ParseAt(%q) = (nil, nil)", expr)
		}
		// These are all expressible as wrapped day lists, so they should in
		// fact parse now.
		if err != nil {
			t.Errorf("ParseAt(%q) failed: %v; a wrapped day range is a valid ask", expr, err)
		}
	}
}

// Reversed spellings of @every must be refused, not floored to a schedule
// that fires every second and pins the machine awake.
func TestEveryRejectsNonPositiveIntervals(t *testing.T) {
	for _, expr := range []string{"@every -5m", "@every 0s", "@every 500ms", "every 500ms"} {
		if _, err := ParseAt(expr, time.Local, time.Time{}); err == nil {
			t.Errorf("ParseAt(%q) accepted a sub-second or negative interval", expr)
		}
	}
}
