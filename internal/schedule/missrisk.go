package schedule

import (
	"sort"
	"time"
)

// SleepInterval is one observed period during which the machine was asleep.
// Wake is zero for the currently-open interval, which only exists in a
// history reconstructed from system logs (the daemon itself cannot record a
// sleep it is currently inside).
type SleepInterval struct {
	Sleep time.Time `json:"sleep"`
	Wake  time.Time `json:"wake,omitzero"`
}

// Contains reports whether t fell inside this sleep period.
func (s SleepInterval) Contains(t time.Time) bool {
	if t.Before(s.Sleep) {
		return false
	}
	if s.Wake.IsZero() {
		return true // still asleep as far as this record knows
	}
	return t.Before(s.Wake)
}

// SleepHistory is the machine's observed sleep/wake record. WakeGuard uses
// it to answer the only question that matters when deciding whether a job is
// worth waking for: has this job actually been getting skipped?
//
// A heuristic ("jobs between 1am and 8am are risky") would be wrong for
// anyone whose laptop stays open overnight or who works nights. Measuring is
// both more accurate and explainable to the user.
type SleepHistory struct {
	Intervals []SleepInterval
	// Since is the earliest moment the record covers. Risk estimates are
	// clamped to this window so a one-day-old install cannot claim to know
	// what happened last week.
	Since time.Time
}

// AsleepAt reports whether the machine was asleep at t.
func (h *SleepHistory) AsleepAt(t time.Time) bool {
	for _, iv := range h.Intervals {
		if iv.Contains(t) {
			return true
		}
	}
	return false
}

// Coverage is how long a span the history actually spans.
func (h *SleepHistory) Coverage(now time.Time) time.Duration {
	if h.Since.IsZero() || len(h.Intervals) == 0 {
		return 0
	}
	return now.Sub(h.Since)
}

// MissRisk is the empirical probability that a given schedule fires while
// the machine is asleep.
type MissRisk struct {
	// Fires is how many past fire times were evaluated.
	Fires int `json:"fires"`
	// Missed is how many of those landed inside a sleep interval.
	Missed int `json:"missed"`
	// Ratio is Missed/Fires, 0 when Fires is 0.
	Ratio float64 `json:"ratio"`
	// Confident is false when the history is too short or too sparse to draw
	// a conclusion, in which case the UI must present this as unknown rather
	// than as a low risk. Absence of evidence is the failure mode that would
	// hide exactly the jobs the user wants found.
	Confident bool `json:"confident"`
	// Coverage is how far back the underlying history reaches.
	Coverage time.Duration `json:"coverage"`
}

// Level buckets the ratio for display. Kept as a small enum so the CLI and
// GUI render the same three categories rather than each inventing cutoffs.
type RiskLevel string

const (
	RiskUnknown RiskLevel = "unknown"
	RiskNone    RiskLevel = "none"
	RiskSome    RiskLevel = "some"
	RiskHigh    RiskLevel = "high"
)

func (m MissRisk) Level() RiskLevel {
	switch {
	case !m.Confident:
		return RiskUnknown
	case m.Ratio == 0:
		return RiskNone
	case m.Ratio < 0.5:
		return RiskSome
	default:
		return RiskHigh
	}
}

// minFiresForConfidence is how many past fire times must be observable before
// a risk ratio is reported as trustworthy. Two data points can be a
// coincidence; three is a pattern worth telling the user about.
const minFiresForConfidence = 3

// EstimateMissRisk replays a schedule backwards over the sleep history and
// counts how many of its past fire times landed while the machine was
// asleep. Those are runs that were silently skipped — no error, no retry,
// which is the entire problem WakeGuard exists to solve.
func EstimateMissRisk(s *Schedule, h *SleepHistory, now time.Time, lookback time.Duration) MissRisk {
	risk := MissRisk{Coverage: h.Coverage(now)}
	if s == nil || h == nil || len(h.Intervals) == 0 {
		return risk
	}
	// Never claim to evaluate a period the history does not cover.
	start := now.Add(-lookback)
	if !h.Since.IsZero() && h.Since.After(start) {
		start = h.Since
	}
	if !start.Before(now) {
		return risk
	}

	for _, fire := range pastFires(s, start, now, 500) {
		risk.Fires++
		if h.AsleepAt(fire) {
			risk.Missed++
		}
	}
	if risk.Fires > 0 {
		risk.Ratio = float64(risk.Missed) / float64(risk.Fires)
	}
	risk.Confident = risk.Fires >= minFiresForConfidence
	return risk
}

// pastFires enumerates fire times in [start, now). The cron library only
// walks forward, so this steps forward from start and collects. The cap
// bounds the work for a pathologically frequent schedule — such a schedule
// is filtered out on frequency grounds anyway.
func pastFires(s *Schedule, start, now time.Time, cap int) []time.Time {
	var out []time.Time
	cur := start
	for len(out) < cap {
		cur = s.Next(cur)
		if cur.IsZero() || !cur.Before(now) {
			break
		}
		out = append(out, cur)
	}
	return out
}

// DarkWakeGap is how long a gap between two sleep periods may be while still
// counting as continuous sleep.
//
// macOS punctuates a long sleep with frequent maintenance DarkWakes lasting
// two to five seconds each. The machine is not usefully awake during those —
// user cron jobs do not reliably run — so a fire time landing in such a gap
// was still missed. Without coalescing, an overnight sleep is recorded as
// dozens of separate intervals with live gaps between them, and a job that
// happened to fire in one would be scored "not missed", which is exactly
// backwards.
const DarkWakeGap = 90 * time.Second

// CoalesceShortGaps joins sleep periods separated by less than maxGap into
// single continuous intervals. Input must already be merged and sorted.
func CoalesceShortGaps(in []SleepInterval, maxGap time.Duration) []SleepInterval {
	if len(in) < 2 {
		return in
	}
	out := []SleepInterval{in[0]}
	for _, iv := range in[1:] {
		last := &out[len(out)-1]
		if last.Wake.IsZero() {
			continue
		}
		if iv.Sleep.Sub(last.Wake) <= maxGap {
			if iv.Wake.IsZero() || iv.Wake.After(last.Wake) {
				last.Wake = iv.Wake
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// MergeIntervals normalises a sleep record: sorted, with overlapping or
// touching periods coalesced. Log-derived records routinely contain
// overlapping entries (a dark-wake nested inside a longer sleep), which
// would otherwise be double-counted.
func MergeIntervals(in []SleepInterval) []SleepInterval {
	if len(in) < 2 {
		return in
	}
	sorted := make([]SleepInterval, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sleep.Before(sorted[j].Sleep) })

	out := []SleepInterval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]
		// An open-ended interval swallows everything after it.
		if last.Wake.IsZero() {
			continue
		}
		if !iv.Sleep.After(last.Wake) {
			if iv.Wake.IsZero() || iv.Wake.After(last.Wake) {
				last.Wake = iv.Wake
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
