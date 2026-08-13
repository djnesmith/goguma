package scan

import (
	"testing"
	"time"
)

func TestCronFromOnCalendar(t *testing.T) {
	cases := map[string]string{
		"*-*-* 03:00:00":     "0 3 * * *",
		"Mon *-*-* 09:00:00": "0 9 * * 1",
		"Mon..Fri 18:00":     "0 18 * * 1-5",
		"daily":              "0 0 * * *",
		"weekly":             "0 0 * * 1",
		"*-*-1 03:00:00":     "0 3 1 * *",
		"Sat,Sun 20:00:00":   "0 20 * * 6,0",
		"*-*-* 03,15:00:00":  "0 3,15 * * *",
		"*-*-* *:00:00":      "0 * * * *",
		"hourly":             "0 * * * *",
		// Untranslatable forms come back empty rather than wrong: a wake at
		// the wrong time is worse than reporting no schedule.
		"2026-08-12 10:00": "", // a concrete year does not recur
		"Fri..Sun 10:00":   "", // wraps past Sunday; no cron range
		"*-*~1 03:00:00":   "", // last-day-of-month syntax
	}
	for in, want := range cases {
		if got := CronFromOnCalendar(in); got != want {
			t.Errorf("CronFromOnCalendar(%q) = %q, want %q", in, got, want)
		}
	}
}

// An interval schedule's replay phase is fabricated (anchored at process
// start), so it must never produce a confident "never missed" verdict. That
// verdict filtered out exactly the daily-digest jobs auto-adopt exists to
// catch, permanently, whenever the daemon happened to start at an awake hour.
func TestIntervalSchedulesAreNeverConfidentlyNeverMissed(t *testing.T) {
	// Machine asleep 23:00-08:00 nightly; the job really fires at 03:00 and
	// is missed every night. But the replay anchors at process start (a
	// daytime, awake hour), which is exactly the fiction that produced the
	// confident wrong verdict.
	hist := alwaysAsleepAt3am(14)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)
	entries := []Entry{{Name: "daily digest", Source: "hermes", Schedule: "every 1440m"}}

	keep, filtered := Evaluate(entries, hist, now, DefaultOptions())

	for _, f := range filtered {
		if f.Reason == ReasonNeverMissed {
			t.Fatalf("an interval schedule was confidently filtered as never missed; "+
				"its replayed fire times are fiction (filtered: %+v)", f.Entry.Name)
		}
	}
	if len(keep) == 0 {
		t.Fatal("the interval entry was not kept; unknown risk must keep, not hide")
	}
}
