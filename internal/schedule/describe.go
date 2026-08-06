package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Lists longer than this stop being a sentence and start being a table. Past
// the limit the wording either collapses to a count ("18 times a day") or the
// raw expression wins, because "on the 1st, 2nd, 3rd, 4th, 5th, 6th ... of the
// month" tells a reader less than "1-15" does.
const maxListed = 4

// describe renders a human-readable sentence for a parsed schedule, plus the
// compact form for rows that show the schedule next to its next fire time.
//
// Every field is expanded to its concrete set of values before any wording
// happens. The previous version decided from the raw text — "is this field a
// single number?" — and so bailed to the cron string for the shapes people
// actually write. "0 9,21 * * *" is a morning and an evening run, not an
// exotic expression, and it showed up in the GUI verbatim.
//
// Falling back to the raw expression is still the behaviour for anything that
// cannot be expanded or cannot be said readably, because a wrong or unreadable
// plain-English rendering of a schedule is worse than showing the cron string
// itself.
//
// Both widths are worded from the expanded fields together. Deriving the short
// form by cutting the finished sentence at "at" would be a second, weaker
// parser of this function's own output, and it would quietly mis-cut the next
// phrasing anyone adds here.
func describe(orig, norm string, interval time.Duration) (full, short string) {
	if interval > 0 {
		// A fixed interval is already as short as it gets.
		s := "every " + model_HumanDuration(interval)
		return s, s
	}
	switch strings.ToLower(norm) {
	case "@monthly":
		return "monthly, on the 1st at 00:00", "monthly"
	case "@yearly", "@annually":
		return "yearly, on Jan 1 at 00:00", "yearly"
	}
	fields := strings.Fields(norm)
	if len(fields) != 5 {
		return orig, orig
	}

	minute, ok := expandField(fields[0], 0, 59, nil)
	if !ok {
		return orig, orig
	}
	hour, ok := expandField(fields[1], 0, 23, nil)
	if !ok {
		return orig, orig
	}
	dom, ok := expandField(fields[2], 1, 31, nil)
	if !ok {
		return orig, orig
	}
	month, ok := expandField(fields[3], 1, 12, monthNumbers)
	if !ok {
		return orig, orig
	}
	dow, ok := expandField(fields[4], 0, 6, dowNumbers)
	if !ok {
		return orig, orig
	}

	when, times, ok := timePhrase(minute, hour)
	if !ok {
		return orig, orig
	}
	days, ok := dayPhrase(dom, month, dow)
	if !ok {
		return orig, orig
	}

	// times > 0 marks a plain clock list, which is the only phrasing that reads
	// correctly after "at". Sniffing for a leading digit would misfile
	// "18 times a day" as a clock and produce "daily at 18 times a day".
	if days.full == "" {
		switch {
		case times == 0:
			return when.full, when.short
		case times == 2:
			return "twice daily, at " + when.full, when.short
		default:
			return "daily at " + when.full, when.short
		}
	}
	if times > 0 {
		// The day constraint is the cadence once the clock list goes: a job on
		// "weekdays at 08:30" is a weekdays job, not a daily one.
		return days.full + " at " + when.full, days.short
	}
	// "every 15 minutes on weekdays" is already a cadence, so the day
	// constraint stays attached rather than replacing it.
	return when.full + " on " + days.full, when.short + " on " + days.full
}

// phrasing is one worded fragment at both widths. short drops the clock detail
// and keeps only the cadence, for the GUI's compact rows where the adjacent
// next-run time already says when the job fires.
type phrasing struct {
	full  string
	short string
}

// cadence names n fires per period ("daily", "monthly"). "twice" is spelled
// out because two a day — a morning and an evening run — is the commonest
// shape after one, and it deserves to read like English rather than a count.
func cadence(n int, period string) string {
	switch n {
	case 1:
		return period
	case 2:
		return "twice " + period
	}
	return fmt.Sprintf("%d× %s", n, period)
}

// cronField is one schedule field after expansion.
type cronField struct {
	// values is sorted and deduplicated.
	values []int
	// star records a field written as a bare "*". Day-of-week needs this
	// distinction: "*" means the field is not constraining anything, while an
	// explicit "0-6" is a deliberate "every day" worth saying out loud.
	star bool
	// all is true when the field covers its whole range however it was
	// written, so "0-59" and "*/1" are treated as the "*" they amount to.
	all bool
	// step is set only when the entire field is one full-range step term,
	// which is what makes "*/15" sayable as "every 15 minutes" rather than as
	// four listed times.
	step int
}

// expandField expands one field into its value set. names supplies the
// three-letter aliases the cron parser accepts for that field, if any.
func expandField(text string, lo, hi int, names map[string]int) (cronField, bool) {
	f := cronField{step: 1}
	if text == "" {
		return f, false
	}
	terms := strings.Split(text, ",")
	seen := make(map[int]bool, hi-lo+1)
	for _, term := range terms {
		vals, full, step, ok := expandTerm(term, lo, hi, names)
		if !ok {
			return cronField{}, false
		}
		if len(terms) == 1 {
			f.star = term == "*" || term == "?"
			if full && step > 1 {
				f.step = step
			}
		}
		for _, v := range vals {
			if !seen[v] {
				seen[v] = true
				f.values = append(f.values, v)
			}
		}
	}
	if len(f.values) == 0 {
		return cronField{}, false
	}
	sort.Ints(f.values)
	f.all = len(f.values) == hi-lo+1
	return f, true
}

// expandTerm expands a single comma-separated term: "*", "N", "a-b", "a-b/s",
// "*/s" or "N/s". full reports whether the term's base spans the whole field,
// which is what separates "*/6" from "9-17/6".
func expandTerm(term string, lo, hi int, names map[string]int) (vals []int, full bool, step int, ok bool) {
	base, stepText, hasStep := strings.Cut(term, "/")
	step = 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n <= 0 {
			return nil, false, 0, false
		}
		step = n
	}

	from, to := lo, hi
	if base != "*" && base != "?" {
		low, high, isRange := strings.Cut(base, "-")
		a, ok := fieldValue(low, names)
		if !ok {
			return nil, false, 0, false
		}
		switch {
		case isRange:
			b, ok := fieldValue(high, names)
			if !ok || b < a {
				return nil, false, 0, false
			}
			from, to = a, b
		case hasStep:
			// "5/15" means "5-max/15" to this parser and to vixie cron, so
			// reading it as the single value 5 would understate the schedule.
			from, to = a, hi
		default:
			from, to = a, a
		}
	}
	if from < lo || to > hi {
		return nil, false, 0, false
	}
	for v := from; v <= to; v += step {
		vals = append(vals, v)
	}
	return vals, from == lo && to == hi, step, true
}

func fieldValue(s string, names map[string]int) (int, bool) {
	if v, err := strconv.Atoi(s); err == nil {
		return v, true
	}
	v, ok := names[strings.ToLower(s)]
	return v, ok
}

// timePhrase words the minute and hour fields. times is the number of clock
// readings in the phrase when it is a plain list ("09:00 and 21:00"), and zero
// for every other shape, which is what the caller uses to decide whether the
// phrase can follow the word "at".
//
// The "every N minutes" and "every N hours" phrasings are their own short
// form: they name a cadence already, and there is nothing in them the next-run
// column repeats. Only the minute mark hanging off the end of one gets cut.
func timePhrase(minute, hour cronField) (p phrasing, times int, ok bool) {
	switch {
	case minute.all && hour.all:
		return phrasing{"every minute", "every minute"}, 0, true

	case hour.all && minute.step > 1:
		every := fmt.Sprintf("every %d minutes", minute.step)
		return phrasing{every, every}, 0, true

	case hour.all && len(minute.values) == 1:
		if minute.values[0] == 0 {
			return phrasing{"every hour, on the hour", "hourly"}, 0, true
		}
		return phrasing{fmt.Sprintf("every hour at :%02d", minute.values[0]), "hourly"}, 0, true

	case hour.all:
		if len(minute.values) > maxListed {
			return phrasing{}, 0, false
		}
		marks := make([]string, len(minute.values))
		for i, m := range minute.values {
			marks[i] = fmt.Sprintf(":%02d", m)
		}
		return phrasing{"every hour at " + joinAnd(marks), "hourly"}, 0, true

	case hour.step > 1 && len(minute.values) == 1:
		every := fmt.Sprintf("every %d hours", hour.step)
		if minute.values[0] == 0 {
			return phrasing{every, every}, 0, true
		}
		return phrasing{fmt.Sprintf("%s, at :%02d", every, minute.values[0]), every}, 0, true
	}

	clock := make([]string, 0, len(hour.values)*len(minute.values))
	for _, h := range hour.values {
		for _, m := range minute.values {
			clock = append(clock, fmt.Sprintf("%02d:%02d", h, m))
		}
	}
	if len(clock) > maxListed {
		return phrasing{fmt.Sprintf("%d times a day", len(clock)), cadence(len(clock), "daily")}, 0, true
	}
	return phrasing{joinAnd(clock), cadence(len(clock), "daily")}, len(clock), true
}

// dayPhrase words the day-of-month, month and day-of-week fields. An empty
// phrase means the schedule runs every day and the caller should say nothing
// about days at all.
//
// A weekday phrase is its own short form — "weekdays" is already the cadence —
// while a list of dates has to be counted into one.
func dayPhrase(dom, month, dow cronField) (phrasing, bool) {
	byDate, byWeekday, byMonth := !dom.all, !dow.star, !month.all

	var p phrasing
	if byDate {
		if len(dom.values) > maxListed {
			return phrasing{}, false
		}
		ordinals := make([]string, len(dom.values))
		for i, d := range dom.values {
			ordinals[i] = ordinal(d)
		}
		p.full = "on the " + joinAnd(ordinals) + " of the month"
		p.short = cadence(len(dom.values), "monthly")
	}
	if byWeekday {
		week := weekdayPhrase(dow.values)
		if byDate {
			// cron ORs the two day fields rather than intersecting them, and a
			// reader who assumes "the 1st, and only if it is a Monday" would be
			// wrong about most of the fires. Say "or".
			p.full += " or on " + week
			p.short += " or " + week
		} else {
			p.full, p.short = week, week
		}
	}
	if byMonth {
		if p.full == "" {
			p.full, p.short = "every day", "daily"
		}
		p.full += " in " + monthPhrase(month.values)
		if byDate && !byWeekday {
			// A date pinned to named months comes round once a year per
			// combination, so "monthly" would overstate it by a factor of
			// twelve — here the month field is what sets the cadence.
			p.short = yearlyCadence(len(dom.values)*len(month.values), month.values)
		} else {
			p.short += " in " + monthPhrase(month.values)
		}
	}
	return p, true
}

// yearlyCadence names n fires per year. A once-a-year job names its month,
// because "yearly" on its own leaves the obvious question unanswered and there
// is room for the answer.
func yearlyCadence(n int, months []int) string {
	if n == 1 {
		return "yearly, in " + monthPhrase(months)
	}
	return fmt.Sprintf("%d× yearly", n)
}

func weekdayPhrase(values []int) string {
	switch {
	case sameInts(values, []int{1, 2, 3, 4, 5}):
		return "weekdays"
	case sameInts(values, []int{0, 6}):
		return "weekends"
	case len(values) == 7:
		return "every day"
	case len(values) == 1:
		return weekdayFullNames[values[0]] + "s"
	}
	short := make([]string, len(values))
	for i, d := range values {
		short[i] = weekdayAbbrevs[d]
	}
	return joinAnd(short)
}

func monthPhrase(values []int) string {
	if len(values) == 1 {
		return monthFullNames[values[0]]
	}
	short := make([]string, len(values))
	for i, m := range values {
		short[i] = monthAbbrevs[m]
	}
	return joinAnd(short)
}

// joinAnd joins with "and" and no Oxford comma, matching how the rest of the
// UI reads times back to the user.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func ordinal(n int) string {
	suffix := "th"
	// 11th, 12th and 13th break the last-digit rule; no other day of the month
	// reaches the teens twice.
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var (
	monthNumbers = map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	dowNumbers = map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}
	monthFullNames = map[int]string{
		1: "January", 2: "February", 3: "March", 4: "April",
		5: "May", 6: "June", 7: "July", 8: "August",
		9: "September", 10: "October", 11: "November", 12: "December",
	}
	monthAbbrevs = map[int]string{
		1: "Jan", 2: "Feb", 3: "Mar", 4: "Apr", 5: "May", 6: "Jun",
		7: "Jul", 8: "Aug", 9: "Sep", 10: "Oct", 11: "Nov", 12: "Dec",
	}
	weekdayFullNames = map[int]string{
		0: "Sunday", 1: "Monday", 2: "Tuesday", 3: "Wednesday",
		4: "Thursday", 5: "Friday", 6: "Saturday",
	}
	weekdayAbbrevs = map[int]string{
		0: "Sun", 1: "Mon", 2: "Tue", 3: "Wed", 4: "Thu", 5: "Fri", 6: "Sat",
	}
)
