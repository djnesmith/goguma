package schedule

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// describeExpr runs an expression through the real normalisation path, so the
// test exercises what Parse actually hands to describe rather than a
// hand-written approximation of it.
func describeExpr(t *testing.T, expr string) (full, short string) {
	t.Helper()
	norm, interval, err := normalize(expr)
	if err != nil {
		t.Fatalf("normalize(%q): %v", expr, err)
	}
	return describe(expr, norm, interval)
}

func TestDescribeEverydayExpressions(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"0 9 * * *", "daily at 09:00"},
		{"0 9,21 * * *", "twice daily, at 09:00 and 21:00"},
		{"0 9,12,15 * * *", "daily at 09:00, 12:00 and 15:00"},
		{"*/15 * * * *", "every 15 minutes"},
		{"*/5 * * * *", "every 5 minutes"},
		{"0 */6 * * *", "every 6 hours"},
		{"30 */6 * * *", "every 6 hours, at :30"},
		{"0 * * * *", "every hour, on the hour"},
		{"30 * * * *", "every hour at :30"},
		{"30 8 * * 1-5", "weekdays at 08:30"},
		{"0 9 * * 1,3,5", "Mon, Wed and Fri at 09:00"},
		{"0 9 * * 1", "Mondays at 09:00"},
		{"0 10 * * 0,6", "weekends at 10:00"},
		{"0 0 1 * *", "on the 1st of the month at 00:00"},
		{"0 0 1,15 * *", "on the 1st and 15th of the month at 00:00"},
		{"*/15 * * * 1-5", "every 15 minutes on weekdays"},
		{"0 9 * * mon-fri", "weekdays at 09:00"},
		{"0 0 1 1 *", "on the 1st of the month in January at 00:00"},
	}
	for _, tc := range tests {
		got, _ := describeExpr(t, tc.expr)
		if got != tc.want {
			t.Errorf("describe(%q) = %q, want %q", tc.expr, got, tc.want)
		}
		// The complaint that prompted this was seeing raw cron in the GUI, so
		// echoing the input back is a failure even when nothing else is wrong.
		if got == tc.expr {
			t.Errorf("describe(%q) returned the raw expression", tc.expr)
		}
	}
}

func TestDescribeMoreShapes(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"* * * * *", "every minute"},
		{"0-59 * * * *", "every minute"},
		{"*/1 * * * *", "every minute"},
		{"*/30 * * * *", "every 30 minutes"},
		{"0,30 * * * *", "every hour at :00 and :30"},
		{"0,15,30,45 * * * *", "every hour at :00, :15, :30 and :45"},
		{"0 */2 * * *", "every 2 hours"},
		{"15 */12 * * *", "every 12 hours, at :15"},
		{"0 0 * * *", "daily at 00:00"},
		{"30 2 * * *", "daily at 02:30"},
		{"0 6,12,18,23 * * *", "daily at 06:00, 12:00, 18:00 and 23:00"},
		{"0 8,10,12,14,16 * * *", "5 times a day"},
		{"0,30 9,17 * * *", "daily at 09:00, 09:30, 17:00 and 17:30"},

		// Day-of-week shapes, including the abbreviations cron accepts.
		{"0 9 * * 0", "Sundays at 09:00"},
		{"0 9 * * SUN", "Sundays at 09:00"},
		{"0 9 * * sat,sun", "weekends at 09:00"},
		{"0 9 * * 6,0", "weekends at 09:00"},
		{"0 9 * * 0-6", "every day at 09:00"},
		{"0 9 * * 1,2,3", "Mon, Tue and Wed at 09:00"},
		{"0 22 * * 5", "Fridays at 22:00"},
		{"0 * * * 1-5", "every hour, on the hour on weekdays"},
		{"*/10 * * * 6", "every 10 minutes on Saturdays"},

		// Sunday-as-7 is rewritten before describe ever sees it.
		{"0 9 * * 7", "Sundays at 09:00"},
		{"0 9 * * 1-7", "every day at 09:00"},

		// Day of month, with the ordinals that do not follow the -th rule.
		{"0 0 2 * *", "on the 2nd of the month at 00:00"},
		{"0 0 3 * *", "on the 3rd of the month at 00:00"},
		{"0 0 11,12,13 * *", "on the 11th, 12th and 13th of the month at 00:00"},
		{"0 0 21 * *", "on the 21st of the month at 00:00"},
		{"0 0 31 * *", "on the 31st of the month at 00:00"},
		{"0 0 1-31 * *", "daily at 00:00"},

		// Months.
		{"0 9 * 6 *", "every day in June at 09:00"},
		{"0 9 * 1,4,7,10 *", "every day in Jan, Apr, Jul and Oct at 09:00"},
		{"0 9 * jan *", "every day in January at 09:00"},
		{"0 9 * * 1 ", "Mondays at 09:00"},
		{"0 0 15 3 *", "on the 15th of the month in March at 00:00"},

		// cron ORs the two day fields, so the wording must not imply "and".
		{"0 9 1 * 1", "on the 1st of the month or on Mondays at 09:00"},

		// Descriptors and intervals keep their existing wording.
		{"@monthly", "monthly, on the 1st at 00:00"},
		{"@yearly", "yearly, on Jan 1 at 00:00"},
		{"@daily", "every 1d"},
		{"@hourly", "every 1h"},
		{"every 6h", "every 6h"},
	}
	for _, tc := range tests {
		if got, _ := describeExpr(t, tc.expr); got != tc.want {
			t.Errorf("describe(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestDescribeShortForm pins the compact rendering shown in the job list and
// the schedule column, where a "next run" time sits alongside and the clock
// readings in the full form say nothing the reader cannot already see.
func TestDescribeShortForm(t *testing.T) {
	tests := []struct{ expr, want, wantShort string }{
		{"0 9 * * *", "daily at 09:00", "daily"},
		{"0 9,21 * * *", "twice daily, at 09:00 and 21:00", "twice daily"},
		{"0 9,12,15 * * *", "daily at 09:00, 12:00 and 15:00", "3× daily"},
		{"0 6,11,15,20 * * *", "daily at 06:00, 11:00, 15:00 and 20:00", "4× daily"},

		// Interval phrasings are cadences already; only the minute mark goes.
		{"*/15 * * * *", "every 15 minutes", "every 15 minutes"},
		{"0 */6 * * *", "every 6 hours", "every 6 hours"},
		{"30 */6 * * *", "every 6 hours, at :30", "every 6 hours"},
		{"0 * * * *", "every hour, on the hour", "hourly"},
		{"30 * * * *", "every hour at :30", "hourly"},

		// A day-of-week constraint is the cadence, so it replaces "daily".
		{"30 8 * * 1-5", "weekdays at 08:30", "weekdays"},
		{"0 9 * * 1,3,5", "Mon, Wed and Fri at 09:00", "Mon, Wed and Fri"},
		{"0 9 * * 1", "Mondays at 09:00", "Mondays"},
		{"0 10 * * 0,6", "weekends at 10:00", "weekends"},

		{"0 0 1 * *", "on the 1st of the month at 00:00", "monthly"},
		{"0 0 1,15 * *", "on the 1st and 15th of the month at 00:00", "twice monthly"},

		// An interval keeps whatever day constraint trails it.
		{"*/15 * * * 1-5", "every 15 minutes on weekdays", "every 15 minutes on weekdays"},

		// A date inside a named month is a once-a-year job, not a monthly one.
		{"0 0 1 1 *", "on the 1st of the month in January at 00:00", "yearly, in January"},
	}
	for _, tc := range tests {
		full, short := describeExpr(t, tc.expr)
		if full != tc.want {
			t.Errorf("describe(%q) full = %q, want %q", tc.expr, full, tc.want)
		}
		if short != tc.wantShort {
			t.Errorf("describe(%q) short = %q, want %q", tc.expr, short, tc.wantShort)
		}
	}
}

// TestDescribeShortIsNeverLonger guards the only promise the short form makes
// to a caller choosing between the two: it fits wherever the full one does.
func TestDescribeShortIsNeverLonger(t *testing.T) {
	for _, expr := range []string{
		"0 9 * * *",
		"0 9,21 * * *",
		"0 9,12,15 * * *",
		"0 6,11,15,20 * * *",
		"0 8,10,12,14,16 * * *",
		"*/15 * * * *",
		"0 */6 * * *",
		"30 */6 * * *",
		"0 * * * *",
		"30 * * * *",
		"0,15,30,45 * * * *",
		"* * * * *",
		"30 8 * * 1-5",
		"0 9 * * 1,3,5",
		"0 9 * * 1",
		"0 10 * * 0,6",
		"0 9 * * 0-6",
		"0 0 1 * *",
		"0 0 1,15 * *",
		"0 0 11,12,13 * *",
		"*/15 * * * 1-5",
		"0 * * * 1-5",
		"0 0 1 1 *",
		"0 0 1,15 1,7 *",
		"0 9 * 6 *",
		"0 9 * 6 1",
		"0 9 1 * 1",
		"@monthly",
		"@yearly",
		"@daily",
		"every 6h",
		"0 9 * * bogus",
	} {
		full, short := describeExpr(t, expr)
		if short == "" {
			t.Errorf("describe(%q) short is empty", expr)
		}
		// Counted in runes: the "×" in "3× daily" is three bytes and would
		// make a shorter phrase look longer.
		if utf8.RuneCountInString(short) > utf8.RuneCountInString(full) {
			t.Errorf("describe(%q) short = %q, longer than full %q", expr, short, full)
		}
	}
}

func TestDescribeFallsBackWhenUnreadable(t *testing.T) {
	// The fallback is the honest answer, not a bug: a list this long says less
	// in English than the cron field does, and a wrong rendering is worse than
	// either.
	for _, expr := range []string{
		"*/7 3,4 1-15 * *",
		"0 0 1-10 * *",
		"1-10 * * * *",
		"0 9 * * bogus",
		"*/0 9 * * *",
		"0 9 5-1 * *",
	} {
		got, short := describeExpr(t, expr)
		if got != expr {
			t.Errorf("describe(%q) = %q, want the raw expression", expr, got)
		}
		// There is nothing to shorten in a cron string, and an empty compact
		// column would read as "this job has no schedule".
		if short != expr {
			t.Errorf("describe(%q) short = %q, want the raw expression", expr, short)
		}
	}
}

func TestDescribeNeverEmitsAStrayComma(t *testing.T) {
	// joinAnd is the one place a list can go wrong, and a trailing "and" or a
	// doubled comma is the kind of thing that only shows up in the GUI.
	for _, expr := range []string{
		"0 9,21 * * *",
		"0 9,12,15 * * *",
		"0 9 * * 1,3,5",
		"0 0 1,15 * *",
		"0 9 * 1,4,7 *",
	} {
		full, short := describeExpr(t, expr)
		for _, got := range []string{full, short} {
			if strings.Contains(got, ", and") || strings.Contains(got, ",,") ||
				strings.HasSuffix(got, ",") || strings.HasSuffix(got, " and") {
				t.Errorf("describe(%q) = %q, malformed list", expr, got)
			}
		}
	}
}

func TestOrdinal(t *testing.T) {
	want := map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 10: "10th",
		11: "11th", 12: "12th", 13: "13th", 14: "14th",
		20: "20th", 21: "21st", 22: "22nd", 23: "23rd", 30: "30th", 31: "31st",
	}
	for n, w := range want {
		if got := ordinal(n); got != w {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, w)
		}
	}
}

func TestExpandFieldReadsSingleValueSteps(t *testing.T) {
	// "5/15" means "5-59/15" to the cron parser this package uses. Reading it
	// as the single minute 5 would describe a job as running once an hour when
	// it runs four times.
	f, ok := expandField("5/15", 0, 59, nil)
	if !ok {
		t.Fatal("expandField(5/15) failed")
	}
	if !sameInts(f.values, []int{5, 20, 35, 50}) {
		t.Errorf("values = %v, want [5 20 35 50]", f.values)
	}
}
