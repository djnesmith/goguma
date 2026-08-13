package schedule

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FromHuman translates a plain-English schedule into a cron expression.
//
// The point is that a person knows *when* they want something to run long
// before they know how to write it in cron. Requiring "0 9 * * *" to register
// a morning briefing asks the human to speak the machine's language for no
// reason: the translation is mechanical and the machine can do it.
//
// Every accepted phrase is echoed back as both the cron expression and the
// next concrete fire time, because a natural-language parser that silently
// guesses wrong is worse than one that refuses. Anything not confidently
// understood is rejected with the forms that do work, rather than being
// approximated.
//
// Returns the cron expression and true when the input was understood.
func FromHuman(input string) (string, bool) {
	s := strings.ToLower(strings.Join(strings.Fields(input), " "))
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" {
		return "", false
	}

	// Fixed phrases first, so they cannot be mangled by the general rules.
	switch s {
	case "hourly", "every hour", "once an hour":
		return "@hourly", true
	case "daily", "every day", "once a day":
		return "@daily", true
	case "weekly", "every week", "once a week":
		return "@weekly", true
	case "monthly", "every month", "once a month":
		return "@monthly", true
	}

	// A pure interval: "every 30 minutes", "every 6 hours".
	//
	// Checked before the time-of-day rules because "every 2 hours" contains a
	// number that would otherwise be misread as an hour of the day.
	if expr, ok := intervalPhrase(s); ok {
		return expr, true
	}
	// An interval phrase with anything trailing is refused rather than allowed
	// to fall through.
	//
	// The regexp above is anchored, so "every 15 minutes on weekdays" does not
	// match it, and the 15 then reached the time-of-day rules and was read as
	// 15:00. The result was a job running once a day instead of ninety-six
	// times, which is both wrong and impossible to notice from the rendered
	// schedule. Any count of 12 to 23 did this, which is precisely why it
	// looked like it worked.
	if intervalPrefix.MatchString(s) {
		return "", false
	}

	// Everything else must name a time of day; without one there is nothing
	// to schedule.
	hour, minute, rest, ok := timeOfDay(s)
	if !ok {
		return "", false
	}
	dow, ok := dayConstraint(rest)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d %d * * %s", minute, hour, dow), true
}

var intervalRe = regexp.MustCompile(
	`^every\s+(\d+)\s*(minute|minutes|min|mins|m|hour|hours|hr|hrs|h)$`)

// intervalPrefix matches the same shape without the end anchor, so a phrase
// that begins like an interval but continues can be refused instead of
// silently reinterpreted.
var intervalPrefix = regexp.MustCompile(
	`^every\s+\d+\s*(minute|minutes|min|mins|m|hour|hours|hr|hrs|h)\b`)

func intervalPhrase(s string) (string, bool) {
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return "", false
	}
	switch m[2][0] {
	case 'm':
		return fmt.Sprintf("every %dm", n), true
	case 'h':
		return fmt.Sprintf("every %dh", n), true
	}
	return "", false
}

// timeRe matches "9am", "9:30am", "09:00", "21:00", "9 am".
var timeRe = regexp.MustCompile(`\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b`)

// timeOfDay extracts an hour and minute, returning the remaining words so the
// day constraint can be read from them.
func timeOfDay(s string) (hour, minute int, rest string, ok bool) {
	// Named times are unambiguous and easy to get wrong numerically.
	for phrase, hm := range map[string][2]int{
		"midnight": {0, 0}, "noon": {12, 0}, "midday": {12, 0},
	} {
		if strings.Contains(s, phrase) {
			return hm[0], hm[1], strings.ReplaceAll(s, phrase, " "), true
		}
	}

	m := timeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, "", false
	}
	h, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, "", false
	}
	if m[2] != "" {
		minute, err = strconv.Atoi(m[2])
		if err != nil {
			return 0, 0, "", false
		}
	}

	switch m[3] {
	case "am":
		if h == 12 { // 12am is midnight
			h = 0
		}
	case "pm":
		if h != 12 { // 12pm is noon
			h += 12
		}
	default:
		// A bare number with no am/pm is only unambiguous on a 24-hour clock.
		// Refusing 1-11 here is deliberate: guessing whether "run at 8" means
		// morning or evening would silently schedule a wake twelve hours off.
		if h < 12 && !strings.Contains(s, ":") {
			return 0, 0, "", false
		}
	}
	if h > 23 || minute > 59 {
		return 0, 0, "", false
	}
	return h, minute, strings.Replace(s, m[0], " ", 1), true
}

// dayNames maps the day words people actually use onto cron's numbering.
var dayNames = map[string]string{
	"sunday": "0", "sundays": "0", "sun": "0",
	"monday": "1", "mondays": "1", "mon": "1",
	"tuesday": "2", "tuesdays": "2", "tue": "2", "tues": "2",
	"wednesday": "3", "wednesdays": "3", "wed": "3",
	"thursday": "4", "thursdays": "4", "thu": "4", "thur": "4", "thurs": "4",
	"friday": "5", "fridays": "5", "fri": "5",
	"saturday": "6", "saturdays": "6", "sat": "6",
}

// qualifiers narrow a schedule in ways cron's five fields cannot express, or
// can only express with syntax this parser does not build.
//
// They must cause a refusal rather than being ignored. "the third tuesday"
// silently reduced to "tuesday" schedules a weekly job where the user asked
// for a monthly one: the job would then fire four times as often as intended,
// waking the machine for three runs that should not happen.
var qualifiers = map[string]bool{
	"first": true, "second": true, "third": true, "fourth": true,
	"fifth": true, "last": true, "penultimate": true,
	"1st": true, "2nd": true, "3rd": true, "4th": true, "5th": true,
	"alternate": true, "other": true, "odd": true, "even": true,
	"except": true, "unless": true, "between": true, "after": true,
	"before": true, "until": true,
}

// filler words carry no meaning for a schedule and may safely remain.
var filler = map[string]bool{
	"": true, "at": true, "every": true, "on": true,
	"run": true, "each": true, "the": true,
}

// dayConstraint reads which days the schedule applies to.
//
// Every token must be accounted for. An earlier version returned as soon as it
// matched a day word, so the "anything unrecognised" check never ran on that
// path and leftover words were silently dropped. That turned "monday to friday
// at 07:30" into Monday and Friday only: three of five weekly runs never woken
// for, from the natural spelling of a documented example. Recognising less and
// refusing more is much the safer failure.
func dayConstraint(rest string) (string, bool) {
	words := strings.FieldsFunc(rest, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})
	for i, w := range words {
		words[i] = strings.Trim(w, ".")
	}

	// Narrowing words change the meaning in ways this parser cannot express,
	// so they are refused before anything else is read.
	for _, w := range words {
		if qualifiers[w] {
			return "", false
		}
	}

	consumed := make([]bool, len(words))

	// A range first ("monday to friday", "mon-fri"), so its endpoints are not
	// also collected as individual days.
	for i, w := range words {
		lo, isDay := dayNames[w]
		if isDay && i+2 < len(words) && isRangeWord(words[i+1]) {
			if hi, ok := dayNames[words[i+2]]; ok {
				consumed[i], consumed[i+1], consumed[i+2] = true, true, true
				return dayRange(lo, hi), accountedFor(words, consumed)
			}
		}
		if lo2, hi2, ok := splitDayRange(w); ok {
			consumed[i] = true
			return dayRange(lo2, hi2), accountedFor(words, consumed)
		}
	}

	var days []string
	seen := map[string]bool{}
	everyDay := false

	for i, w := range words {
		switch {
		case w == "weekday" || w == "weekdays":
			consumed[i] = true
			return "1-5", accountedFor(words, consumed)
		case w == "weekend" || w == "weekends":
			consumed[i] = true
			return "6,0", accountedFor(words, consumed)
		case w == "daily", w == "day" && i > 0 && words[i-1] == "every":
			everyDay, consumed[i] = true, true
		default:
			if n, isDay := dayNames[w]; isDay {
				consumed[i] = true
				if !seen[n] {
					seen[n] = true
					days = append(days, n)
				}
			}
		}
	}

	switch {
	case len(days) > 0:
		return strings.Join(days, ","), accountedFor(words, consumed)
	case everyDay:
		return "*", accountedFor(words, consumed)
	default:
		// A bare time means daily, the overwhelmingly common intent, but only
		// when nothing else was said.
		return "*", accountedFor(words, consumed)
	}
}

func isRangeWord(w string) bool {
	return w == "to" || w == "through" || w == "until" || w == "-"
}

// accountedFor reports whether every token was consumed or is filler. A
// leftover means the phrase said something this parser did not understand, and
// reading past it is how a schedule ends up quietly wrong.
func accountedFor(words []string, consumed []bool) bool {
	for i, w := range words {
		if consumed[i] || filler[w] {
			continue
		}
		return false
	}
	return true
}

// splitDayRange parses a hyphenated range inside one token, e.g. "mon-fri".
func splitDayRange(tok string) (lo, hi string, ok bool) {
	i := strings.IndexByte(tok, '-')
	if i <= 0 || i == len(tok)-1 {
		return "", "", false
	}
	l, lok := dayNames[tok[:i]]
	h, hok := dayNames[tok[i+1:]]
	if !lok || !hok {
		return "", "", false
	}
	return l, h, true
}

// HumanExamples lists the accepted phrasings, for help text and error
// messages. Kept beside the parser so the two cannot drift apart.
func HumanExamples() []string {
	return []string{
		"every day at 9am",
		"weekdays at 6pm",
		"mondays at 09:00",
		"saturday at 11pm",
		"every 30 minutes",
		"every 6 hours",
		"daily at midnight",
		"hourly",
	}
}

// dayRange renders lo..hi as a cron day-of-week field. A range that runs
// "backwards" through the week ("friday to monday") cannot be a cron range,
// so it is spelled out as the wrapped list instead; emitting "5-1" produced
// an expression the parser rejects, which used to surface as a nil schedule.
func dayRange(lo, hi string) string {
	l, lerr := strconv.Atoi(lo)
	h, herr := strconv.Atoi(hi)
	if lerr != nil || herr != nil || l <= h {
		return lo + "-" + hi
	}
	var days []string
	for d := l; ; d = (d + 1) % 7 {
		days = append(days, strconv.Itoa(d))
		if d == h {
			break
		}
	}
	return strings.Join(days, ",")
}
