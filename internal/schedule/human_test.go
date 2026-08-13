package schedule

import (
	"testing"
	"time"
)

func TestFromHumanUnderstandsCommonPhrasings(t *testing.T) {
	tests := []struct{ in, want string }{
		{"every day at 9am", "0 9 * * *"},
		{"daily at 9am", "0 9 * * *"},
		{"at 9am", "0 9 * * *"},
		{"9am", "0 9 * * *"},
		{"every day at 9:30am", "30 9 * * *"},
		{"every day at 6pm", "0 18 * * *"},
		{"weekdays at 6pm", "0 18 * * 1-5"},
		{"weekends at 10am", "0 10 * * 6,0"},
		{"mondays at 09:00", "0 9 * * 1"},
		{"monday at 9am", "0 9 * * 1"},
		{"saturday at 11pm", "0 23 * * 6"},
		{"monday, wednesday, friday at 9am", "0 9 * * 1,3,5"},
		{"daily at midnight", "0 0 * * *"},
		{"every day at noon", "0 12 * * *"},
		{"at 21:00", "0 21 * * *"},
		{"at 12am", "0 0 * * *"},
		{"at 12pm", "0 12 * * *"},
		{"every 30 minutes", "every 30m"},
		{"every 6 hours", "every 6h"},
		{"every 15 min", "every 15m"},
		{"hourly", "@hourly"},
		{"daily", "@daily"},
	}
	for _, tc := range tests {
		got, ok := FromHuman(tc.in)
		if !ok {
			t.Errorf("FromHuman(%q) was not understood", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("FromHuman(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFromHumanRefusesAmbiguousBareHours(t *testing.T) {
	// "run at 8" could mean 08:00 or 20:00. Guessing would silently schedule
	// a wake twelve hours from where the user meant, so it is refused and the
	// user is shown the forms that are unambiguous.
	for _, in := range []string{"at 8", "every day at 8", "at 5"} {
		if got, ok := FromHuman(in); ok {
			t.Errorf("FromHuman(%q) = %q; a bare 1-11 hour is ambiguous and must be refused",
				in, got)
		}
	}
	// The same numbers are fine once disambiguated.
	for _, in := range []string{"at 8am", "at 8pm", "at 08:00"} {
		if _, ok := FromHuman(in); !ok {
			t.Errorf("FromHuman(%q) should be understood", in)
		}
	}
}

func TestFromHumanRefusesNonsense(t *testing.T) {
	for _, in := range []string{
		"", "   ", "sometime tomorrow", "when I feel like it",
		"every fortnight at 9am", "at 99:00", "at 9am on the third tuesday",
	} {
		if got, ok := FromHuman(in); ok {
			t.Errorf("FromHuman(%q) = %q; it should have been refused", in, got)
		}
	}
}

func TestParseAcceptsPlainEnglish(t *testing.T) {
	// The end-to-end property: a person can register a job without knowing
	// cron syntax, and the resulting schedule fires when they meant.
	s, err := Parse("every day at 9am", time.UTC)
	if err != nil {
		t.Fatalf("Parse could not read plain English: %v", err)
	}
	from := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	next := s.Next(from)
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("next fire at %s, want 09:00", next.Format("15:04"))
	}
	if s.Display != "daily at 09:00" {
		t.Errorf("Display = %q, want a readable rendering", s.Display)
	}
}

func TestParseStillPrefersRealCron(t *testing.T) {
	// A valid cron expression must never be reinterpreted by the English
	// parser; cron syntax is the precise form and takes precedence.
	s, err := Parse("30 2 * * 1-5", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if s.Expr != "30 2 * * 1-5" {
		t.Errorf("Expr = %q, want the original cron expression preserved", s.Expr)
	}
}

func TestParseEnglishWeekdaysFireOnWeekdaysOnly(t *testing.T) {
	s, err := Parse("weekdays at 6pm", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	for _, at := range s.NextN(from, 10) {
		if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
			t.Errorf("weekday schedule fired on %s", at.Weekday())
		}
		if at.Hour() != 18 {
			t.Errorf("fired at %02d:00, want 18:00", at.Hour())
		}
	}
}

func TestHumanExamplesAllParse(t *testing.T) {
	// The examples shown in help and error messages must actually work, or
	// the tool teaches the user forms it then rejects.
	for _, ex := range HumanExamples() {
		if _, err := Parse(ex, time.UTC); err != nil {
			t.Errorf("documented example %q does not parse: %v", ex, err)
		}
	}
}

// TestEveryTokenMustBeAccountedFor is the guard against the worst failure
// class: plain English that produces a confidently wrong schedule.
//
// Each of these was previously accepted and silently mistranslated.
func TestEveryTokenMustBeAccountedFor(t *testing.T) {
	wrong := map[string]string{
		"every 15 minutes on weekdays":            "read the 15 as 15:00, once a day instead of 96 times",
		"every 12 hours on weekdays":              "read the 12 as 12:00",
		"at 17:00 and 21:00 daily":                "silently dropped 21:00",
		"every day at 9am utc":                    "silently discarded the timezone",
		"mondays at 9am in january only":          "52 fires instead of 4",
		"tuesdays at 21:00 twice":                 "ignored the qualifier",
		"saturdays at 23:00 and sundays at 06:00": "invented a Sunday 23:00 wake",
	}
	for in, why := range wrong {
		if got, ok := FromHuman(in); ok {
			t.Errorf("FromHuman(%q) = %q, accepted; previously %s", in, got, why)
		}
	}
}

func TestDayRangesAreUnderstood(t *testing.T) {
	// "monday to friday" is the natural spelling of the documented
	// "weekdays at 6pm" example. It used to yield "1,5": Monday and Friday
	// only, losing three of five weekly runs.
	for _, in := range []string{
		"monday to friday at 07:30",
		"mon-fri at 07:30",
		"monday through friday at 07:30",
	} {
		got, ok := FromHuman(in)
		if !ok {
			t.Errorf("FromHuman(%q) was refused", in)
			continue
		}
		if got != "30 7 * * 1-5" {
			t.Errorf("FromHuman(%q) = %q, want %q", in, got, "30 7 * * 1-5")
		}
	}
}
