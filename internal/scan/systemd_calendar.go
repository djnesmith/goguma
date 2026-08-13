package scan

import (
	"fmt"
	"strconv"
	"strings"
)

// CronFromOnCalendar translates common systemd OnCalendar expressions into
// cron. It returns "" for forms cron cannot express (a concrete year, a
// wrapping weekday range, last-day-of-month syntax), so the caller reports
// "no schedule" only for the genuinely exotic rather than for every timer.
//
// Translation is deliberately conservative: a wrong wake time is worse than
// no wake, so anything not understood exactly comes back empty.
func CronFromOnCalendar(expr string) string {
	expr = strings.TrimSpace(expr)
	switch strings.ToLower(expr) {
	case "minutely":
		return "* * * * *"
	case "hourly":
		return "0 * * * *"
	case "daily":
		return "0 0 * * *"
	case "weekly":
		return "0 0 * * 1" // systemd weeks start Monday 00:00
	case "monthly":
		return "0 0 1 * *"
	case "quarterly":
		return "0 0 1 1,4,7,10 *"
	case "semiannually":
		return "0 0 1 1,7 *"
	case "yearly", "annually":
		return "0 0 1 1 *"
	}

	dow, date, clock := "", "", ""
	for _, part := range strings.Fields(expr) {
		switch {
		case strings.Contains(part, ":"):
			if clock != "" {
				return ""
			}
			clock = part
		case strings.Contains(part, "-") && !strings.ContainsAny(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"):
			if date != "" {
				return ""
			}
			date = part
		default:
			if dow != "" {
				return ""
			}
			dow = part
		}
	}

	cronDOW := "*"
	if dow != "" {
		var ok bool
		if cronDOW, ok = weekdayField(dow); !ok {
			return ""
		}
	}

	cronDOM, cronMon := "*", "*"
	if date != "" {
		bits := strings.Split(date, "-")
		if len(bits) != 3 || bits[0] != "*" {
			return "" // a concrete year does not recur; cron cannot say it
		}
		var ok bool
		if cronMon, ok = numField(bits[1], 1, 12); !ok {
			return ""
		}
		if cronDOM, ok = numField(bits[2], 1, 31); !ok {
			return ""
		}
	}

	cronMin, cronHour := "0", "0"
	if clock != "" {
		bits := strings.Split(clock, ":")
		if len(bits) != 2 && len(bits) != 3 {
			return ""
		}
		var ok bool
		if cronHour, ok = numField(bits[0], 0, 23); !ok {
			return ""
		}
		if cronMin, ok = numField(bits[1], 0, 59); !ok {
			return ""
		}
		// Seconds are dropped: cron has minute precision, and the wake
		// buffer dwarfs a sub-minute offset anyway.
	}

	return strings.Join([]string{cronMin, cronHour, cronDOM, cronMon, cronDOW}, " ")
}

// numField translates a systemd numeric component: "*", a value, a comma
// list, or "*/step".
func numField(s string, min, max int) (string, bool) {
	if s == "*" {
		return "*", true
	}
	if rest, found := strings.CutPrefix(s, "*/"); found {
		if n, err := strconv.Atoi(rest); err == nil && n > 0 {
			return "*/" + rest, true
		}
		_ = rest
		return "", false
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < min || n > max {
			return "", false
		}
		out = append(out, fmt.Sprintf("%d", n))
	}
	return strings.Join(out, ","), true
}

// weekdayField translates "Mon", "Mon,Fri", "Mon..Fri" (full names too)
// into cron day-of-week numbers. A range that wraps past Sunday cannot be a
// cron range, so it is rejected.
func weekdayField(s string) (string, bool) {
	names := map[string]int{
		"mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6, "sun": 0,
	}
	day := func(w string) (int, bool) {
		w = strings.ToLower(w)
		if len(w) > 3 {
			w = w[:3]
		}
		n, ok := names[w]
		return n, ok
	}

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if a, b, found := strings.Cut(p, ".."); found {
			from, ok1 := day(a)
			to, ok2 := day(b)
			if !ok1 || !ok2 || from > to || from == 0 {
				return "", false // wrapping ranges have no cron form
			}
			out = append(out, fmt.Sprintf("%d-%d", from, to))
			continue
		}
		n, ok := day(p)
		if !ok {
			return "", false
		}
		out = append(out, fmt.Sprintf("%d", n))
	}
	return strings.Join(out, ","), true
}
