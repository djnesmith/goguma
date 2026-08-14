// Package scan discovers scheduled jobs already configured on the machine
// and decides which of them are worth waking for.
//
// The hard problem is not finding entries; it is not drowning the user in
// them. A typical Mac has hundreds of loaded launchd services and, usually,
// an empty crontab. Presenting all of that would be worse than presenting
// nothing.
//
// So the filter is not "does this look like a cron job" but "would this
// actually be missed". Most entries fail that test for structural reasons:
// they belong to the OS, they are always running, they have no time trigger
// at all, or they fire so often that waking for them would cost far more
// battery than the run is worth. What survives is ranked by measured
// miss-risk: how often the entry has genuinely been firing while this
// machine was asleep.
package scan

import (
	"strings"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// Source identifies where a candidate came from.
type Source string

const (
	SourceCrontab Source = "crontab"
	SourceLaunchd Source = "launchd"
	SourceSystemd Source = "systemd"
)

// Entry is a discovered scheduled job, before filtering.
type Entry struct {
	Name     string
	Source   Source
	Schedule string
	Command  string

	// File and Line locate the definition so the user can go look at it.
	File string
	Line int

	// Label is the launchd/systemd identifier, empty for crontab.
	Label string

	// AlwaysRunning marks a service that stays resident (KeepAlive, a live
	// pid, or socket-activated). Such a service is never "missed"; it is
	// already there when the machine wakes.
	AlwaysRunning bool

	// SelfHealing marks an entry the scheduler itself catches up on after a
	// missed window. launchd re-fires a missed StartCalendarInterval job on
	// wake, so updaters and similar lose nothing by running late.
	SelfHealing bool

	// SystemOwned marks OS-vendor entries, which are not the user's
	// automation and are not ours to manage.
	SystemOwned bool

	// LastRun and NextRun are the scheduler's own records, when it keeps
	// them. Most schedulers do not: cron keeps nothing, launchd exposes
	// nothing useful. Application schedulers frequently do, and when
	// available this is far better evidence than anything goguma can
	// infer; see ObservedLateness.
	LastRun time.Time
	NextRun time.Time

	// Wrappable reports whether the user can put `goguma-mark` in front of
	// this job's command.
	//
	// False for application schedulers, which invoke their jobs internally:
	// there is no crontab line to edit and often no distinct process to match
	// either. Offering the wrapper for such a job would be advice the user
	// cannot act on.
	Wrappable bool

	// Dialect is the syntax this entry's Schedule is written in.
	//
	// Declared by the provider, because only the engine that will execute the
	// schedule determines how to read it; see dialect.go. Empty means
	// DialectCron, which is what every current provider produces.
	Dialect Dialect

	// TimeSensitive marks a schedule that names a specific wall-clock time
	// rather than an interval.
	//
	// The distinction matters for how much lateness costs. "Every 6 hours"
	// only asks to happen periodically, and running an hour late is nearly
	// free. "At 09:00" states that 09:00 is the point: a morning briefing
	// delivered at 13:00 has lost most of its value even though it did
	// eventually run.
	TimeSensitive bool
}

// Reason explains why an entry was filtered out. Shown under `import --all`
// so the filtering is auditable rather than a black box.
type Reason string

const (
	ReasonSystemOwned   Reason = "belongs to the operating system"
	ReasonAlwaysRunning Reason = "runs continuously, so it is never missed"
	ReasonNoSchedule    Reason = "has no scheduled time to wake for"
	ReasonTooFrequent   Reason = "fires too often to be worth a dedicated wake"
	ReasonSelfHealing   Reason = "the scheduler re-runs it automatically after a missed window"
	ReasonUnparseable   Reason = "its schedule could not be parsed"
	// ReasonUnsupportedDialect is deliberately distinct from unparseable. The
	// schedule is perfectly valid (in a syntax goguma would misread), so
	// the honest report is "not supported", not "malformed".
	ReasonUnsupportedDialect Reason = "its schedule syntax would be misread by this parser"
	ReasonNeverMissed        Reason = "has not been missed, the machine is awake when it fires"
)

// Candidate is an entry evaluated against the sleep history.
type Candidate struct {
	Entry

	Parsed   *schedule.Schedule
	NextFire time.Time
	Risk     schedule.MissRisk

	// Lateness is how far behind its scheduled time the last run actually
	// happened, computed from the scheduler's own last_run_at record.
	//
	// This is direct observation rather than inference: it does not depend on
	// reconstructing sleep history or on the schedule replay being right. When
	// available it is the strongest evidence there is, so it outranks
	// MissRisk.
	Lateness    time.Duration
	HasLateness bool

	// Filtered is set when the entry was excluded, with Reason explaining it.
	Filtered bool
	Reason   Reason
}

// LatenessThreshold is how far behind schedule a run must be before it counts
// as evidence of a problem.
//
// Schedulers are not instantaneous and a machine under load can be a minute
// slow without anything being wrong. Five minutes is comfortably past normal
// jitter while still catching the multi-hour delays that sleeping causes.
const LatenessThreshold = 5 * time.Minute

// Options tune the filtering.
type Options struct {
	// MinInterval rejects entries firing more often than this. Zero means no
	// schedule is rejected for its frequency, which is the default: the cost
	// of a frequent job is real, but it is measured and reported rather than
	// guessed at here, and refusing to even offer the job hides it instead.
	MinInterval time.Duration

	// Lookback bounds how far back miss-risk is measured.
	Lookback time.Duration

	// IncludeNeverMissed keeps entries that measurably never get missed.
	// Off by default: if the machine is reliably awake when a job fires,
	// goguma has nothing to contribute.
	IncludeNeverMissed bool
}

func DefaultOptions() Options {
	return Options{
		// No frequency floor. See Config.MinImportInterval.
		MinInterval: 0,
		Lookback:    14 * 24 * time.Hour,
	}
}

// Evaluate filters and ranks discovered entries.
//
// Returns the candidates worth showing (most at-risk first) and everything
// that was excluded, each with its reason.
func Evaluate(entries []Entry, hist *schedule.SleepHistory, now time.Time, opts Options) (keep, filtered []Candidate) {
	for _, e := range entries {
		c := Candidate{Entry: e}

		switch {
		case e.SystemOwned:
			c.Filtered, c.Reason = true, ReasonSystemOwned
		case e.AlwaysRunning:
			c.Filtered, c.Reason = true, ReasonAlwaysRunning
		case strings.TrimSpace(e.Schedule) == "":
			c.Filtered, c.Reason = true, ReasonNoSchedule
		}
		if c.Filtered {
			filtered = append(filtered, c)
			continue
		}

		// Refuse a dialect we cannot read correctly, before attempting to read
		// it. Parsing Quartz or Jenkins syntax as POSIX cron would frequently
		// succeed and silently mean different days.
		if !e.Dialect.Supported() {
			c.Filtered, c.Reason = true, ReasonUnsupportedDialect
			filtered = append(filtered, c)
			continue
		}

		// Plain Parse: a scanned entry is not a job yet and has no CreatedAt to
		// anchor an interval schedule to. That leaves an interval entry's
		// computed phase arbitrary, but it is only ever a fallback here,
		// because the source's own next-run time wins immediately below, and
		// nothing else this function decides depends on phase. Cadence, which
		// the frequency filter and the cost estimate do depend on, is exact
		// either way.
		sched, err := schedule.Parse(e.Schedule, time.Local)
		if err != nil {
			c.Filtered, c.Reason = true, ReasonUnparseable
			filtered = append(filtered, c)
			continue
		}
		c.Parsed = sched
		// The scheduler's own next-run time is authoritative when it publishes
		// one. Recomputing it from the expression can disagree (an interval
		// schedule counts from its last run, not from now), and showing a time
		// that differs from what will actually happen is worse than showing
		// nothing.
		if !e.NextRun.IsZero() && e.NextRun.After(now) {
			c.NextFire = e.NextRun
		} else {
			c.NextFire = sched.Next(now)
		}

		// Frequency check uses the smallest observed gap, so a calendar-form
		// schedule like "*/5 * * * *" is caught even though it declares no
		// interval.
		// Inclusive on purpose. With an exclusive comparison a job running at
		// exactly the threshold slips through: a 30-minute job against a
		// 30-minute limit is kept, which alone is 48 wakes a day.
		if gap := sched.TypicalInterval(now); gap > 0 && gap <= opts.MinInterval {
			c.Filtered, c.Reason = true, ReasonTooFrequent
			filtered = append(filtered, c)
			continue
		}

		// Self-healing is deliberately NOT excluded here. It is checked after
		// lateness below, so that a scheduler which claims to catch up but
		// demonstrably runs hours late still surfaces.
		//
		// The replay is skipped for interval schedules. Their phase here is
		// arbitrary (anchored at whatever moment this process started, per
		// the Parse comment above), so replayed fire times are fiction: a
		// daily job that really fires at 03:00 replays at the process start
		// hour, reads confidently "never missed" against a machine that was
		// awake then, and is filtered, permanently hiding exactly the job
		// this scan exists to find. An unconfident zero Risk is kept by the
		// never-missed filter below, which is the honest outcome: for these
		// entries the truth arrives via observed lateness, not replay.
		if _, interval := sched.Interval(); !interval {
			c.Risk = schedule.EstimateMissRisk(sched, hist, now, opts.Lookback)
		}
		c.Lateness, c.HasLateness = observedLateness(sched, e.LastRun)

		// Observed lateness beats every other consideration, including the
		// self-healing exclusion. A scheduler that reliably catches up after a
		// missed window is not a problem in principle, but if its own records
		// show a run four hours behind a 09:00 schedule, then in practice this
		// job is not happening when the user expects, and no amount of
		// catch-up logic changes that.
		if c.HasLateness && c.Lateness >= LatenessThreshold && e.TimeSensitive {
			keep = append(keep, c)
			continue
		}

		if e.SelfHealing {
			c.Filtered, c.Reason = true, ReasonSelfHealing
			filtered = append(filtered, c)
			continue
		}

		// A confidently-never-missed job needs no wake. An *unknown* risk is
		// deliberately kept: absence of evidence would otherwise hide exactly
		// the jobs the user is looking for.
		if !opts.IncludeNeverMissed && c.Risk.Confident && c.Risk.Missed == 0 {
			c.Filtered, c.Reason = true, ReasonNeverMissed
			filtered = append(filtered, c)
			continue
		}
		keep = append(keep, c)
	}

	rank(keep)
	return keep, filtered
}

// observedLateness measures how far past its scheduled time a run happened.
//
// The run at lastRun was fulfilling the most recent scheduled fire time at or
// before it, so the delay is the gap between the two. Walking forward from a
// generous lookback is the only option because cron schedules are evaluated
// forwards; the window is bounded so a pathological schedule cannot spin.
//
// It reports "unknown" for an interval schedule, by construction rather than
// by a special case: an interval entry is parsed with no anchor, so its fire
// times start at process start and the walk finds none before lastRun. That is
// the honest answer. Lateness means "late against the time this was supposed
// to run", and an unanchored interval schedule has no such time; the number
// the walk used to produce measured the lookback's own phase, not the job's.
func observedLateness(s *schedule.Schedule, lastRun time.Time) (time.Duration, bool) {
	if s == nil || lastRun.IsZero() {
		return 0, false
	}
	// Look back far enough to find the preceding fire for a weekly schedule.
	cur := lastRun.Add(-8 * 24 * time.Hour)
	var prev time.Time
	for range 2000 {
		next := s.Next(cur)
		if next.IsZero() || next.After(lastRun) {
			break
		}
		prev, cur = next, next
	}
	if prev.IsZero() {
		return 0, false
	}
	d := lastRun.Sub(prev)
	if d < 0 {
		return 0, false
	}
	return d, true
}

// rank orders candidates by how badly they need goguma.
func rank(cs []Candidate) {
	score := func(c Candidate) float64 {
		// Directly observed lateness outranks everything inferred. A job the
		// scheduler itself recorded as four hours behind is not a probability
		// estimate, it is a fact, and it belongs at the top of the list.
		// Scores above 1.0 keep it strictly ahead of any inferred risk ratio,
		// growing with the delay so the worst offender leads.
		if c.HasLateness && c.Lateness >= LatenessThreshold {
			return 1.0 + c.Lateness.Hours()
		}
		// Confident, frequently-missed entries rank next. Unknown risk sorts
		// between "definitely missed" and "rarely missed": it deserves
		// attention but should not outrank measured evidence.
		if !c.Risk.Confident {
			return 0.5
		}
		return c.Risk.Ratio
	}
	// Simple insertion sort: candidate lists are tiny by construction, and
	// this keeps entries with equal scores in discovery order.
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && score(cs[j]) > score(cs[j-1]); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

// MissedSummary renders the evidence behind a risk score as a phrase like
// "12 of 14 recent runs". This is what makes a recommendation arguable rather
// than something the user has to take on faith.
func MissedSummary(r schedule.MissRisk) string {
	if r.Fires == 0 {
		return "no recent runs to measure"
	}
	if !r.Confident {
		return "not enough history to tell yet"
	}
	if r.Missed == 0 {
		return "none of the last " + itoa(r.Fires) + " runs were missed"
	}
	return itoa(r.Missed) + " of the last " + itoa(r.Fires) + " runs were missed"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
