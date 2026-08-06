package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration that marshals to JSON as a human string
// ("90s", "5m") rather than an opaque nanosecond integer. Config files and
// `--json` output are both read by humans, and a bare 300000000000 in a
// config file is hostile. Unmarshalling accepts either form, so hand-edited
// files and machine-written ones both load.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string {
	return HumanDuration(time.Duration(d))
}

func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(HumanDuration(time.Duration(d)))
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	// Accept a bare number as nanoseconds so round-tripping a raw
	// time.Duration still works.
	if len(b) > 0 && b[0] != '"' {
		var n int64
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		*d = Duration(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if strings.TrimSpace(s) == "" {
		*d = 0
		return nil
	}
	parsed, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// unitSizes are the suffixes ParseDuration accepts, longest first so that
// "ms" is matched before "m".
var unitSizes = []struct {
	suffix string
	size   time.Duration
}{
	{"ms", time.Millisecond},
	{"us", time.Microsecond},
	{"ns", time.Nanosecond},
	{"w", 7 * 24 * time.Hour},
	{"d", 24 * time.Hour},
	{"h", time.Hour},
	{"m", time.Minute},
	{"s", time.Second},
}

// ParseDuration parses the duration forms WakeGuard writes and accepts.
//
// It must be the exact inverse of HumanDuration, which is why this is
// hand-rolled rather than delegating to time.ParseDuration. Two of
// HumanDuration's outputs are unparseable by the standard library:
//
//   - spaces between units — "1h 30m", "1d 12h"
//   - day and week units — "2d", "1w"
//
// Those are not hypothetical. Any config value that renders with two units
// would be written to config.json successfully and then fail to load on the
// next daemon start, silently reverting a setting the user had changed.
// Whitespace is therefore ignored and d/w are first-class.
func ParseDuration(s string) (time.Duration, error) {
	orig := s
	// Strip all whitespace so "1h 30m" and "1h30m" are the same input.
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	neg := false
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		neg, s = true, rest
	} else {
		s = strings.TrimPrefix(s, "+")
	}

	var total time.Duration
	matched := false
	for s != "" {
		// Read the numeric part, which may be fractional ("1.5h").
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration %q: expected a number before %q (try 90s, 5m, 2h, 1d)", orig, s)
		}
		value, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %q is not a number", orig, s[:i])
		}
		s = s[i:]

		unit, ok := time.Duration(0), false
		for _, u := range unitSizes {
			if rest, found := strings.CutPrefix(s, u.suffix); found {
				unit, ok, s = u.size, true, rest
				break
			}
		}
		if !ok {
			return 0, fmt.Errorf(
				"invalid duration %q: missing or unknown unit (use ms, s, m, h, d, or w)", orig)
		}
		total += time.Duration(value * float64(unit))
		matched = true
	}
	if !matched {
		return 0, fmt.Errorf("invalid duration %q (try 90s, 5m, 2h, 1d)", orig)
	}
	if neg {
		total = -total
	}
	return total, nil
}

// HumanDuration renders a duration the way the CLI and GUI show it: compact,
// at most two units, and never scientific notation. Sub-minute values keep
// one decimal because job runtimes are frequently a few seconds and "3s" vs
// "3.4s" is a meaningful difference when you're tuning a ceiling.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		// Negating the most negative int64 overflows back to itself, so the
		// obvious `-HumanDuration(-d)` recurses until the stack is exhausted
		// and the process dies.
		//
		// That is reachable, not theoretical: `time.Time{}.Sub(now)` saturates
		// to exactly this value, and a zero time arrives whenever a cron
		// expression has no future occurrence — "0 0 31 4 *" is April 31st,
		// which parses cleanly and never fires. Rendering a job list would
		// then crash the CLI.
		if d == math.MinInt64 {
			return "a very long time"
		}
		return "-" + HumanDuration(-d)
	}
	switch {
	case d == 0:
		return "0s"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		secs := d.Seconds()
		if secs == float64(int64(secs)) {
			return fmt.Sprintf("%ds", int64(secs))
		}
		return strings.TrimSuffix(fmt.Sprintf("%.1f", secs), ".0") + "s"
	case d < time.Hour:
		m := int64(d / time.Minute)
		s := int64((d % time.Minute) / time.Second)
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	case d < 24*time.Hour:
		h := int64(d / time.Hour)
		m := int64((d % time.Hour) / time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days := int64(d / (24 * time.Hour))
		h := int64((d % (24 * time.Hour)) / time.Hour)
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}

// HumanUntil renders a relative time the way the job list shows "next run":
// "in 13h 59m", "in 42s", or "overdue by 2m" when a fire time has passed
// without the daemon observing the job.
// HumanCountdown renders a gap that is on screen while it counts down.
//
// Whole minutes until the last one, then seconds: 54m, 3m, 1m, 59s, 8s.
// HumanDuration gives "54m 34s", which is right for a duration read once — a
// ceiling, a schedule, a run time — and wrong for one that ticks: the seconds
// column changes every second for an hour, drawing the eye to a figure nobody
// is watching until the end. This is the CLI counterpart of the app's
// `WGDuration.countdownString`, and the two must agree; a Mac showing "in 22m"
// beside a terminal showing "in 22m 58s" looks like one of them is wrong.
func HumanCountdown(d time.Duration) string {
	if d < 0 {
		if d == math.MinInt64 {
			return "a very long time"
		}
		return "-" + HumanCountdown(-d)
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		// Beyond a day HumanDuration is already coarse enough.
		return HumanDuration(d)
	}
}

func HumanUntil(t time.Time, now time.Time) string {
	// A zero time means the caller had no answer — most often a schedule with
	// no future occurrence. Saying so is more useful than rendering the two
	// thousand years between year one and now.
	if t.IsZero() {
		return "never"
	}
	d := t.Sub(now)
	if d < 0 {
		return "overdue by " + HumanCountdown(-d)
	}
	return "in " + HumanCountdown(d)
}

// Clock renders an elapsed duration as mm:ss / hh:mm:ss, for the live
// "elapsed" counter in `status`, `watch`, and the menu bar.
func Clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d / time.Second)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
