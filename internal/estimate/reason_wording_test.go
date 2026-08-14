package estimate

import (
	"strings"
	"testing"
	"time"

	"github.com/junnam586/goguma/internal/config"
	"github.com/junnam586/goguma/internal/model"
)

// The reason is read in a settings pane by someone who wants to know why the
// Mac is being held awake for that long. It is the one string in this package
// with a non-technical audience, so it is worth pinning that it stays in
// plain words.
func TestReasonsAvoidJargon(t *testing.T) {
	cfg := config.Default()
	job := &model.Job{ID: "j", Name: "j", Detection: model.DetectMark}

	seen := map[int]string{}
	for _, n := range []int{0, 2, 5, 20} {
		var runs []model.Run
		for i := 0; i < n; i++ {
			runs = append(runs, model.Run{
				JobID: "j", Outcome: model.OutcomeOK,
				Duration: model.Duration(time.Duration(40+i) * time.Second),
				Started:  time.Now(), Ended: time.Now(),
			})
		}
		r := Compute(job, runs, cfg).Reason
		seen[n] = r
		t.Logf("%2d runs -> %q", n, r)
		for _, jargon := range []string{"p95", "P95", "percentile", "cold start", "×"} {
			if strings.Contains(r, jargon) {
				t.Errorf("%d runs: reason still says %q: %q", n, jargon, r)
			}
		}
		if r == "" {
			t.Errorf("%d runs: empty reason", n)
		}
	}

	// The learned case should say what it measured and how much slack it left.
	if got := seen[20]; !strings.Contains(got, "plus") || !strings.Contains(got, "%") {
		t.Errorf("learned reason does not state its headroom: %q", got)
	}
}
