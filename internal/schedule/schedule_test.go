package schedule

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := Parse(expr, time.UTC)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func TestParseAcceptedForms(t *testing.T) {
	for _, expr := range []string{
		"0 9 * * *", "*/5 * * * *", "0 0 1 * *", "30 2 * * 1-5",
		"@daily", "@hourly", "@weekly", "@midnight",
		"every 6h", "every 30m", "@every 2h",
	} {
		if _, err := Parse(expr, time.UTC); err != nil {
			t.Errorf("Parse(%q) failed: %v", expr, err)
		}
	}
}

func TestParseRejectsNonsense(t *testing.T) {
	for _, expr := range []string{
		"", "not a schedule", "0 9 * *", "99 99 * * *", "every", "every banana",
	} {
		if _, err := Parse(expr, time.UTC); err == nil {
			t.Errorf("Parse(%q) should have failed", expr)
		}
	}
}

func TestRebootIsRejectedWithAReason(t *testing.T) {
	// @reboot has no fire time to wake for — the machine is awake by
	// definition when it runs. Accepting it would create a job that can never
	// trigger a wake, which is worse than refusing it.
	_, err := Parse("@reboot", time.UTC)
	if err == nil {
		t.Fatal("@reboot should be rejected")
	}
	if !contains(err.Error(), "no scheduled time") {
		t.Errorf("error should explain why: %v", err)
	}
}

func TestNextFireTime(t *testing.T) {
	s := mustParse(t, "0 9 * * *")
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)

	got := s.Next(from)
	want := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

func TestNextNIsStrictlyIncreasing(t *testing.T) {
	s := mustParse(t, "0 9 * * *")
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)

	times := s.NextN(from, 5)
	if len(times) != 5 {
		t.Fatalf("got %d times, want 5", len(times))
	}
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Errorf("times are not increasing at %d: %s then %s", i, times[i-1], times[i])
		}
	}
}

func TestTypicalIntervalCatchesFrequentCalendarSchedules(t *testing.T) {
	from := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	// "*/5 * * * *" declares no interval but fires every five minutes. Import
	// must catch this on frequency grounds, or it would propose waking the
	// machine 288 times a day.
	if got := mustParse(t, "*/5 * * * *").TypicalInterval(from); got != 5*time.Minute {
		t.Errorf("*/5 interval = %s, want 5m", got)
	}
	if got := mustParse(t, "0 9 * * *").TypicalInterval(from); got != 24*time.Hour {
		t.Errorf("daily interval = %s, want 24h", got)
	}
	if got := mustParse(t, "every 6h").TypicalInterval(from); got != 6*time.Hour {
		t.Errorf("every-6h interval = %s, want 6h", got)
	}
}

func TestTypicalIntervalUsesTheSmallestGap(t *testing.T) {
	from := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	// Fires at 09:00 and 09:05 daily. The gaps alternate between 5 minutes
	// and just under 24 hours. For "is a dedicated wake worth it", the job is
	// five-minutes-apart, not daily — so the minimum is the right statistic.
	if got := mustParse(t, "0,5 9 * * *").TypicalInterval(from); got != 5*time.Minute {
		t.Errorf("interval = %s, want the 5m minimum gap", got)
	}
}

func TestDisplayStrings(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"0 9 * * *", "daily at 09:00"},
		{"30 2 * * *", "daily at 02:30"},
		{"every 6h", "every 6h"},
		{"0 9 * * 1-5", "weekdays at 09:00"},
	}
	for _, tc := range tests {
		if got := mustParse(t, tc.expr).Display; got != tc.want {
			t.Errorf("Display(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestDisplayFallsBackToRawExpression(t *testing.T) {
	// A wrong plain-English rendering is worse than showing the cron string,
	// so anything not confidently describable stays verbatim.
	expr := "*/7 3,4 1-15 * *"
	if got := mustParse(t, expr).Display; got != expr {
		t.Errorf("Display = %q, want the raw expression %q", got, expr)
	}
}

func TestScheduleRespectsTimezone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	s, err := Parse("0 9 * * *", tokyo)
	if err != nil {
		t.Fatal(err)
	}
	// 09:00 Tokyo is 00:00 UTC.
	from := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	got := s.Next(from).UTC()
	if got.Hour() != 0 {
		t.Errorf("next fire at %s UTC, want 00:00 UTC (09:00 Tokyo)", got.Format("15:04"))
	}
}

// TestIntervalFireTimeIsStableAcrossParses is the regression test for the bug
// that motivated anchoring.
//
// Reproduced on the live daemon: the same job, polled four seconds apart,
// reported a next fire four seconds further out. Every poll rebuilt the
// Schedule, and an unanchored interval schedule answers "six hours from
// whatever you passed me", so the countdown never counted down. The same value
// drives the OS wake, so the wake was re-registered a tick further out on
// every tick and never arrived.
func TestIntervalFireTimeIsStableAcrossParses(t *testing.T) {
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC)
	t0 := time.Date(2026, 8, 5, 23, 47, 3, 0, time.UTC)

	for _, expr := range []string{"every 6h", "every 360m", "@every 6h"} {
		first, err := ParseAt(expr, time.UTC, anchor)
		if err != nil {
			t.Fatalf("ParseAt(%q): %v", expr, err)
		}
		// A second construction, four seconds later, exactly as the daemon
		// does it on the next poll.
		second, err := ParseAt(expr, time.UTC, anchor)
		if err != nil {
			t.Fatalf("ParseAt(%q): %v", expr, err)
		}

		a := first.Next(t0)
		b := second.Next(t0.Add(4 * time.Second))
		if !a.Equal(b) {
			t.Errorf("%s: fire moved from %s to %s between two polls four seconds apart",
				expr, a, b)
		}
	}
}

func TestIntervalNextLandsOnAnAnchorBoundary(t *testing.T) {
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC)
	s, err := ParseAt("every 6h", time.UTC, anchor)
	if err != nil {
		t.Fatal(err)
	}

	for _, t0 := range []time.Time{
		anchor.Add(time.Second),
		anchor.Add(3 * time.Hour),
		anchor.Add(97 * time.Hour),
	} {
		got := s.Next(t0)
		if !got.After(t0) {
			t.Errorf("Next(%s) = %s, which is not strictly after t", t0, got)
		}
		if off := got.Sub(anchor) % (6 * time.Hour); off != 0 {
			t.Errorf("Next(%s) = %s, which is %s off the anchor's 6h boundary", t0, got, off)
		}
	}
}

func TestIntervalAnchorEqualToTStepsToTheNextFire(t *testing.T) {
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC)
	s, err := ParseAt("every 6h", time.UTC, anchor)
	if err != nil {
		t.Fatal(err)
	}
	// Landing exactly on a fire must move past it, not return t itself.
	// NextN and the miss-risk walk both feed the result straight back in, so
	// returning t would spin forever on one instant.
	if got, want := s.Next(anchor), anchor.Add(6*time.Hour); !got.Equal(want) {
		t.Errorf("Next(anchor) = %s, want %s", got, want)
	}
}

func TestIntervalAnchorInTheFutureIsTheFirstFire(t *testing.T) {
	// A created_at ahead of the clock — a clock that stepped backwards, or a
	// hand-edited jobs.json. The lattice must not be extended backwards: a
	// fire time in the past reads as "due now" to the daemon, which would hold
	// the machine awake immediately for a job that is not coming.
	anchor := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	s, err := ParseAt("every 6h", time.UTC, anchor)
	if err != nil {
		t.Fatal(err)
	}
	t0 := anchor.Add(-2 * time.Hour)
	if got := s.Next(t0); !got.Equal(anchor) {
		t.Errorf("Next = %s, want the anchor %s", got, anchor)
	}
}

func TestIntervalZeroAnchorIsStillStable(t *testing.T) {
	// No anchor available falls back to process start, which is fixed for the
	// life of the process. The fire time is arbitrary — it has to be, nothing
	// told us the phase — but it must not move between two calls, because that
	// movement is the bug.
	s1, err := ParseAt("every 6h", time.UTC, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Parse("every 6h", time.UTC) // Parse means "no anchor"
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a := s1.Next(now)
	b := s2.Next(now.Add(time.Second))
	if !a.Equal(b) {
		t.Errorf("unanchored fire moved from %s to %s a second apart", a, b)
	}
	if !a.After(now) {
		t.Errorf("Next = %s, which is not in the future", a)
	}
}

func TestIntervalAbsurdAnchorDoesNotProduceAPastFire(t *testing.T) {
	// time.Duration tops out near 292 years and Sub saturates there silently,
	// after which the multiplication back out wraps negative. Left unguarded,
	// a corrupt created_at yields a fire time in the distant past, which the
	// daemon reads as due right now and acts on by pinning the machine awake
	// indefinitely.
	for _, anchor := range []time.Time{
		time.Date(1500, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		s, err := ParseAt("every 6h", time.UTC, anchor)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		got := s.Next(now)
		if !got.After(now) {
			t.Errorf("anchor %s produced fire %s, which is not in the future", anchor, got)
		}
		if got.Sub(now) > 6*time.Hour {
			t.Errorf("anchor %s produced fire %s, more than one interval out", anchor, got)
		}
	}
}

func TestIntervalAnchorIsTruncatedToTheSecond(t *testing.T) {
	// A nanosecond tail on created_at would show up in every fire time the GUI
	// renders, and the sub-second precision is meaningless next to a wake
	// buffer measured in minutes.
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 123456789, time.UTC)
	s, err := ParseAt("every 6h", time.UTC, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Next(anchor); got.Nanosecond() != 0 {
		t.Errorf("Next = %s, want a whole second", got)
	}
}

func TestFixedPeriodDescriptorsStayCalendar(t *testing.T) {
	// @hourly, @daily and @weekly report an interval so that import can price
	// them, but they parse to calendar schedules: the top of the hour, and
	// midnight. They were never affected by the sliding-fire bug, and they must
	// not be anchored — anchoring @daily on a job's created_at would move it
	// off midnight to whenever the job happened to be registered, which is not
	// what the user wrote and not what their real cron does.
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC)
	from := time.Date(2026, 8, 5, 23, 47, 3, 0, time.UTC)

	tests := []struct {
		expr string
		want time.Time
	}{
		{"@hourly", time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)},
		{"@daily", time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)},
		{"@weekly", time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		s, err := ParseAt(tc.expr, time.UTC, anchor)
		if err != nil {
			t.Fatalf("ParseAt(%q): %v", tc.expr, err)
		}
		if got := s.Next(from); !got.Equal(tc.want) {
			t.Errorf("%s: Next = %s, want %s", tc.expr, got, tc.want)
		}
		// They still report an interval, which is what lets import price them.
		if _, ok := s.Interval(); !ok {
			t.Errorf("%s: should still declare an interval for import", tc.expr)
		}
		// But they are not anchored, and callers can tell the two apart.
		if _, ok := s.AnchoredInterval(); ok {
			t.Errorf("%s: is a calendar schedule and must not be anchored", tc.expr)
		}
	}

	if d, ok := mustParse(t, "every 6h").AnchoredInterval(); !ok || d != 6*time.Hour {
		t.Errorf(`AnchoredInterval("every 6h") = %s, %v; want 6h, true`, d, ok)
	}
	if _, ok := mustParse(t, "0 9 * * *").AnchoredInterval(); ok {
		t.Error("a cron expression must not report an anchored interval")
	}
}

func TestCalendarScheduleIgnoresTheAnchor(t *testing.T) {
	from := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	for _, anchor := range []time.Time{
		{},
		time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC),
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		s, err := ParseAt("0 9 * * *", time.UTC, anchor)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Next(from); !got.Equal(want) {
			t.Errorf("anchor %s changed a cron fire time: got %s, want %s", anchor, got, want)
		}
	}
}

func TestNextNWalksTheIntervalLattice(t *testing.T) {
	anchor := time.Date(2026, 8, 1, 5, 47, 3, 0, time.UTC)
	s, err := ParseAt("every 6h", time.UTC, anchor)
	if err != nil {
		t.Fatal(err)
	}
	times := s.NextN(anchor.Add(time.Minute), 4)
	if len(times) != 4 {
		t.Fatalf("got %d times, want 4", len(times))
	}
	for i, got := range times {
		want := anchor.Add(time.Duration(i+1) * 6 * time.Hour)
		if !got.Equal(want) {
			t.Errorf("fire %d = %s, want %s", i, got, want)
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
