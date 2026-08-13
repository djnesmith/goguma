package schedule

import (
	"testing"
	"time"
)

func night(day int, fromHour, toHour int) SleepInterval {
	return SleepInterval{
		Sleep: time.Date(2026, 8, day, fromHour, 0, 0, 0, time.UTC),
		Wake:  time.Date(2026, 8, day+1, toHour, 0, 0, 0, time.UTC),
	}
}

// asleepEveryNight builds a history where the machine sleeps 23:00-08:00.
func asleepEveryNight(days int) *SleepHistory {
	var ivs []SleepInterval
	for d := 1; d <= days; d++ {
		ivs = append(ivs, night(d, 23, 8))
	}
	return &SleepHistory{
		Intervals: ivs,
		Since:     time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestMissRiskDetectsAlwaysMissedJob(t *testing.T) {
	hist := asleepEveryNight(14)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	// A 03:00 job fires squarely inside every sleep period.
	s := mustParse(t, "0 3 * * *")
	risk := EstimateMissRisk(s, hist, now, 14*24*time.Hour)

	if !risk.Confident {
		t.Fatalf("expected confidence from %d fires", risk.Fires)
	}
	if risk.Ratio != 1.0 {
		t.Errorf("ratio = %.2f, want 1.0 (every 3am run lands in a sleep period)", risk.Ratio)
	}
	if risk.Level() != RiskHigh {
		t.Errorf("level = %s, want high", risk.Level())
	}
}

func TestMissRiskDetectsNeverMissedJob(t *testing.T) {
	hist := asleepEveryNight(14)
	now := time.Date(2026, 8, 15, 20, 0, 0, 0, time.UTC)

	// A 14:00 job always fires while the machine is awake, so goguma has
	// nothing to contribute and import should filter it out.
	s := mustParse(t, "0 14 * * *")
	risk := EstimateMissRisk(s, hist, now, 14*24*time.Hour)

	if risk.Missed != 0 {
		t.Errorf("missed = %d, want 0", risk.Missed)
	}
	if risk.Level() != RiskNone {
		t.Errorf("level = %s, want none", risk.Level())
	}
}

func TestMissRiskReportsUnknownWithoutEnoughHistory(t *testing.T) {
	// One night of history cannot support a conclusion. Reporting "low risk"
	// here would hide exactly the jobs the user is trying to find, so absence
	// of evidence must surface as unknown.
	hist := &SleepHistory{
		Intervals: []SleepInterval{night(14, 23, 8)},
		Since:     time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
	}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	risk := EstimateMissRisk(mustParse(t, "0 3 * * *"), hist, now, 14*24*time.Hour)
	if risk.Confident {
		t.Errorf("should not be confident from %d fire(s)", risk.Fires)
	}
	if risk.Level() != RiskUnknown {
		t.Errorf("level = %s, want unknown", risk.Level())
	}
}

func TestMissRiskIsClampedToActualCoverage(t *testing.T) {
	// The history starts on the 10th, so a 14-day lookback must not pretend
	// to know what happened on the 1st.
	hist := &SleepHistory{
		Intervals: []SleepInterval{night(10, 23, 8), night(11, 23, 8), night(12, 23, 8)},
		Since:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	risk := EstimateMissRisk(mustParse(t, "0 3 * * *"), hist, now, 30*24*time.Hour)
	if risk.Fires > 4 {
		t.Errorf("evaluated %d fires, more than the ~3 days of history covers", risk.Fires)
	}
}

func TestEmptyHistoryYieldsNoConfidence(t *testing.T) {
	risk := EstimateMissRisk(mustParse(t, "0 3 * * *"), &SleepHistory{},
		time.Now(), 14*24*time.Hour)
	if risk.Confident || risk.Fires != 0 {
		t.Errorf("expected an empty result from empty history, got %+v", risk)
	}
}

func TestCoalesceShortGapsJoinsDarkWakes(t *testing.T) {
	base := time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC)

	// macOS punctuates a long sleep with 2-5 second maintenance DarkWakes.
	// Without coalescing, a 03:00 job landing in one of those gaps would be
	// scored "not missed", which is exactly backwards; the machine is not
	// usefully awake during a DarkWake.
	in := []SleepInterval{
		{Sleep: base, Wake: base.Add(2 * time.Hour)},
		{Sleep: base.Add(2*time.Hour + 3*time.Second), Wake: base.Add(5 * time.Hour)},
		{Sleep: base.Add(5*time.Hour + 2*time.Second), Wake: base.Add(9 * time.Hour)},
	}
	got := CoalesceShortGaps(MergeIntervals(in), DarkWakeGap)

	if len(got) != 1 {
		t.Fatalf("got %d intervals, want 1 continuous sleep", len(got))
	}
	if !got[0].Wake.Equal(base.Add(9 * time.Hour)) {
		t.Errorf("coalesced interval ends at %s, want %s", got[0].Wake, base.Add(9*time.Hour))
	}

	// A job firing during one of those 3-second gaps must now count as missed.
	h := &SleepHistory{Intervals: got, Since: base}
	if !h.AsleepAt(base.Add(2*time.Hour + 1*time.Second)) {
		t.Error("a fire time inside a DarkWake gap was not counted as asleep")
	}
}

func TestCoalesceKeepsGenuinelySeparateSleeps(t *testing.T) {
	base := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)

	// Two sleeps separated by two hours of real use must stay distinct, or
	// the machine would look asleep during a period it was actually in use.
	in := []SleepInterval{
		{Sleep: base, Wake: base.Add(time.Hour)},
		{Sleep: base.Add(3 * time.Hour), Wake: base.Add(4 * time.Hour)},
	}
	got := CoalesceShortGaps(MergeIntervals(in), DarkWakeGap)
	if len(got) != 2 {
		t.Fatalf("got %d intervals, want 2 separate sleeps", len(got))
	}

	h := &SleepHistory{Intervals: got, Since: base}
	if h.AsleepAt(base.Add(2 * time.Hour)) {
		t.Error("the machine was reported asleep during a period it was awake")
	}
}

func TestMergeIntervalsHandlesOverlap(t *testing.T) {
	base := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)

	// Log-derived records routinely nest a short wake inside a longer sleep;
	// double-counting those would inflate the measured sleep time.
	in := []SleepInterval{
		{Sleep: base, Wake: base.Add(4 * time.Hour)},
		{Sleep: base.Add(time.Hour), Wake: base.Add(2 * time.Hour)},
		{Sleep: base.Add(3 * time.Hour), Wake: base.Add(6 * time.Hour)},
	}
	got := MergeIntervals(in)
	if len(got) != 1 {
		t.Fatalf("got %d intervals, want 1 merged", len(got))
	}
	if !got[0].Wake.Equal(base.Add(6 * time.Hour)) {
		t.Errorf("merged interval ends at %s, want +6h", got[0].Wake)
	}
}

func TestOpenEndedIntervalMeansStillAsleep(t *testing.T) {
	base := time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC)
	iv := SleepInterval{Sleep: base}

	// A sleep with no recorded wake is the one the machine is currently
	// inside. Treating it as zero-length would report the machine awake
	// during a period it demonstrably is not.
	if !iv.Contains(base.Add(5 * time.Hour)) {
		t.Error("an open-ended interval should contain any later time")
	}
	if iv.Contains(base.Add(-time.Hour)) {
		t.Error("an open-ended interval should not contain an earlier time")
	}
}
