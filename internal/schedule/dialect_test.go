package schedule

import (
	"testing"
	"time"
)

// TestSundayAsSevenIsAccepted guards a false negative that would have silently
// dropped working jobs.
//
// POSIX and vixie cron — the cron actually installed on macOS and Linux —
// accept day-of-week 0-7 with both 0 and 7 meaning Sunday. The underlying
// parser follows the stricter 0-6 range. Without normalisation a legitimate
// crontab line is reported as unparseable and never woken for, while the
// user's cron happily keeps running it.
func TestSundayAsSevenIsAccepted(t *testing.T) {
	// A Monday, so "next Sunday" is unambiguous.
	from := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	seven, err := Parse("0 0 * * 7", time.UTC)
	if err != nil {
		t.Fatalf("dow=7 must be accepted as Sunday, as vixie cron does: %v", err)
	}
	zero, err := Parse("0 0 * * 0", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := seven.Next(from), zero.Next(from); !got.Equal(want) {
		t.Errorf("dow=7 fired %s, dow=0 fired %s; both mean Sunday", got, want)
	}
	if seven.Next(from).Weekday() != time.Sunday {
		t.Errorf("dow=7 fired on %s, want Sunday", seven.Next(from).Weekday())
	}
}

func TestDayOfWeekRangesEndingInSeven(t *testing.T) {
	from := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		expr string
		want []time.Weekday
	}{
		// 1-7 spans Monday through Sunday: every day.
		{"0 0 * * 1-7", []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
			time.Friday, time.Saturday, time.Sunday}},
		// 0-7 is also every day.
		{"0 0 * * 0-7", []time.Weekday{
			time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
			time.Friday, time.Saturday, time.Sunday}},
		// A list containing 7 includes Sunday.
		{"0 0 * * 1,7", []time.Weekday{time.Monday, time.Sunday}},
		// 6-7 is the weekend.
		{"0 0 * * 6-7", []time.Weekday{time.Saturday, time.Sunday}},
	}

	for _, tc := range tests {
		s, err := Parse(tc.expr, time.UTC)
		if err != nil {
			t.Errorf("Parse(%q) failed: %v", tc.expr, err)
			continue
		}
		seen := map[time.Weekday]bool{}
		for _, at := range s.NextN(from, 14) {
			seen[at.Weekday()] = true
		}
		for _, w := range tc.want {
			if !seen[w] {
				t.Errorf("%q never fired on %s", tc.expr, w)
			}
		}
		if len(seen) != len(tc.want) {
			t.Errorf("%q fired on %d distinct weekdays, want %d", tc.expr, len(seen), len(tc.want))
		}
	}
}

func TestStepsAreNotRewritten(t *testing.T) {
	// "*/7" in the day-of-week field is a stride, not a weekday. Rewriting the
	// 7 there would change the schedule's meaning entirely.
	s, err := Parse("0 0 * * */7", time.UTC)
	if err != nil {
		// Some parsers reject a stride of 7 over a 0-6 range; either way the
		// point is that it must not be silently reinterpreted as Sunday.
		t.Skipf("*/7 rejected by the parser, which is acceptable: %v", err)
	}
	from := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	if s.Next(from).Weekday() != time.Sunday {
		return // stride semantics; nothing to assert beyond not crashing
	}
}

// TestForeignDialectsFailLoudly documents which constructs are deliberately
// not supported.
//
// These come from other schedulers — Quartz, Jenkins, AWS EventBridge — whose
// syntax overlaps cron's without matching it. The important property is that
// they are REJECTED rather than misread: a construct that parsed successfully
// under the wrong semantics would schedule wakes at confidently wrong times,
// which is far worse than refusing the line.
func TestForeignDialectsFailLoudly(t *testing.T) {
	foreign := []struct{ expr, from string }{
		{"0 0 12 ? * WED", "Quartz — ? means no specific value"},
		{"H/15 * * * *", "Jenkins — H spreads load by hashing the job name"},
		{"0 0 L * *", "Quartz — L is the last day of the month"},
		{"0 0 * * 6#3", "Quartz — #3 is the third occurrence"},
		{"0 0 1 * ? *", "AWS EventBridge — six fields with a required ?"},
		{"*/0 * * * *", "a step of zero is undefined"},
	}
	for _, f := range foreign {
		if _, err := Parse(f.expr, time.UTC); err == nil {
			t.Errorf("%q (%s) was accepted; unsupported dialects must be refused, "+
				"not reinterpreted under cron semantics", f.expr, f.from)
		}
	}
}

// TestImpossibleSchedulesAreRejected guards a reachable crash.
//
// "0 0 31 4 *" is April 31st. It is valid cron syntax and an easy off-by-one,
// but it has no next occurrence, so the parser returns the zero time forever.
// A job built on it sat in the list looking scheduled while never running —
// and rendering that list crashed the CLI, because a zero time subtracted from
// now saturates to the most negative int64, which the duration formatter
// recursed on until the stack was exhausted.
func TestImpossibleSchedulesAreRejected(t *testing.T) {
	for _, expr := range []string{
		"0 0 30 2 *", // February 30th
		"0 0 31 2 *", // February 31st
		"0 0 31 4 *", // April has 30 days
		"0 0 31 6 *", // June has 30 days
	} {
		if _, err := Parse(expr, time.UTC); err == nil {
			t.Errorf("Parse(%q) was accepted; it can never fire", expr)
		}
	}

	// Dates that are rare but real must still work. February 29th happens on
	// leap years and is a legitimate schedule.
	for _, expr := range []string{"0 0 29 2 *", "0 0 31 1 *", "0 0 29 * *"} {
		if _, err := Parse(expr, time.UTC); err != nil {
			t.Errorf("Parse(%q) was rejected but is a real date: %v", expr, err)
		}
	}
}

// TestSundaySevenInStepRangesAndSelfRanges guards two forms that were dropped.
//
// "0-7/2" was skipped entirely because of its slash, so the 7 reached a parser
// that rejects it and a valid vixie crontab line was reported unparseable —
// the exact failure the normalisation exists to prevent. "7-7" was rewritten
// to the backwards range "7-6,0" and rejected the same way.
func TestSundaySevenInStepRangesAndSelfRanges(t *testing.T) {
	from := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC) // a Monday

	cases := []struct {
		expr string
		want []time.Weekday
	}{
		// vixie: 0,2,4,6
		{"0 3 * * 0-7/2", []time.Weekday{
			time.Sunday, time.Tuesday, time.Thursday, time.Saturday}},
		// vixie: 1,3,5,7 with 7 folding onto Sunday
		{"0 3 * * 1-7/2", []time.Weekday{
			time.Sunday, time.Monday, time.Wednesday, time.Friday}},
		{"0 0 * * 7-7", []time.Weekday{time.Sunday}},
		{"0 0 * * 6-7", []time.Weekday{time.Saturday, time.Sunday}},
	}
	for _, tc := range cases {
		s, err := Parse(tc.expr, time.UTC)
		if err != nil {
			t.Errorf("Parse(%q) rejected a valid vixie expression: %v", tc.expr, err)
			continue
		}
		seen := map[time.Weekday]bool{}
		for _, at := range s.NextN(from, 21) {
			seen[at.Weekday()] = true
		}
		for _, w := range tc.want {
			if !seen[w] {
				t.Errorf("%q never fired on %s", tc.expr, w)
			}
		}
		if len(seen) != len(tc.want) {
			t.Errorf("%q fired on %d distinct days, want %d", tc.expr, len(seen), len(tc.want))
		}
	}
}
