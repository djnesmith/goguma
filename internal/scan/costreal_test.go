package scan

import (
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/schedule"
)

// TestRealWorldCost prices the job set actually found on the development
// machine, so the estimate is sanity-checked against something real rather
// than only against synthetic inputs.
func TestRealWorldCost(t *testing.T) {
	// wake_buffer 90s + wake_only_hold 3m
	const perWake = 90*time.Second + 3*time.Minute

	set := []struct{ name, sched string }{
		{"Morning briefing: AI/Tech", "0 9 * * *"},
		{"Morning briefing: Finance", "0 9 * * *"},
		{"Morning briefing: Global News", "0 9 * * *"},
		{"FloppyData balance watchdog", "every 6h"},
		{"Revenue tracker", "every 6h"},
		{"Unanswered emails digest", "0 9,21 * * *"},
	}
	var cands []Candidate
	for _, j := range set {
		s, err := schedule.Parse(j.sched, time.Local)
		if err != nil {
			t.Fatalf("parse %q: %v", j.sched, err)
		}
		cands = append(cands, Candidate{
			Entry:  Entry{Name: j.name, Schedule: j.sched},
			Parsed: s,
		})
	}

	c := EstimateCost(cands, time.Now(), perWake)
	t.Logf("cost: %s", c.Summary())
	t.Logf("busiest: %s at %.1f wakes/day", c.Busiest, c.BusiestWakes)
	t.Logf("heavy (would warn)? %v", c.Heavy())

	// Three briefings share 09:00, so they cost one wake between them, not
	// three. Plus a twice-daily digest and two six-hourly jobs.
	if c.WakesPerDay < 5 || c.WakesPerDay > 20 {
		t.Errorf("WakesPerDay = %.1f, outside the plausible 5-20 range", c.WakesPerDay)
	}
	if c.AwakePerDay <= 0 || c.AwakePerDay > 2*time.Hour {
		t.Errorf("AwakePerDay = %s, implausible", c.AwakePerDay)
	}
}

func TestHighFrequencyJobWouldBeFlaggedHeavy(t *testing.T) {
	// The every-30-minute check that the frequency filter now excludes. If it
	// ever slipped through, the cost report must at least shout about it.
	s, err := schedule.Parse("every 30m", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	c := EstimateCost([]Candidate{{
		Entry: Entry{Name: "uptime check", Schedule: "every 30m"}, Parsed: s,
	}}, time.Now(), 90*time.Second+3*time.Minute)

	if c.WakesPerDay < 40 {
		t.Errorf("WakesPerDay = %.1f, want ~48 for a 30-minute job", c.WakesPerDay)
	}
	if !c.Heavy() {
		t.Error("48 wakes a day must be reported as heavy")
	}
}

func TestInfrequentJobsAreNotPricedAsDaily(t *testing.T) {
	const perWake = 90*time.Second + 3*time.Minute

	cases := []struct {
		sched   string
		wantMax float64
	}{
		{"0 9 * * 1", 0.2},  // weekly: ~0.14/day
		{"0 0 1 * *", 0.05}, // monthly: ~0.03/day
		{"0 9 * * *", 1.1},  // daily: 1/day
	}
	for _, tc := range cases {
		s, err := schedule.Parse(tc.sched, time.Local)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.sched, err)
		}
		c := EstimateCost([]Candidate{{
			Entry: Entry{Name: tc.sched, Schedule: tc.sched}, Parsed: s,
		}}, time.Now(), perWake)

		if c.WakesPerDay > tc.wantMax {
			t.Errorf("%q priced at %.2f wakes/day, want at most %.2f; "+
				"infrequent jobs must not be rounded up to a daily minimum",
				tc.sched, c.WakesPerDay, tc.wantMax)
		}
	}
}

func TestJobsSharingAScheduleCostOneWake(t *testing.T) {
	const perWake = 90*time.Second + 3*time.Minute
	s, err := schedule.Parse("0 9 * * *", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	// Three briefings all at 09:00 fire together.
	var cands []Candidate
	for _, n := range []string{"ai", "finance", "global"} {
		cands = append(cands, Candidate{
			Entry: Entry{Name: n, Schedule: "0 9 * * *"}, Parsed: s,
		})
	}
	c := EstimateCost(cands, time.Now(), perWake)
	if c.WakesPerDay > 1.1 {
		t.Errorf("three jobs at the same time cost %.1f wakes/day, want ~1",
			c.WakesPerDay)
	}
}
