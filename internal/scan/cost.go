package scan

import (
	"fmt"
	"time"
)

// Cost is the battery price of covering a set of jobs.
//
// WakeGuard exists to save battery by sleeping between jobs, so it owes the
// user an honest account of what waking for those jobs costs. Presenting the
// list of jobs without this makes covering forty of them look the same as
// covering two.
type Cost struct {
	// WakesPerDay is how many times the machine would be woken.
	WakesPerDay float64
	// AwakePerDay is roughly how long it would spend awake as a result.
	AwakePerDay time.Duration
	// Busiest names the job responsible for the most wakes, which is almost
	// always the one worth reconsidering.
	Busiest      string
	BusiestWakes float64
}

// EstimateCost computes the wake cost of a set of candidates.
//
// perWake is the awake time a single wake implies: the buffer before the job
// fires plus however long the hold lasts afterwards.
func EstimateCost(candidates []Candidate, now time.Time, perWake time.Duration) Cost {
	var c Cost

	// Jobs sharing a schedule fire together and cost one wake between them,
	// so each distinct schedule is counted once. Three morning briefings all
	// at 09:00 are one wake, not three.
	//
	// Nothing is rounded up to a daily minimum. An earlier version clamped
	// every job to at least one wake a day, which priced a weekly job at seven
	// times its real cost and a yearly one at three hundred and sixty-five —
	// enough that a handful of weekly jobs tripped the "this is heavy" warning
	// while costing almost nothing.
	counted := map[string]bool{}
	for _, cand := range candidates {
		if cand.Parsed == nil || counted[cand.Schedule] {
			continue
		}
		gap := cand.Parsed.TypicalInterval(now)
		if gap <= 0 {
			continue
		}
		counted[cand.Schedule] = true

		per := float64(24*time.Hour) / float64(gap)
		c.WakesPerDay += per
		if per > c.BusiestWakes {
			c.Busiest, c.BusiestWakes = cand.Name, per
		}
	}
	c.AwakePerDay = time.Duration(c.WakesPerDay * float64(perWake))
	return c
}

// Summary renders the cost as a sentence.
func (c Cost) Summary() string {
	if c.WakesPerDay <= 0 {
		return "no extra wakes"
	}
	return fmt.Sprintf("about %.0f wakes a day, roughly %s of extra awake time",
		c.WakesPerDay, roundDuration(c.AwakePerDay))
}

// Heavy reports whether the cost is high enough to be worth querying.
//
// Twelve wakes a day is roughly one every two hours; past that the machine is
// spending a meaningful share of the night awake and the user should be told
// rather than left to discover it from their battery.
func (c Cost) Heavy() bool { return c.WakesPerDay > 12 }

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
}
