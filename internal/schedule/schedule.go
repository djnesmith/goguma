// Package schedule parses the schedule expressions WakeGuard accepts and
// computes fire times. WakeGuard never fires anything itself — these times
// are used only to decide when the machine must be awake, so being slightly
// early is harmless and being late means a missed job.
package schedule

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule is a parsed, timezone-bound schedule.
type Schedule struct {
	// Expr is the normalised expression, as stored on the Job.
	Expr string
	// Display is a human sentence for the CLI and GUI ("daily at 09:00").
	Display string
	// DisplayShort is the cadence without the clock detail ("twice daily"),
	// for the narrow places — a job row, a table column — that sit next to a
	// "next run" time. There the clock readings in Display only repeat what is
	// already on screen.
	DisplayShort string

	loc   *time.Location
	inner cron.Schedule

	// interval is set for @every-style schedules, where the gap between fires
	// is a fixed duration. Import uses it to reject high-frequency jobs, for
	// which waking the machine costs more than the job is worth.
	interval time.Duration

	// delay is the gap for schedules whose fire times are *only* a gap —
	// "@every 6h" and the "every 6h" spelling of it. Those are the ones with
	// no phase of their own, so they are the ones that need an anchor.
	//
	// It is deliberately narrower than interval, which is also set for
	// @hourly/@daily/@weekly so that import can price them. Those descriptors
	// parse to a calendar schedule — midnight, the top of the hour — and they
	// already know when they fire. Anchoring them would move @daily off
	// midnight to whenever the job happened to be created, which is not what
	// the user wrote and not what their real cron will do.
	delay time.Duration

	// anchor is the origin the delay is measured from: fires land on
	// anchor + n*delay. See ParseAt for why it must be supplied by the caller,
	// and for what it can and cannot promise.
	anchor time.Time
}

// processStart is the fallback anchor for interval schedules parsed without
// one, captured once at package initialisation.
//
// The value hardly matters; that it never moves is the entire point. Parse
// used to leave interval schedules unanchored, so cron's own relative Next
// answered "t plus six hours" for whatever t the caller passed. A daemon that
// re-parses every job on every tick therefore pushed the next fire time
// forward by exactly the time between ticks — the countdown never counted
// down, and the OS wake was re-registered a tick further out on every tick,
// so it never arrived.
var processStart = time.Now()

// parser accepts standard 5-field crontab syntax plus @descriptors. Seconds
// are deliberately not enabled: WakeGuard's whole reason to exist is jobs
// infrequent enough that the machine sleeps between them, and accepting a
// 6-field form invites confusion with crontab lines pasted from a real
// crontab, where field 1 is minutes.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// Parse normalises and parses a schedule expression in the given zone.
// Accepts crontab syntax ("0 9 * * *"), descriptors ("@daily"), and the
// friendlier interval forms "every 6h" / "every 360m".
//
// Interval schedules get the process-start anchor, which is the right choice
// only for callers that have nothing better — a schedule being validated as
// the user types it, or one belonging to no job yet. Any caller holding a Job
// should use ParseAt, so that its answer matches the daemon's.
func Parse(expr string, loc *time.Location) (*Schedule, error) {
	return ParseAt(expr, loc, time.Time{})
}

// ParseAt is Parse with an explicit origin for interval schedules.
//
// An interval schedule says how often, never when: "every 6h" has a cadence
// and no phase. The anchor supplies the phase — fires land on anchor + n*6h —
// and it has to come from the caller, because the only sensible source for it
// is the job, and this package deliberately knows nothing about jobs.
//
// Whatever anchor is chosen must be chosen the same way everywhere. The GUI's
// countdown and the daemon's wake scheduler both display and act on this
// number, so two callers anchoring differently would mean the machine wakes at
// a time the user was never shown.
//
// The limitation, stated plainly: for a job WakeGuard adopted from another
// scheduler, the best anchor available is the job's CreatedAt, which is when
// WakeGuard first saw the job — not when that scheduler's own interval clock
// started. The two coincide only by luck, so the fire times computed here are
// an estimate that can be up to one full interval out of phase with what
// actually runs. That is still worth computing: the cadence is right, the wake
// buffer absorbs part of the error, and a stable estimate is the only kind a
// wake can be scheduled from. It is not a claim about the other scheduler's
// clock, and nothing downstream should present it as one.
//
// A zero anchor means "none available" and falls back to process start.
// Calendar schedules ignore the anchor entirely; their phase is in the
// expression itself.
func ParseAt(expr string, loc *time.Location, anchor time.Time) (*Schedule, error) {
	if loc == nil {
		loc = time.Local
	}
	norm, interval, err := normalize(expr)
	if err != nil {
		// Before giving up, try reading it as plain English. A person knows
		// when they want something to run long before they know how to write
		// it in cron, and the translation is mechanical.
		if human, ok := FromHuman(expr); ok {
			if norm, interval, err = normalize(human); err == nil {
				if inner, perr := parser.Parse(norm); perr == nil {
					return newSchedule(human, norm, loc, inner, interval, anchor), nil
				}
			}
		}
		return nil, err
	}
	inner, err := parser.Parse(norm)
	if err != nil {
		// Same fallback for input that tokenises like cron but is not.
		if human, ok := FromHuman(expr); ok {
			if hnorm, hinterval, herr := normalize(human); herr == nil {
				if hinner, perr := parser.Parse(hnorm); perr == nil {
					return newSchedule(human, hnorm, loc, hinner, hinterval, anchor), nil
				}
			}
		}
		return nil, fmt.Errorf("invalid schedule %q: %w", expr, err)
	}
	s := newSchedule(strings.TrimSpace(expr), norm, loc, inner, interval, anchor)

	// Reject an expression that is syntactically valid but can never occur.
	//
	// "0 0 31 4 *" is April 31st and "0 0 30 2 *" is February 30th. Both parse
	// cleanly and both are easy to write by accident, but neither has a next
	// fire time — the parser returns the zero time forever. A job built on one
	// would sit in the list looking scheduled while never running, which is
	// exactly the silent failure this tool exists to prevent. It also used to
	// crash the CLI outright, because the zero time renders as a duration that
	// overflows.
	if s.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf(
			"schedule %q has no future occurrences, so it would never run "+
				"(check the day and month, for example April has no 31st)", expr)
	}
	return s, nil
}

// newSchedule assembles a Schedule from an already-parsed expression.
//
// Every construction path goes through here on purpose. There are four of
// them — the plain form, and the plain-English fallback on each of two error
// paths — and a field set in three of the four is a field that is silently
// zero in whichever one the next reader forgets. The anchor is exactly such a
// field: an unanchored interval schedule does not fail, it quietly goes back
// to sliding its fire time forward on every call.
func newSchedule(expr, norm string, loc *time.Location, inner cron.Schedule, interval time.Duration, anchor time.Time) *Schedule {
	full, short := describe(expr, norm, interval)
	s := &Schedule{
		Expr:         expr,
		Display:      full,
		DisplayShort: short,
		loc:          loc,
		inner:        inner,
		interval:     interval,
		anchor:       normalizeAnchor(anchor),
	}

	// Take the delay from the parsed schedule rather than from the interval
	// the normaliser reported. The two differ where it matters: interval is
	// also set for @hourly and @daily, which parse to calendar schedules and
	// must keep firing on the hour and at midnight, and cron's own Every()
	// rounds the duration down to whole seconds and floors it at one second.
	// The type is the honest test of "does this schedule's Next depend on what
	// you pass it", which is the property being corrected.
	if cds, ok := inner.(cron.ConstantDelaySchedule); ok && cds.Delay > 0 {
		s.delay = cds.Delay
	}
	return s
}

// normalizeAnchor strips what must not survive into stored state.
//
// The monotonic reading comes first, because it is the dangerous one. A
// caller's anchor usually starts life as time.Now(), which carries one, and
// Sub uses monotonic readings exclusively when both operands have them. The
// monotonic clock does not advance while the machine is asleep — the same trap
// the daemon's sleep detection documents — so an anchor holding one would
// measure a six-hour sleep as no elapsed time at all and the fire time would
// never arrive. Truncating to the second strips it, and also drops a
// nanosecond tail that would otherwise appear in every fire time on screen.
func normalizeAnchor(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.Truncate(time.Second)
}

// normalize maps WakeGuard's friendly forms onto what the cron parser
// accepts, and reports a fixed interval when the schedule has one.
func normalize(expr string) (string, time.Duration, error) {
	s := strings.TrimSpace(expr)
	if s == "" {
		return "", 0, fmt.Errorf("empty schedule")
	}
	lower := strings.ToLower(s)

	// "every 6h" / "every 360m" -> "@every 6h"
	if rest, ok := strings.CutPrefix(lower, "every "); ok {
		rest = strings.TrimSpace(rest)
		d, err := time.ParseDuration(strings.ReplaceAll(rest, " ", ""))
		if err != nil {
			return "", 0, fmt.Errorf("invalid interval %q in %q (try: every 6h)", rest, expr)
		}
		if d <= 0 {
			return "", 0, fmt.Errorf("interval in %q must be positive", expr)
		}
		return "@every " + d.String(), d, nil
	}
	if rest, ok := strings.CutPrefix(lower, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return "", 0, fmt.Errorf("invalid interval in %q", expr)
		}
		return lower, d, nil
	}

	// Fixed-period descriptors have a known interval too, which lets import
	// filter @hourly the same way it filters "every 30m".
	switch lower {
	case "@hourly":
		return lower, time.Hour, nil
	case "@daily", "@midnight":
		return lower, 24 * time.Hour, nil
	case "@weekly":
		return lower, 7 * 24 * time.Hour, nil
	case "@monthly", "@yearly", "@annually":
		return lower, 0, nil
	case "@reboot":
		// launchd/cron @reboot has no fire time to wake for — the machine is
		// by definition already awake when it runs.
		return "", 0, fmt.Errorf("@reboot has no scheduled time to wake for")
	}
	return normalizeDayOfWeek(s), 0, nil
}

// normalizeDayOfWeek rewrites Sunday-as-7 into Sunday-as-0.
//
// POSIX and vixie cron — the cron that actually runs on macOS and Linux —
// accept day-of-week 0-7 with both 0 and 7 meaning Sunday. The parser used
// here follows the stricter 0-6 range and rejects 7 outright.
//
// That mismatch is the worst kind of failure for this tool: a legitimate,
// currently-working crontab line would be reported as unparseable, dropped
// from the job list, and never woken for. The user's cron would keep running
// it while WakeGuard silently ignored it.
//
// Only the fifth field is touched, and only where 7 appears as a value rather
// than as part of a step, so "*/7" and every other field are left alone.
func normalizeDayOfWeek(expr string) string {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr
	}
	dow := fields[4]
	if !strings.Contains(dow, "7") {
		return expr
	}

	parts := strings.Split(dow, ",")
	for i, p := range parts {
		parts[i] = expandSundaySeven(p)
	}
	fields[4] = strings.Join(parts, ",")
	return strings.Join(fields, " ")
}

// Next returns the next fire time strictly after t.
func (s *Schedule) Next(t time.Time) time.Time {
	if s.delay > 0 {
		return s.nextFromAnchor(t)
	}
	return s.inner.Next(t.In(s.loc))
}

// nextFromAnchor places t on the lattice of fire times measured from the
// anchor, for schedules that are nothing but a repeating gap.
//
// cron.ConstantDelaySchedule.Next(t) answers t+delay for any t at all: it is
// relative to whoever asked, and a Schedule built fresh on each call has no
// memory of an origin to be relative to. WakeGuard builds one on every poll
// and on every daemon tick, so "every 6h" reported a next fire six hours from
// *now*, every time it was asked. The countdown slid forward instead of
// counting down, and because the same value drives the OS wake, the daemon
// cancelled and re-registered the wake a tick further out on every tick. The
// wake never arrived and the machine was never woken for an interval job.
//
// Measuring from a fixed anchor is what makes two calls a second apart return
// the same answer, which is the whole requirement.
func (s *Schedule) nextFromAnchor(t time.Time) time.Time {
	t = t.In(s.loc)
	anchor := s.usableAnchor(t)

	// An anchor in the future is itself the first fire — a job does not run
	// before it exists. Reached by a clock that stepped backwards or a
	// hand-edited created_at, and the alternative, extending the lattice
	// backwards past the anchor, would hand back a fire time in the past. The
	// daemon reads a past fire as due right now, so it would open a window
	// immediately and hold the machine awake for a job that is not coming.
	if anchor.After(t) {
		return anchor.In(s.loc)
	}

	// Strictly after t, including when t lands exactly on a fire. Integer
	// division truncates and the +1 steps past the boundary, so Next never
	// returns t itself — callers that walk a series by feeding the result back
	// in (NextN, pastFires) would otherwise spin on the same instant forever.
	n := int64(t.Sub(anchor)/s.delay) + 1
	return anchor.Add(time.Duration(n) * s.delay).In(s.loc)
}

// maxAnchorDistance bounds how far an anchor may sit from the time being asked
// about before it is treated as unusable.
//
// This is an overflow guard, not a policy. time.Duration tops out near 292
// years; Sub saturates there silently rather than reporting anything, and the
// multiplication back out then wraps to a negative offset. The result would be
// a fire time in the distant past, which the daemon reads as "due now" and
// would act on by pinning the machine awake indefinitely. A created_at a
// century out is corruption rather than a date, so it is treated as no anchor
// at all — the schedule keeps its cadence and loses only a phase that was
// never real.
const maxAnchorDistance = 100 * 365 * 24 * time.Hour

func (s *Schedule) usableAnchor(t time.Time) time.Time {
	a := s.anchor
	if a.IsZero() || t.Sub(a) > maxAnchorDistance || a.Sub(t) > maxAnchorDistance {
		return processStart
	}
	return a
}

// NextN returns the next n fire times after t, for the GUI's upcoming-runs
// list and for `list --upcoming`.
func (s *Schedule) NextN(t time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	cur := t
	for range n {
		cur = s.Next(cur)
		if cur.IsZero() {
			break
		}
		out = append(out, cur)
	}
	return out
}

// AnchoredInterval reports the gap for a schedule that is nothing but a
// repeating gap ("every 6h", "@every 6h"), and whether this is one.
//
// Deliberately distinct from Interval, which also answers for @hourly and
// @daily. Those declare a period but parse to a calendar schedule and fire at
// a known time of day. The difference is exactly the difference between a fire
// time that is known and one that is inferred: an anchored interval's phase
// comes from the anchor its caller supplied and is only ever as good as that
// anchor, so a caller telling the user when a job will run may want to say so.
// See ParseAt.
func (s *Schedule) AnchoredInterval() (time.Duration, bool) {
	return s.delay, s.delay > 0
}

// Interval reports the fixed gap between fires, and whether there is one.
// Calendar schedules ("0 9 * * *") report false even though they are
// effectively daily, because the gap is not constant across DST boundaries.
func (s *Schedule) Interval() (time.Duration, bool) {
	return s.interval, s.interval > 0
}

// TypicalInterval estimates the gap between fires for any schedule by
// sampling upcoming times. Import uses this to filter high-frequency jobs
// even when they are written in calendar form ("*/5 * * * *"), which has no
// declared interval but fires every five minutes.
func (s *Schedule) TypicalInterval(from time.Time) time.Duration {
	if d, ok := s.Interval(); ok {
		return d
	}
	times := s.NextN(from, 6)
	if len(times) < 2 {
		return 0
	}
	// The minimum observed gap is the right statistic: a job that fires at
	// 09:00 and 09:05 daily must be treated as five-minutes-apart for the
	// purpose of "is a dedicated wake worth it", not as daily.
	min := times[1].Sub(times[0])
	for i := 2; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < min {
			min = gap
		}
	}
	return min
}

// Location is the zone this schedule is evaluated in.
func (s *Schedule) Location() *time.Location { return s.loc }

// expandSundaySeven rewrites one day-of-week term that uses 7 for Sunday.
//
// Simple substitution is not enough. A step range like "0-7/2" was skipped
// entirely because of its slash, so the 7 reached a parser that rejects it and
// a valid vixie crontab line was dropped as unparseable — the exact failure
// this normalisation exists to prevent. And "7-7" became "7-6,0", which is a
// backwards range and also rejected.
//
// Enumerating the term removes the guesswork: the set is computed with vixie's
// rule that 7 and 0 are both Sunday, then emitted explicitly.
func expandSundaySeven(term string) string {
	if !strings.Contains(term, "7") {
		return term
	}
	base, stepStr, hasStep := strings.Cut(term, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepStr)
		if err != nil || n <= 0 {
			return term // leave it alone; the parser will report it
		}
		step = n
	}

	lo, hi, err := dowBounds(base)
	if err != nil {
		return term
	}

	seen := map[int]bool{}
	var out []int
	for v := lo; v <= hi; v += step {
		d := v
		if d == 7 {
			d = 0 // vixie folds 7 onto Sunday
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return term
	}
	sort.Ints(out)

	strs := make([]string, len(out))
	for i, d := range out {
		strs[i] = strconv.Itoa(d)
	}
	return strings.Join(strs, ",")
}

// dowBounds reads a day-of-week term as an inclusive numeric range.
func dowBounds(base string) (lo, hi int, err error) {
	if base == "*" {
		return 0, 6, nil
	}
	if l, h, found := strings.Cut(base, "-"); found {
		lo, err = strconv.Atoi(l)
		if err != nil {
			return 0, 0, err
		}
		hi, err = strconv.Atoi(h)
		if err != nil {
			return 0, 0, err
		}
		if lo > hi {
			return 0, 0, fmt.Errorf("reversed range")
		}
		return lo, hi, nil
	}
	v, err := strconv.Atoi(base)
	if err != nil {
		return 0, 0, err
	}
	return v, v, nil
}

// model_HumanDuration is duplicated rather than imported to keep this package
// free of a dependency on internal/model, which imports nothing itself and
// should stay that way.
func model_HumanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int64(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int64(d.Minutes()))
	case d%(24*time.Hour) == 0 && d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int64(d.Hours()/24))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int64(d.Hours()))
	default:
		return fmt.Sprintf("%dh %dm", int64(d.Hours()), int64(d.Minutes())%60)
	}
}
