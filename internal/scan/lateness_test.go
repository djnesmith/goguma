package scan

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// TestObservedLatenessBeatsInference is the case that motivated this whole
// mechanism.
//
// A user's morning briefing is scheduled for 09:00. Its own scheduler records
// that it last ran at 12:52, nearly four hours late, because the machine was
// asleep at 09:00 and the job only ran once the lid opened. That is directly
// observed evidence, and it must surface even though the scheduler technically
// "caught up" and would otherwise be filtered as self-healing.
func TestObservedLatenessBeatsInference(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	entry := Entry{
		Name:     "Morning briefing",
		Source:   SourceHermes,
		Schedule: "0 9 * * *",
		// The scheduler catches up after a missed window, so without observed
		// lateness this would be excluded.
		SelfHealing:   true,
		TimeSensitive: true,
		LastRun:       time.Date(2026, 8, 4, 12, 52, 0, 0, time.Local),
	}

	keep, filtered := Evaluate([]Entry{entry}, &schedule.SleepHistory{}, now, DefaultOptions())
	if len(keep) != 1 {
		t.Fatalf("a job observed running 4h late should be kept; kept=%d filtered=%d (reason %q)",
			len(keep), len(filtered), reasonOf(filtered))
	}
	c := keep[0]
	if !c.HasLateness {
		t.Fatal("lateness should have been computed from last_run_at")
	}
	want := 3*time.Hour + 52*time.Minute
	if c.Lateness != want {
		t.Errorf("lateness = %s, want %s", c.Lateness, want)
	}
}

func TestPunctualJobIsNotFlaggedAsLate(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	// Ran at 09:00:12 for a 09:00 schedule, twelve seconds of scheduler
	// jitter, not a problem.
	entry := Entry{
		Name: "punctual", Source: SourceHermes, Schedule: "0 9 * * *",
		SelfHealing: true, TimeSensitive: true,
		LastRun: time.Date(2026, 8, 4, 9, 0, 12, 0, time.Local),
	}
	keep, filtered := Evaluate([]Entry{entry}, &schedule.SleepHistory{}, now, DefaultOptions())
	if len(keep) != 0 {
		t.Errorf("a job that ran on time should not be surfaced (lateness %s)",
			keep[0].Lateness)
	}
	if len(filtered) != 1 || filtered[0].Reason != ReasonSelfHealing {
		t.Errorf("expected exclusion as self-healing, got %q", reasonOf(filtered))
	}
}

func TestIntervalJobLatenessIsNotTreatedAsUrgent(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	// An interval schedule only asks to happen periodically; running late
	// costs far less than for a job that names a specific time. It should not
	// be promoted past the self-healing filter on lateness alone.
	entry := Entry{
		Name: "watchdog", Source: SourceHermes, Schedule: "every 6h",
		SelfHealing: true, TimeSensitive: false,
		LastRun: time.Date(2026, 8, 4, 14, 0, 0, 0, time.Local),
	}
	keep, filtered := Evaluate([]Entry{entry}, &schedule.SleepHistory{}, now, DefaultOptions())
	if len(keep) != 0 {
		t.Errorf("an interval job should not be promoted on lateness alone")
	}
	if len(filtered) != 1 {
		t.Fatalf("expected it to be filtered, got %d", len(filtered))
	}
}

func TestLatenessRanksAboveInferredRisk(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	// One job with hard evidence of being 10 hours late, one with an inferred
	// 100% miss ratio. Observation should outrank inference.
	var ivs []schedule.SleepInterval
	for d := 1; d <= 14; d++ {
		ivs = append(ivs, schedule.SleepInterval{
			Sleep: time.Date(2026, 8, d, 1, 0, 0, 0, time.Local),
			Wake:  time.Date(2026, 8, d, 6, 0, 0, 0, time.Local),
		})
	}
	hist := &schedule.SleepHistory{Intervals: ivs, Since: time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)}

	entries := []Entry{
		{Name: "inferred", Source: SourceCrontab, Schedule: "0 3 * * *", TimeSensitive: true},
		{
			Name: "observed", Source: SourceHermes, Schedule: "0 9 * * *",
			TimeSensitive: true, SelfHealing: true,
			LastRun: time.Date(2026, 8, 4, 19, 30, 0, 0, time.Local),
		},
	}
	keep, _ := Evaluate(entries, hist, now, DefaultOptions())
	if len(keep) != 2 {
		t.Fatalf("expected both to survive, got %d", len(keep))
	}
	if keep[0].Name != "observed" {
		t.Errorf("first is %q; directly observed lateness should outrank an inferred ratio",
			keep[0].Name)
	}
}

func TestObservedLatenessHandlesMultiFireSchedules(t *testing.T) {
	// "0 9,21 * * *" fires twice daily. A run at 15:12 was fulfilling the
	// 09:00 fire, not the 21:00 one, so lateness is 6h12m rather than negative.
	s, err := schedule.Parse("0 9,21 * * *", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	lastRun := time.Date(2026, 8, 4, 15, 12, 0, 0, time.Local)
	got, ok := observedLateness(s, lastRun)
	if !ok {
		t.Fatal("expected lateness to be computable")
	}
	want := 6*time.Hour + 12*time.Minute
	if got != want {
		t.Errorf("lateness = %s, want %s", got, want)
	}
}

func TestObservedLatenessWithoutARecordIsUnknown(t *testing.T) {
	s, err := schedule.Parse("0 9 * * *", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	// Most schedulers keep no last-run record at all; that must read as
	// "unknown", never as "on time".
	if _, ok := observedLateness(s, time.Time{}); ok {
		t.Error("a zero last-run time should not produce a lateness value")
	}
	if _, ok := observedLateness(nil, time.Now()); ok {
		t.Error("a nil schedule should not produce a lateness value")
	}
}

func TestSchedulerNextRunWins(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	// An interval schedule counts from its last run, not from now, so
	// recomputing it here would disagree with what the scheduler will
	// actually do. The scheduler's own value must win.
	authoritative := time.Date(2026, 8, 5, 2, 7, 0, 0, time.Local)
	entry := Entry{
		Name: "watchdog", Source: SourceHermes, Schedule: "every 6h",
		NextRun: authoritative,
	}
	keep, filtered := Evaluate([]Entry{entry}, &schedule.SleepHistory{}, now, DefaultOptions())
	all := append(keep, filtered...)
	if len(all) != 1 {
		t.Fatalf("expected one result, got %d", len(all))
	}
	if !all[0].NextFire.Equal(authoritative) {
		t.Errorf("NextFire = %s, want the scheduler's own %s", all[0].NextFire, authoritative)
	}
}

func reasonOf(cs []Candidate) Reason {
	if len(cs) == 0 {
		return ""
	}
	return cs[0].Reason
}

func TestUnsupportedDialectIsRefusedNotMisread(t *testing.T) {
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.Local)

	// "0 0 * * 1" is valid in both dialects and means different days: Monday
	// under POSIX cron, Sunday under Quartz. Nothing in the string reveals
	// which. Parsing it under the wrong semantics would wake the machine on
	// the wrong day and report success, so it must be refused instead.
	entry := Entry{
		Name: "quartz-job", Source: "some-scheduler",
		Schedule: "0 0 * * 1", Dialect: DialectQuartz, TimeSensitive: true,
	}
	keep, filtered := Evaluate([]Entry{entry}, &schedule.SleepHistory{}, now, DefaultOptions())
	if len(keep) != 0 {
		t.Fatal("a Quartz schedule must not be evaluated under POSIX cron semantics")
	}
	if len(filtered) != 1 || filtered[0].Reason != ReasonUnsupportedDialect {
		t.Errorf("reason = %q, want %q", reasonOf(filtered), ReasonUnsupportedDialect)
	}
}

func TestDefaultDialectIsCron(t *testing.T) {
	// Every current provider emits POSIX cron, so the zero value must be
	// usable rather than requiring each one to set it explicitly.
	var d Dialect
	if !d.Supported() {
		t.Error("the zero dialect must be treated as supported POSIX cron")
	}
}
