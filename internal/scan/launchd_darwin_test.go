package scan

import (
	"encoding/json"
	"testing"
)

// A StartCalendarInterval array is several fire times. The common shape
// (same minute, different hours) is exactly expressible as a cron list, and
// dropping all but the first fire silently lost the overnight one, the only
// fire goguma matters for.
func TestCalendarArrayMergesIntoOneCron(t *testing.T) {
	raw := json.RawMessage(`[
		{"Hour": 14, "Minute": 0},
		{"Hour": 2, "Minute": 0}
	]`)
	if got := cronFromCalendar(raw); got != "0 2,14 * * *" {
		t.Errorf("cronFromCalendar = %q, want the union %q", got, "0 2,14 * * *")
	}
}

// Entries that differ in more than one field have no single cron form.
// Falling back to the first entry is the documented behavior; inventing an
// approximation would schedule wakes at the wrong times.
func TestCalendarArrayUnmergeableFallsBackToFirst(t *testing.T) {
	raw := json.RawMessage(`[
		{"Hour": 2, "Minute": 0},
		{"Hour": 14, "Minute": 30, "Weekday": 5}
	]`)
	if got := cronFromCalendar(raw); got != "0 2 * * *" {
		t.Errorf("cronFromCalendar = %q, want the first entry %q", got, "0 2 * * *")
	}
}

func TestCalendarArrayMergesWeekdays(t *testing.T) {
	raw := json.RawMessage(`[
		{"Hour": 9, "Minute": 30, "Weekday": 1},
		{"Hour": 9, "Minute": 30, "Weekday": 5}
	]`)
	if got := cronFromCalendar(raw); got != "30 9 * * 1,5" {
		t.Errorf("cronFromCalendar = %q, want %q", got, "30 9 * * 1,5")
	}
}
