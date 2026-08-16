package scan

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// alwaysAsleepAt3am builds a history where the machine sleeps 23:00-08:00.
func alwaysAsleepAt3am(days int) *schedule.SleepHistory {
	var ivs []schedule.SleepInterval
	for d := 1; d <= days; d++ {
		ivs = append(ivs, schedule.SleepInterval{
			Sleep: time.Date(2026, 8, d, 23, 0, 0, 0, time.Local),
			Wake:  time.Date(2026, 8, d+1, 8, 0, 0, 0, time.Local),
		})
	}
	return &schedule.SleepHistory{
		Intervals: ivs,
		Since:     time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local),
	}
}

func evalOne(t *testing.T, e Entry) (Candidate, bool) {
	t.Helper()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)
	keep, filtered := Evaluate([]Entry{e}, alwaysAsleepAt3am(14), now, DefaultOptions())
	if len(keep) == 1 {
		return keep[0], true
	}
	if len(filtered) == 1 {
		return filtered[0], false
	}
	t.Fatalf("expected exactly one result, got keep=%d filtered=%d", len(keep), len(filtered))
	return Candidate{}, false
}

func TestSystemOwnedIsExcluded(t *testing.T) {
	c, kept := evalOne(t, Entry{
		Name: "apple-thing", Schedule: "0 3 * * *", SystemOwned: true,
	})
	if kept {
		t.Fatal("an OS-owned entry should be excluded")
	}
	if c.Reason != ReasonSystemOwned {
		t.Errorf("reason = %q, want %q", c.Reason, ReasonSystemOwned)
	}
}

func TestAlwaysRunningIsExcluded(t *testing.T) {
	// A resident service is already there when the machine wakes, so it is
	// never "missed" and waking for it accomplishes nothing.
	c, kept := evalOne(t, Entry{
		Name: "postgres", Schedule: "0 3 * * *", AlwaysRunning: true,
	})
	if kept || c.Reason != ReasonAlwaysRunning {
		t.Errorf("expected exclusion as always-running, got kept=%v reason=%q", kept, c.Reason)
	}
}

func TestNoScheduleIsExcluded(t *testing.T) {
	c, kept := evalOne(t, Entry{Name: "watcher", Schedule: ""})
	if kept || c.Reason != ReasonNoSchedule {
		t.Errorf("expected exclusion for no schedule, got kept=%v reason=%q", kept, c.Reason)
	}
}

func TestFrequentJobsAreKeptByDefault(t *testing.T) {
	// No frequency floor now. A five-minute job is expensive to wake for, and
	// that is the user's call to make with the cost in front of them rather
	// than the scanner's to make by never mentioning the job.
	c, kept := evalOne(t, Entry{Name: "poller", Schedule: "*/5 * * * *"})
	if !kept {
		t.Errorf("a frequent job was filtered out with no floor set: reason=%q", c.Reason)
	}
}

func TestAFrequencyFloorStillExcludesWhenSet(t *testing.T) {
	// The knob still works for anyone who wants it back, and the comparison
	// stays inclusive: a job at exactly the limit is excluded, because a
	// 30-minute job against a 30-minute floor is 48 wakes a day.
	opts := DefaultOptions()
	opts.MinInterval = 30 * time.Minute

	entries := []Entry{{Name: "poller", Source: SourceCrontab, Schedule: "*/30 * * * *"}}
	keep, filtered := Evaluate(entries, nil, time.Now(), opts)
	if len(keep) != 0 {
		t.Errorf("a 30m job survived a 30m floor")
	}
	if len(filtered) != 1 || filtered[0].Reason != ReasonTooFrequent {
		t.Errorf("filtered = %+v, want one entry reasoned too frequent", filtered)
	}
}

func TestSelfHealingIsExcluded(t *testing.T) {
	// launchd re-runs a missed calendar job on the next wake, so these are
	// late rather than lost.
	c, kept := evalOne(t, Entry{
		Name: "updater", Schedule: "0 3 * * *", SelfHealing: true, Source: SourceLaunchd,
	})
	if kept || c.Reason != ReasonSelfHealing {
		t.Errorf("expected exclusion as self-healing, got kept=%v reason=%q", kept, c.Reason)
	}
}

func TestNeverMissedIsExcluded(t *testing.T) {
	// The machine is reliably awake at 14:00, so goguma adds nothing.
	c, kept := evalOne(t, Entry{Name: "afternoon", Schedule: "0 14 * * *", Source: SourceCrontab})
	if kept || c.Reason != ReasonNeverMissed {
		t.Errorf("expected exclusion as never-missed, got kept=%v reason=%q", kept, c.Reason)
	}
}

func TestGenuinelyMissedCronJobIsKept(t *testing.T) {
	// The case the whole tool exists for: a cron job at 3am that is silently
	// skipped every night.
	c, kept := evalOne(t, Entry{
		Name: "nightly-backup", Schedule: "0 3 * * *", Source: SourceCrontab,
		Command: "restic backup /home",
	})
	if !kept {
		t.Fatalf("a nightly cron job that is always missed should be kept, got reason %q", c.Reason)
	}
	if c.Risk.Ratio != 1.0 {
		t.Errorf("miss ratio = %.2f, want 1.0", c.Risk.Ratio)
	}
}

func TestUnknownRiskIsKeptNotDiscarded(t *testing.T) {
	// With no sleep history there is no evidence either way. Discarding on
	// "no evidence of being missed" would hide exactly what the user is
	// looking for, so unknown risk must survive the filter.
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)
	keep, _ := Evaluate(
		[]Entry{{Name: "mystery", Schedule: "0 3 * * *", Source: SourceCrontab}},
		&schedule.SleepHistory{}, now, DefaultOptions())

	if len(keep) != 1 {
		t.Fatalf("an entry with unknown risk should be kept, got %d", len(keep))
	}
	if keep[0].Risk.Confident {
		t.Error("risk should be reported as not confident")
	}
}

func TestRankingPutsMostMissedFirst(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)

	// Asleep 01:00-06:00 every night, and additionally 22:00-23:00 on only
	// half the nights. A 03:00 job is therefore always missed, while a 22:30
	// job is missed about half the time, so their ranking is unambiguous
	// rather than a tie broken by discovery order.
	hist := alwaysAsleepAt3am(0)
	for d := 1; d <= 14; d++ {
		hist.Intervals = append(hist.Intervals, schedule.SleepInterval{
			Sleep: time.Date(2026, 8, d, 1, 0, 0, 0, time.Local),
			Wake:  time.Date(2026, 8, d, 6, 0, 0, 0, time.Local),
		})
		if d%2 == 0 {
			hist.Intervals = append(hist.Intervals, schedule.SleepInterval{
				Sleep: time.Date(2026, 8, d, 22, 0, 0, 0, time.Local),
				Wake:  time.Date(2026, 8, d, 23, 0, 0, 0, time.Local),
			})
		}
	}

	entries := []Entry{
		{Name: "sometimes-missed", Schedule: "30 22 * * *", Source: SourceCrontab},
		{Name: "always-missed", Schedule: "0 3 * * *", Source: SourceCrontab},
	}
	keep, filtered := Evaluate(entries, hist, now, DefaultOptions())
	if len(keep) != 2 {
		t.Fatalf("expected both to survive; kept %d, filtered %d", len(keep), len(filtered))
	}
	if keep[0].Name != "always-missed" {
		t.Errorf("first candidate is %q (ratio %.2f), want always-missed (ratio %.2f) first",
			keep[0].Name, keep[0].Risk.Ratio, keep[1].Risk.Ratio)
	}
	if keep[0].Risk.Ratio <= keep[1].Risk.Ratio {
		t.Errorf("ranking is not by descending miss ratio: %.2f then %.2f",
			keep[0].Risk.Ratio, keep[1].Risk.Ratio)
	}
}

func TestFilteringScalesDownARealisticMachine(t *testing.T) {
	// The motivating scenario: hundreds of launchd entries, of which almost
	// none are the user's missable automation.
	var entries []Entry
	for i := range 500 {
		entries = append(entries, Entry{
			Name: "apple-service", Schedule: "0 3 * * *", SystemOwned: true, Source: SourceLaunchd,
		})
		_ = i
	}
	for range 10 {
		entries = append(entries, Entry{
			Name: "resident", Schedule: "", AlwaysRunning: true, Source: SourceLaunchd,
		})
	}
	entries = append(entries, Entry{
		Name: "nightly-backup", Schedule: "0 3 * * *", Source: SourceCrontab,
	})

	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local)
	keep, filtered := Evaluate(entries, alwaysAsleepAt3am(14), now, DefaultOptions())

	if len(keep) != 1 {
		t.Errorf("kept %d candidates from %d entries, want exactly the 1 real job",
			len(keep), len(entries))
	}
	if len(filtered) != len(entries)-1 {
		t.Errorf("filtered %d, want %d", len(filtered), len(entries)-1)
	}
}

func TestMissedSummaryWording(t *testing.T) {
	tests := []struct {
		risk schedule.MissRisk
		want string
	}{
		{schedule.MissRisk{}, "no recent runs to measure"},
		{schedule.MissRisk{Fires: 2, Missed: 1}, "not enough history to tell yet"},
		// Conditional, not past tense. This counts fires that landed during
		// sleep whether goguma was installed or not, so stating it as fact
		// contradicted `goguma list`, which reports what goguma has actually
		// let through and is usually zero.
		{schedule.MissRisk{Fires: 14, Missed: 0, Confident: true},
			"none of the last 14 runs would have been missed"},
		{schedule.MissRisk{Fires: 14, Missed: 12, Confident: true},
			"12 of the last 14 runs would have been missed without goguma"},
	}
	for _, tc := range tests {
		if got := MissedSummary(tc.risk); got != tc.want {
			t.Errorf("MissedSummary(%+v) = %q, want %q", tc.risk, got, tc.want)
		}
	}
}

func TestParseCrontab(t *testing.T) {
	text := `# a comment
PATH=/usr/bin:/bin
MAILTO=""

0 9 * * * hermes cron run morning-briefing
*/5 * * * * /usr/local/bin/poll.sh >/dev/null 2>&1
@daily /opt/backup.sh
@reboot /opt/startup.sh
not a real line
`
	entries := ParseCrontab(text)
	if len(entries) != 4 {
		t.Fatalf("parsed %d entries, want 4: %+v", len(entries), entries)
	}

	if entries[0].Schedule != "0 9 * * *" {
		t.Errorf("schedule = %q, want %q", entries[0].Schedule, "0 9 * * *")
	}
	if entries[0].Command != "hermes cron run morning-briefing" {
		t.Errorf("command = %q", entries[0].Command)
	}
	// Environment assignments must not be read as jobs.
	for _, e := range entries {
		if e.Command == "" {
			t.Errorf("entry with empty command: %+v", e)
		}
	}
	// @reboot has no fire time to wake for.
	var reboot *Entry
	for i := range entries {
		if entries[i].Schedule == "@reboot" {
			reboot = &entries[i]
		}
	}
	if reboot == nil {
		t.Fatal("@reboot line was not parsed")
	}
	if !reboot.SelfHealing {
		t.Error("@reboot should be marked as not needing a wake")
	}
}

func TestSixFieldCrontabIsRefusedNotMisparsed(t *testing.T) {
	// A six-field (seconds-precision) line splits at five fields into a
	// schedule that is silently wrong but still valid: "0 0 9 * * * cmd"
	// becomes "0 0 9 * *": 00:00 on the 9th of each month, not 09:00:00
	// daily. goguma would wake the machine at confidently incorrect times,
	// which is worse than not handling the line at all.
	entries := ParseCrontab("0 0 9 * * * /opt/six-field.sh")
	if len(entries) != 0 {
		t.Errorf("six-field line was parsed as %q + %q; it must be refused rather than guessed",
			entries[0].Schedule, entries[0].Command)
	}
}

func TestFiveFieldLinesStillParse(t *testing.T) {
	// The ambiguity check must not reject legitimate five-field lines whose
	// command happens to start with something unusual.
	for _, line := range []string{
		"0 9 * * * hermes cron run briefing",
		"*/5 * * * * /usr/local/bin/poll.sh",
		"30 2 * * 1-5 /opt/weekday.sh --flag",
		"0,30 * * * * echo hi",
	} {
		if got := ParseCrontab(line); len(got) != 1 {
			t.Errorf("ParseCrontab(%q) returned %d entries, want 1", line, len(got))
		}
	}
}

func TestLooksLikeCronField(t *testing.T) {
	for _, tok := range []string{"*", "*/5", "1-5", "0,30", "15", "0"} {
		if !looksLikeCronField(tok) {
			t.Errorf("looksLikeCronField(%q) = false, want true", tok)
		}
	}
	// Real commands must never be mistaken for schedule fields.
	for _, tok := range []string{"/opt/x.sh", "hermes", "python3", "echo", "-v", ""} {
		if looksLikeCronField(tok) {
			t.Errorf("looksLikeCronField(%q) = true, want false", tok)
		}
	}
}

func TestNameFromCommand(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"hermes cron run morning-briefing", "hermes-cron-run"},
		{"/usr/local/bin/backup.sh", "backup"},
		{"/bin/sh -c /opt/thing.sh", "thing"},
		{"python3 /opt/scripts/report.py", "report"},
	}
	for _, tc := range tests {
		if got := nameFromCommand(tc.cmd); got != tc.want {
			t.Errorf("nameFromCommand(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
